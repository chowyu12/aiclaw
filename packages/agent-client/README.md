# @aiclaw/agent-client

`tools/claw-agent` 的 TypeScript 客户端。桌面宿主用它拉起并驱动自研的 Agent 执行内核。

线格式是一行一个 JSON-RPC 2.0 帧走 stdio。`src/protocol.ts` 与
`tools/claw-agent/internal/protocol/protocol.go` **手工保持同步**——协议是我们自己的，
字段不多，暂不引入代码生成；改一边记得改另一边，以 Go 侧为准。

审批是 claw-agent 向宿主发的请求（有 id），宿主必须调 `respondApproval` 回应，
否则那一轮会等到 30 分钟超时后按拒绝处理。

### Submission identity and recovery

`turnStart(sessionId, text, images?, audioPaths?, references?, requestId?)` accepts an
optional stable request ID. Retry the same payload with the same ID only against the
same session, including after a kernel restart: it returns the original turn receipt without executing twice.
Reusing the ID with different input fails. User-message notifications echo `requestId`
so clients can reconcile optimistic UI entries, including identical text and image-only
messages. Receipts are stored in SQLite and scoped to the session.

A broken stdio connection may mean the kernel already accepted a request. The transport
rejects pending calls and never replays them automatically across a process restart.
The desktop restores unconfirmed input to the composer for user review, retaining
attachments and references. Confirmed input is not restored as an unsent draft.


### 持久工作流

`sessionWork(id)` 读取目标、恢复记录和分叉来源；`goalSet` / `goalUpdate` 管理目标与计划。
`sessionFork(id, itemId?, model?)` 从全部历史或指定消息创建独立普通会话。
`sessionRecover(id, requestId, "resume" | "dismiss")` 只允许恢复尚未消费的输入，执行结果
不确定的输入只能检查后移除提示。`changesList` / `changesUndo` 查看、撤销内置文本文件
工具的改动，检测到外部修改时拒绝覆盖。详见 [工作流设计](../../docs/design/workflows.md)。
