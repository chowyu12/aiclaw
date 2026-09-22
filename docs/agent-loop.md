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

`record_items` 里做截断，而不是在工具返回时做。界面上留的是工具的完整输出，
只有给模型的那份被截。掐头去尾都不行——命令的输出头部是它在做什么，
尾部是结果和报错，所以截中间。

预算我们定 32KB（Codex 的 fallback 是 10000 字节）。放大是因为
`read_file` 的正常用法就是读源码文件，10KB 会把它切废。

### 中断之后的历史修复

Codex 往历史里写一句「用户故意中断了上一轮，被中止的命令可能已经部分执行」。
措辞照搬，因为那句话里「**可能已经部分执行**」直接影响模型要不要重做。

我们还多做了一件 Chat Completions 才需要的事：给每个没有结果的 `tool_call`
补一条结果消息。少一条，下一轮开口就是 400，整个会话废掉。

### 转向输入排队

`input_queue`，在下一次采样之前插进历史。轮次进行中用户又发话，排队而不是
拒绝——他想插话往往正是因为看见这一轮跑偏了，让他先等完一轮是最不该做的。

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
