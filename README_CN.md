# AIClaw

[English](README.md) · **简体中文**

本地优先的 Agent 桌面应用：Electron 宿主 + Go 内核（`claw-agent`），通过 stdio 上的
JSON-RPC 通信。模型服务、插件、搜索引擎、会话全部存在本机；应用不起 HTTP 服务。
界面支持简体中文与英文（「配置」页顶部切换，新装默认英文）。

## 许可证

AIClaw 采用 [个人与商业许可协议](LICENSE)（[中文译文](LICENSE_CN.md)）。
**个人非商业用途免费；企业或其他组织使用须取得付费商业授权。**
企业内部使用、评估、员工为公司工作、个人付费接单及其他商业用途均须授权。
商业授权请[联系维护者](https://github.com/chowyu12/aiclaw/issues)，价格与范围单独约定。
模型及外部服务费用另计。本项目采用带用途限制的自定义源码可见许可。

## 能做什么

- **持久任务**：目标、计划与预算控制，跨重启恢复检查，从指定消息分叉会话，以及内置文本文件工具的差异查看与冲突保护撤销。见 [工作流说明](docs/design/workflows.md)。

- **对话与执行**：模型在本机读写文件、执行命令、调用 MCP 工具。桌面提供默认、严格、全部放行三档。
  默认档位使用当前会话模型单独审查命令和外部工具，低风险操作自动执行，其余或审查失败时请你确认；规则已识别的高风险命令直接请你确认，可信只读工具不调用审核模型。文件工具读写工作区内外文件不审批；严格档位写文件仍需确认。
  审核在已支持的百炼/DeepSeek 端点关闭思考，最多等待 8 秒后转人工；界面分别显示审核与人工确认耗时。每次模型检查仍会增加用量与等待时间，未知端点或仅思考模型不强行切换模式。
  旧桌面默认/无人值守配置迁回默认档位，已有渠道授权不自动扩大；危险命令与凭据路径保护仍生效。上下文撑满前主动压缩。
  输入框右下角的话筒可以语音输入，听写成文字后再发。
- **子 agent**（参照 Codex 的 multi_agents_v2）：说「用子 agent 并行做……」，模型把能并行的
  边角活交给子 agent，自己接着做关键路径上的事；子 agent 做完，结果自动交回来汇总。
  子会话缩进挂在父会话下面、默认折叠。见 docs/design/multi-agent.md。
- **会话引用**（参照 Codex 的 task mentions）：输入框里打 `@` 选一个会话，模型会先用
  `read_thread` 读它再回答。见 docs/design/thread-references.md。
- **会话管理**：分组、折叠、搜索；归档的会话收进「设置 → 已归档」，可以恢复。
- **定时任务**：每天 / 工作日 / 每周几 / 每隔一段时间 / 只跑一次，到点自动开一个会话去做，
  跑完发系统通知；也可以在对话里直接说「每个工作日 9 点帮我汇总邮件」。
- **对话配置应用**：在桌面会话里通过可编辑卡片配置邮件、搜索引擎、MCP、插件和定时任务。
  密钥在卡片中填写，不回传给模型。见下方「通过对话配置 AIClaw」。
- **Office 文件**：读 Word / Excel / PPT（docx、xlsx、pptx），生成 Word、Excel、PPT，
  在 Word 里精确替换文字、改已有 Excel 里的某几张表。
- **模型服务**：多个 OpenAI 兼容端点（OpenAI、通义、Kimi、OpenRouter、Claude / Gemini
  的兼容入口、自建），各带 Key 与模型清单；会话之间随时切换。Key 加密存在本机配置库里
  （AES-256-GCM，密钥在库旁边的 `secret.key`，0600），不经协议帧、不进日志；
  沙箱对库文件与密钥文件一律禁读。
- **插件**：随应用分发——
  - **邮件**：用你的邮箱列信、读信、存附件、发信、回信；发信每次先确认；公司域名的企业邮箱
    按 DNS 自动找到服务器；
  - **computer use**：截屏 + 鼠标键盘，启用即授权；
  - **微信**：扫码登录，可以连多个号；
  - **企业微信**：智能机器人，可以连多个。

  也能从目录装自己的（技能、MCP server）。外部会话发来的消息先记下等你放行，放行后跑在
  默认无人值守的会话里：需要确认的操作不执行；可为单个外部会话选择「全部放行」，
  允许直接写文件、执行命令。旧会话保持无人值守。已配置的内置只读搜索可直接使用。
- **搜索引擎**：Tavily / SerpAPI / 阿里云 IQS，启用后模型多一个 `web_search` 工具。
- **浏览器**：模型拿到页面上可交互元素的编号列表，按编号打开、点、填、选、滚、抽正文、截图
  （browser-use 那套 DOM indexing 的做法，用 Electron 自带的 Chromium，不装 Playwright）。
  可以用应用自带的窗口，也可以装「AIClaw 浏览器助手」扩展，在你自己的 Chrome / Edge 里、
  用你已有的登录、在后台标签页里操作。打开网址要确认。见 docs/design/browser.md。
- **多模态**：对话之外的四件事各交给一个模型——看图（对话模型不认图时替它读）、
  听写、朗读、画图。在「模型服务」页给模型勾上能力，再到「配置」页各选一个；
  没配的角色对应的工具不会出现在会话里。能力可以一键按 [models.dev](https://models.dev)
  自动标记。百炼（DashScope）的兼容模式只有对话，这三件事内核会自动改走它的原生接口
  （画图选 qwen-image / 万相，朗读选 qwen3-tts，听写选 qwen3-asr）。
- **技能**：技能目录是 `~/.agents/skills`（`npx skills add -g` 装的位置，Codex、Cursor 等也读它），
  一并认 Claude Code、Codex、npm 全局包与插件带来的 `SKILL.md`。模型跑的命令用终端里的
  PATH，nvm / Homebrew 装的命令行工具都找得到。
- **用量**：「设置 → 用量」按天看 token、模型调用、工具与技能。

### 通过对话配置 AIClaw

直接描述想改什么，例如：

- 「帮我配置邮箱，测试通过后启用。」
- 「把默认搜索引擎换成 Tavily。」
- 「添加一个 MCP，我把 JSON 填进配置卡片。」
- 「从本地目录安装插件」或「修改这个插件已有连接的配置」。
- 「每天早上 8 点检查公开项目更新」或「把这个任务改到 9 点」。

助手会先检查当前配置，在对话里打开可编辑卡片，等待你确认或取消。保存复用设置页已有的
管理接口，并反馈保存结果及支持的连接测试结果。密码、授权码、API Key、MCP 请求头和
环境变量从卡片直接交给桌面宿主，模型只收到状态；凭据留空表示保留已有值。

此入口仅供交互式桌面主会话使用。插件从本地目录安装；新建渠道连接和微信扫码仍走设置页。
每张 MCP 导入卡片支持一个服务。工具配置在运行中及自动排队的任务结束后重新挂载，或重新
打开会话时更新；定时任务只在应用运行时执行。保存与测试是分开的操作：测试失败可能已经
保存部分设置，之后取消不会撤销此前已保存的改动。

完整操作范围、OAuth 流程与验证边界见[对话配置使用说明](docs/conversation-configuration.md)。

## 安装

**macOS 一条命令**（下载最新 Release、装进「应用程序」、去掉隔离标记、打开）：

```bash
curl -fsSL https://raw.githubusercontent.com/chowyu12/aiclaw/master/scripts/install-mac.sh | bash
```

Apple Silicon 与 Intel 都认；已经手动下好 zip 的话 `install-mac.sh -l` 直接装
`~/Downloads` 里最新的那个。脚本只做四件事：找包 → 退掉正在跑的 → 替换 `.app` →
`xattr -dr com.apple.quarantine`——包未签名，不去这个标记 Gatekeeper 会拒绝打开。

**Windows**：到 [Releases](https://github.com/chowyu12/aiclaw/releases) 下载
`AIClaw-<版本>-win-x64-setup.exe`，按安装向导确认安装。后续安装到同一位置并覆盖升级，
配置、会话和记忆保留在原用户数据目录。仍提供 `AIClaw-<版本>-win-x64.zip` 便携版。
原 ZIP 用户首次使用安装版后从新快捷方式启动；原解压目录不自动删除。

**Linux**：下载 `AIClaw-<版本>-linux-x64.zip`，解压到任意目录运行。

应用内也会检查新版本：macOS 一键替换并重开；Windows 下载并校验安装包，成功启动安装器后退出，
由用户确认覆盖安装（取消时可重新打开旧版本）。只有 ZIP 的旧 Release 仍下载到「下载」目录。
Linux 保持下载 ZIP 的方式。Windows 安装包目前未签名，系统可能提示发布者未知；SHA256 校验不等同于发布者签名。

## 目录

```text
apps/desktop/           Electron 宿主 + Vue 界面（shared/locales 是界面的英文词典）
apps/browser-extension/ 「AIClaw 浏览器助手」扩展
packages/agent-client/  内核的 TS 客户端
tools/claw-agent/       Go 内核：循环 / 工具 / MCP / 技能 / 记忆 / 会话库 / 子 agent / JSON-RPC
internal/plugin         插件系统（bundle、manifest、权限、配置、通道）
internal/plugins        内置插件：邮件、computer use、微信、企业微信
internal/store          应用库（SQLite，gorm）：模型服务、插件、搜索引擎、通道授权
scripts/                冒烟测试、打包、图标、一键安装脚本
docs/                   设计文档；docs/agent-loop.md 改循环前必读
```

## 开发

需要 Go 1.27+ 与 Node 22+。

```bash
make dev      # 编译并起应用
make check    # 交付前全量预检：格式、vet、测试、类型、两套冒烟
make scenarios # 发版前场景测试：真打模型，对话 / 文件 / 命令 / 审批 / 看图 / 画图 / 朗读 / 听写 / 搜索 / 并发
make help     # 其余 target
```

`make check` 不打模型，每次改动都跑；`make scenarios` 用「配置」页里的默认模型与
多模态角色真跑一遍用户会做的事，花钱也慢，只在发版前跑。

界面文案：源码里写中文原文 `t("新建对话")`，英文放在 `apps/desktop/src/shared/locales/en/`；
`scripts/test-i18n.ts` 会检查每一处 `t("…")` 都有英文。内核同一套做法：`internal/i18n` 的
`i18n.D("…")` / `i18n.E("…")`，英文在 `internal/i18n/en_*.go`。给模型看的文字（系统提示词、
工具说明）直接用英文写。

数据目录：`~/.aiclaw/`（`aiclaw.db` 应用库、`plugins/` 插件、`memory.md` 全局长期记忆）；
技能在 `~/.agents/skills`；会话库与界面配置在 Electron 的 userData 目录。
记忆分两层：跟项目无关的进全局 `memory.md`，某个项目的约定与踩过的坑记在
`<工作区>/.aiclaw/memory.md`，跟着仓库走，`remember` 工具有工作区时默认记这里。

## 打包与发布

`make package` 用 `@electron/packager` 在一台机器上出四个包（macOS arm64 / x64、
Windows x64、Linux x64），内核 `CGO_ENABLED=0` 交叉编译。推 `v*` tag 触发
GitHub Actions：测试 → 打包 → Windows runner 生成 NSIS 安装包并验证安装/覆盖/卸载和数据保留 →
建 Release（tag 注释作发布说明，附 ZIP、Windows Setup.exe 和 SHA256SUMS）。
本地可在 `make package-win` 后运行 `npm run package:win-installer`；推荐在 Windows 上构建和验收安装器。

macOS 包未签名：用上面的安装脚本装不用管；手动解压的话第一次打开要右键「打开」，
或 `xattr -dr com.apple.quarantine /Applications/AIClaw.app`。本机 `make install-mac`
会构建并装进「应用程序」。
