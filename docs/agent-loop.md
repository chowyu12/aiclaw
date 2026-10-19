# Agent 循环：对照 Codex 源码的取舍

内核是自研的，但循环的形状照着 [openai/codex](https://github.com/openai/codex)
的 `codex-rs/core` 过了一遍——那是一个被真实用量磨过的实现，它踩过的坑没必要
自己再踩一次。这份文档记的是**逐条的取舍结果**：哪些照搬了、哪些改了形状、
哪些故意没做。没有这份记录的话，下一个人读到「为什么这里不像 Codex」时，
只能猜是漏了还是有意的。

对照的版本：`codex-rs/core` 的 `session/turn.rs`、`tasks/`、`tools/`、
`compact.rs`、`context_manager/`、`responses_retry.rs`。

## 照搬了的

### 会话 → 轮次 → 条目三层事件模型

Codex 的 `submission_loop → SessionTask → run_turn → run_sampling_request`。
我们的 `RunTurn → loop → sample` 是同一个分层，少了 `SessionTask` 那一层抽象
（它是为了让压缩、review 复用轮次机制；我们的压缩是个函数，不需要）。

### 一轮里的 `needs_follow_up`

有工具调用就继续，没有就收尾。我们多加了一条：**没有工具调用但队列里有
用户输入，也继续**（见下面的转向）。

### 并发闸门是一把读写锁

`internal/agent/turn.go` 的 `executeTools`。只读工具拿读锁可以彼此并行，
有副作用的工具拿写锁——它既不会和别的写并行，也不会和正在跑的读并行。
这正是 Codex `tools/parallel.rs` 里 `Arc<RwLock<()>>` 的用法。

用一把锁而不是「读的并行、写的串行」两条独立路径，是因为后者挡不住
`read_file` 和 `write_file` 同时落在一个文件上。

差别：Codex 让每个 handler 自己声明 `supports_parallel_tool_calls`，
我们按 `Effect` 推导（`tools.Tool.ParallelSafe`）。它的工具集大、分类杂，
我们的小而明确，少一个「新增工具时忘了填」的字段。

### 重试与终态分类

`responses_retry.rs` 的核心判断是「问题出在传输还是出在请求本身」。
我们把它落在 `internal/llm/errors.go`：`Retryable()` 与
`ContextWindowExceeded()`。分类必须在 llm 层做——上层只拿到字符串的话，
就只能靠 `strings.Contains` 猜，换一个上游渠道就静默失效。

退避 1s→2s→4s→8s 带抖动，封顶 30s，最多 4 次。Codex 的连接重试是 5s 起
封顶 60s；我们调短了，因为这是个桌面应用，用户在盯着界面看。

### 自动压缩

`compact.rs`。阈值 90%、保留用户消息上限 20000 token 都照抄。重建历史的
规则也一样：**系统提示词 + 近期用户消息（从新往旧收）+ 一份摘要**。

保留用户消息而不是最近几轮对话，是因为用户说过的话是任务本身，摘要复述
容易失真；助手消息和工具结果是过程，摘要就是用来替代它们的。

顺带一个在 Chat Completions 上**必须**的性质：这样重建出来的历史里没有任何
`tool_calls`，也就不可能留下「有调用没有结果」的孤儿——那是 400。

压缩之后还报超窗时从头丢消息（`dropOldest` ↔ `remove_first_item`）。
从头丢是为了保住上游的 prompt cache，从中间挖洞会让整段缓存失效。

### 工具输出在写入历史时截断

普通工具结果进入模型历史仍以约 32 KiB 为预算，保留头尾并说明省略范围；
超限结果另存当前会话的本地 SQLite 状态，返回 `output_id`，可用 `read_tool_output`
按 UTF-8 字节偏移续读。命令与 Code Mode 默认预览最多 24 KiB，保留的副本最多
8 MiB；达到保留上限时 `complete=false`，不能把副本当成完整源数据。保存失败同样
明确提示不可取回。新副本随会话恢复，并随会话删除；不跨会话或分叉共享，不补回旧截断数据。

`read_tool_output` 只读取当前会话的结果 ID，每页最多 4096 字节，返回
`nextOffset`、`totalBytes`、`hasMore` 和 `complete`，不重新执行原命令。它同时可在
Code Mode 内调用。Code Mode 的单次嵌套结果超过 24 KiB 时也保留原返回并打印结果 ID，
即使脚本只打印 slice 预览也可补读。命令的 stdout/stderr 分别最多捕获 8 MiB，合并后的副本同样最多
8 MiB；任何一层触顶都标记不完整。

`read_file` 在完整文件上定位行号，再读取最多 24 KiB 的页；长行或大文件可按返回的
`byte_offset` 继续，拒绝非字符边界偏移。仍执行原有敏感路径校验，不执行文件内容。
常见 `head/tail/sed -n` 命令另附部分选择提示；已被命令管道删掉的源内容无法从
捕获副本还原，不能把“捕获完整”解释成“查询源完整”。系统及 Code Mode 指令要求把截断和主动 `head/tail/slice` 视为部分证据，在判断无数据
前补读相关字段/分页；无法补读时明确证据不完整。此规则引导模型，不是语义正确性的硬保证。

### 中断之后的历史修复

Codex 往历史里写一句「用户故意中断了上一轮，被中止的命令可能已经部分执行」。
措辞照搬，因为那句话里「**可能已经部分执行**」直接影响模型要不要重做。

我们还多做了一件 Chat Completions 才需要的事：给每个没有结果的 `tool_call`
补一条结果消息。少一条，下一轮开口就是 400，整个会话废掉。

### 转向输入排队

`input_queue`，在下一次采样之前插进历史。桌面输入框现在默认发送 `mode=queue`：
输入先保存到提交记录，当前轮正常完成后按接收顺序分别开启新轮次，不取消当前生成或工具。
输入框上方显示队列，可取消或选择“立即引导”；停止、失败或重启后保留未处理输入，
不自动继续，需用户明确发送。图片、音频路径和会话引用随队列输入一起保存。

“立即引导”把选定的排队输入移交当前轮次，采用以下插话逻辑；未指定 mode 的旧 API
调用保留原有行为。2026-10-03 跟进 Codex 0.159 的 `instant_interrupt`：插话会取消正在
等待的模型响应或重试退避，再用新上下文采样。已经展示的部分正文保留，未执行的
旧工具调用不写入历史，也不派发。新输入也会取消当前工具批次、Code Mode 脚本和嵌套工具；
取消依赖工具遵守 context，不能回滚已发生的副作用。保留已完成输出，给所有已派发调用
补齐结果，并提醒模型检查部分执行状态，再处理新输入。音频路径与图片、会话引用一起排队。

Code Mode 的 Promise 由单一 VM 事件循环结算，底层只读工具可通过 `Promise.all` 并行；
写操作与未知副作用工具在同一运行时的所有脚本间互斥。工具等待不计入脚本 CPU 预算，
脚本返回前会收尾已派发调用，避免操作脱离轮次继续执行。

模型流必须提供 `[DONE]` 或正常完成原因，缺失时作为可重试的传输中断，保留部分正文但
不派发工具。`length`、`content_filter` 等非正常结束是不可重试的不完整响应；压缩遇到
不完整摘要保留原历史并报错，不使用半截摘要或悄悄继续压缩。

轮次收尾与接受插话共用互斥锁：收尾之后到达的输入另起一轮，避免输入排进已经
不会再次采样的轮次。新轮次通过 `runMu` 串行执行，邮箱的重复唤醒不会多跑一次模型。

### 本地通信失败与请求身份

对照 Codex 0.160 的断线队列修复，但 AIClaw 是本地 stdio 子进程，进程退出不是
可以继续同一轮的 WebSocket 重连。`turn/start` 新增可选 `requestId`，同一会话的相同请求重复提交返回原回执（跨内核重启持久化），不重复执行；相同 ID 改正文或附件会报错。
只缓存载荷的 SHA-256，避免把图片再留一份。`userMessage.requestId` 用于认领对应回显，
相同文字或纯图片消息也能区分；重复条目通知不会重复添加或认领另一条。

发送失败且没有用户条目确认时，界面恢复原草稿、附件与引用；已经确认的消息不恢复。
进程退出后不自动重放执行结果未知的输入。请求、执行状态及完整消息记录持久化到 SQLite；
恢复面板区分尚未消费的输入与执行结果待确认的输入，只有前者允许显式恢复处理。
目标与计划、普通会话分叉以及文件差异/撤销的实现和边界见 [持久任务工作流](design/workflows.md)。

### 自定义模型目录

模型选择只使用启用且配好凭据的 provider 清单。默认模型被删掉时从当前清单重新
选择，模型名去掉能力与窗口标记，窗口配置一并更新。空目录不补内置模型。
`/models` 返回错误或结构不合法时明确失败，不返回上次拉取结果；手工维护的清单
保持原样，成功拉取仍按原产品规则合并，因为端点不一定列出全部自定义部署。

本次对照来源：[0.159](https://github.com/openai/codex/releases/tag/rust-v0.159.0)、
[0.160](https://github.com/openai/codex/releases/tag/rust-v0.160.0)。子代理准备状态的
对应实现见 [multi-agent.md](design/multi-agent.md)。

## 故意没做的

### 流式过程中就派发工具调用

Codex 在 `handle_output_item_done` 里，一个 `output_item.done` 到了就把工具
塞进 `FuturesOrdered` 开跑，不等整条流结束。

**这条不搬。** 它的收益依赖 Responses API 的形状：多个 output item 之间隔着
大段推理，先派发能省下真实的等待。Chat Completions 不是这样——`tool_calls`
分片在流的最末尾才出现，和 `finish_reason` 之间只差几毫秒，省不下什么。

而代价是实打实的：一旦提前派发，流在后面断掉时就面对「工具已经跑了、
但这次采样要重试」的局面，重试会让有副作用的工具执行两次。

换句话说，这是 Codex 为它的协议做的优化，不是循环设计本身的一部分。

### 轮次任务抽象（`SessionTask` / `RegularTask` / `CompactTask` / `ReviewTask`）

Codex 需要它是因为压缩和 review 都要复用「一轮」的生命周期（中断、审批、
事件）。我们的压缩是 `Session.compact` 一个函数，没有 review 功能。
加一层 trait 只会多一层间接。

### 审批缓存（approve for session）

Codex 的 `ApprovalCacheKey` 让用户对同一条命令只批一次。UX 上确实好，
但**没有沙箱**的前提下，「这次批了就一直批」是一个需要单独评审的安全决定，
而且它要改协议（审批回应得有第三个选项）、改 UI。这一版不做，留作单独议题。

### 轮次后压缩（PostTurn compaction）

Codex 有 `model_post_turn_compact_threshold_percent`，在一轮结束后主动压缩，
让下一轮从干净的上下文开始。我们只做轮次前和轮次中：桌面应用的会话通常
更短，轮次后压缩要多花一次模型调用，收益不够明显。要加的话入口在
`Session.needsCompaction` 的调用点。

### `FunctionCallOutputPayload.success`

Responses API 的字段，Chat Completions 的 tool 消息没有对应位置。
工具失败靠内容里的「错误：」前缀告诉模型，靠条目上的 `toolFailed` 告诉宿主。
