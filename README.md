# AIClaw

本地优先的 Agent 桌面应用：Electron 宿主 + Go 内核（`claw-agent`），通过 stdio 上的
JSON-RPC 通信。模型服务、插件、搜索引擎、会话全部存在本机；应用不起 HTTP 服务。

## 能做什么

- **对话与执行**：模型在本机读写文件、执行命令、调用 MCP 工具。执行命令与调用外部
  工具前会请你确认（可选严格 / 无人值守档位）；危险命令硬拒绝。上下文撑满前主动压缩。
- **模型服务**：多个 OpenAI 兼容端点（OpenAI、通义、Kimi、OpenRouter、Claude / Gemini
  的兼容入口、自建），各带 Key 与模型清单；会话之间随时切换。Key 只进本机配置库，
  不经协议帧、不进日志。
- **插件**：随应用分发 computer use（截屏 + 鼠标键盘，启用即授权）、微信（扫码登录）、
  企业微信（智能机器人）三个插件；也能从目录装自己的（技能、MCP server）。外部会话
  发来的消息先记下等你放行，放行后跑在只读工具的受限会话里。
- **搜索引擎**：Tavily / SerpAPI / 阿里云 IQS，启用后模型多一个 `web_search` 工具。
- **技能**：一并认 Claude Code、Codex、npm 全局包与插件带来的 `SKILL.md`。

## 目录

```text
apps/desktop/        Electron 宿主 + Vue 界面
packages/agent-client/  内核的 TS 客户端
tools/claw-agent/    Go 内核：循环 / 工具 / MCP / 技能 / 记忆 / 会话库 / JSON-RPC
internal/plugin      插件系统（bundle、manifest、权限、配置、通道）
internal/plugins     内置插件：微信、企业微信
internal/store       应用库（SQLite，gorm）：模型服务、插件、搜索引擎、通道授权
scripts/             冒烟测试、打包、图标
docs/                设计文档；docs/agent-loop.md 改循环前必读
```

## 开发

需要 Go 1.27+ 与 Node 22+。

```bash
make dev      # 编译并起应用
make check    # 交付前全量预检：格式、vet、测试、类型、两套冒烟
make help     # 其余 target
```

数据目录：`~/.aiclaw/`（`aiclaw.db` 应用库、`plugins/` 插件、`skills/` 技能、
`memory.md` 长期记忆）；会话库与界面配置在 Electron 的 userData 目录。

## 打包与发布

`make package` 用 `@electron/packager` 在一台机器上出四个包（macOS arm64 / x64、
Windows x64、Linux x64），内核 `CGO_ENABLED=0` 交叉编译。推 `v*` tag 触发
GitHub Actions：测试 → 打包 → 建 Release（tag 注释作发布说明，附 SHA256SUMS）。

macOS 包未签名，第一次打开要右键「打开」，或
`xattr -dr com.apple.quarantine /Applications/AIClaw.app`。
