# 技能调用事件

内核使用独立的 OpenTelemetry Logs logger 导出 `aiclaw.skill_invocation`。
默认关闭，不桥接应用日志，也不导出聊天消息或工具事件。

## 检测口径

- `explicit`：当前执行轮次的用户输入包含完整的 `$技能名`，且 `load_skill`
  成功加载该已启用技能。仅提及名称、加载失败不产生事件。
- `implicit`：模型未收到该显式指定而成功调用 `load_skill`，或通过 `read_file`
  成功读取已启用技能的 `SKILL.md`。文件路径在内核中解析，仅用于匹配，不导出。
- 同一执行轮次按「技能名 + 调用类型」去重；新轮次重新计数。运行中追加的用户输入
  属于当前轮次，代理间邮件不作为用户显式指定。
- Code Mode 中的嵌套工具遵循相同检测规则。暂不解析 shell 命令、其他工具的输出，
  也不推断模型是否实际遵循了技能。因此事件表示检测到访问，不表示任务完成。

## 数据边界

事件的 body 和 event name 均为固定值。仅发送以下六项属性：

| 属性 | 内容 |
| --- | --- |
| `event.name` | `aiclaw.skill_invocation` |
| `conversation.id` | 会话 ID |
| `turn.id` | 执行轮次 ID |
| `model` | 当前模型名 |
| `skill.name` | 已启用技能名 |
| `skill.invocation_type` | `explicit` / `implicit` |

附带事件时间和 INFO 级别。资源属性仅为固定服务名 `aiclaw-kernel` 和构建版本
`service.version`；发送前替换 SDK 自动合并的资源属性，忽略
`OTEL_RESOURCE_ATTRIBUTES` 和 `OTEL_SERVICE_NAME`。
不发送提示词、技能正文/描述、工具参数/输出、文件路径、用户名、邮箱或模型 API Key。
技能名、模型名和会话 ID 会发送到用户配置的收集器。

## 启用

为内核进程设置以下环境变量，桌面端启动的子进程继承桌面端环境：

```sh
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://127.0.0.1:4318/v1/logs
export OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf
```

也支持通用 `OTEL_EXPORTER_OTLP_ENDPOINT`（自动追加 `/v1/logs`）和
`OTEL_EXPORTER_OTLP_HEADERS`，日志专用 endpoint/headers 优先。
仅支持 HTTP/protobuf，使用系统 TLS 信任及默认 HTTP 代理。
为保证发送边界，使用专用 HTTP 客户端：请求超时固定 10 秒，不压缩；
不读取 OTel 自定义证书、HTTP 超时或压缩环境配置。

`OTEL_LOGS_EXPORTER` 未设置或为 `none`，以及 `OTEL_SDK_DISABLED=true`，均不导出。
不支持的 exporter/protocol 会关闭技能遥测并记录固定配置错误，不阻止内核启动。
事件异步批量发送，正常退出最多等待 3 秒刷新；收集器失败不阻塞工具执行，
进程硬退出或队列满时可能丢失事件，不承诺可靠计数或审计完整性。

## 验证

`internal/telemetry/skills_test.go` 使用本地 HTTP 收集器解码实际 OTLP protobuf，
检查完整属性白名单及环境私密信息未发送，并验证关闭时没有请求。
`tools/claw-agent/internal/agent/skill_telemetry_test.go` 验证显式/隐式分类、失败加载、
直接和 Code Mode 文件读取、每轮去重和新轮次重置，以及正文/参数未进入事件。
