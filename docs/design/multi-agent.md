# 子 agent（多 agent 协作）

对照 Codex 的 **multi_agents_v2** 做，工具名、参数、返回形状与它一致，方便跟进。

对照的版本：openai/codex `main` @ `bcd6d9ab6b`（2026-09-30）。

## 对照表

| AIClaw | Codex |
|---|---|
| `tools/claw-agent/internal/agent/collab.go`（工具定义、邮箱、等待、分叉历史） | `codex-rs/core/src/tools/handlers/multi_agents_v2/*.rs`、`multi_agents_spec.rs` |
| `tools/claw-agent/internal/server/collab.go`（协作树、投递、后台跑轮次） | `codex-rs/core/src/agent/control/*`（spawn / send / list / interrupt） |
| `<subagent_notification>` 邮件 | `codex-rs/core/src/context/subagent_notification.rs` |
| 工具说明里的「何时用、怎么拆」 | `spawn_agent_tool_description`（multi_agents_spec.rs） |

跟进时先看这几个 Codex 文件的 diff，再对照改 AIClaw 这两个文件。

## 六个工具

| 工具 | 参数 | 返回 |
|---|---|---|
| `spawn_agent` | `task_name`（小写字母数字下划线）、`message`、`fork_turns?`（`all` 默认 / `none` / 正整数字符串） | `{"task_name":"/root/x","nickname":"x"}` |
| `send_message` | `target`、`message` | 空（只投递，不叫醒） |
| `followup_task` | `target`、`message` | 空（目标闲着就开一轮；不能发给根） |
| `wait_agent` | `timeout_ms?`（默认 120000，范围 10000～1800000，太小的截到下限并说明） | `{"message":"Wait completed." / "Wait interrupted by new input." / "Wait timed out.","timed_out":bool}`，**不返回内容** |
| `list_agents` | `path_prefix?` | `{"agents":[{"agent_name","agent_status"}]}` |
| `interrupt_agent` | `target` | `{"previous_status"}` |

状态值：`running` / `completed` / `errored` / `interrupted` / `idle`。

## 语义

- **路径**：用户会话是 `/root`，它开的 `research` 是 `/root/research`。
  - 以 `/` 开头的是规范名；不带 `/` 的名字指调用者自己的子任务。
- **邮箱**：agent 间的消息与用户插话走同一个队列（`Session.pending`）。
  - 目标在跑：消息在它下一次采样前插进历史。
  - 目标闲着：下一轮开头插进去。
  - 进历史的是隐藏的 user 消息，时间线上只留一行提示。
- **完成通知**：子 agent 一轮结束时，状态和最终回答以 `<subagent_notification>` 投进父 agent 的邮箱。
  - 父 agent 那时闲着的话，**自动开一轮**来接收结果。
  - 被打断的不叫醒父 agent：那多半是用户在停整棵树。
- **分叉历史**：`fork_turns` 按「用户说的一句话」数轮。
  - 只带完整的部分：父会话正在调 `spawn_agent`，那条工具调用还没有结果，要去掉。
- **共用工作区**：与 Codex 一样，所有 agent 的工作区相同。并行改代码时让各自的写入范围不重叠。
- **不进 exec**：代码模式下这组工具留在 exec 外面，与 Codex 一致。

## 与 Codex 的差别

- **模型与推理强度跟父会话走**，不开放 `model` / `reasoning_effort` 覆盖：AIClaw 的模型绑在模型服务上，按名字换模型说不清换到哪个服务。
- **没有 `agent_type`（角色）**。
- **上限**：最多嵌套 2 层；一棵树上同时在跑的子 agent 最多 6 个（`server/collab.go` 的常量）。
- **树只活在内存里**：内核重启后，子会话还在、能打开能继续聊，但不再属于哪棵树。
- **只给用户自己的会话**：通道会话（微信、企业微信）不挂这组工具。
- **停止会连坐**：用户在界面上停下一个会话时，它开出去还在跑的子 agent 一并停下。

## 界面

- **侧边栏**：子会话按 `parentId` 缩进挂在父会话下面，跑着的亮点。父会话不在列表里时照常平铺。
- **审批框**：不是当前会话发来的审批，写上「来自「↳ research」」。
