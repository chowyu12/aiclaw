# @aiclaw/agent-client

`tools/claw-agent` 的 TypeScript 客户端。桌面宿主用它拉起并驱动自研的 Agent 执行内核。

线格式是一行一个 JSON-RPC 2.0 帧走 stdio。`src/protocol.ts` 与
`tools/claw-agent/internal/protocol/protocol.go` **手工保持同步**——协议是我们自己的，
字段不多，暂不引入代码生成；改一边记得改另一边，以 Go 侧为准。

审批是 claw-agent 向宿主发的请求（有 id），宿主必须调 `respondApproval` 回应，
否则那一轮会等到 30 分钟超时后按拒绝处理。
