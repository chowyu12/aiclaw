# 以上游 Electron + Go 内核项目为基重写 AIClaw

## 这次改的是什么

把 AIClaw 的内核换成 上游项目 的 `claw-agent`，界面换成它的 Electron 宿主与 Vue 页面，
**删掉全部内部平台能力**，同时**保留 AIClaw 现有的模型配置、插件系统、搜索引擎**。
打包用 上游 的 `@electron/packager` 规则，发布仍走我们已验证的 GitHub Actions。

落点：`aiclaw` 仓库 master 原地重写。

## 为什么值得换内核

两个项目架构同源——Electron 宿主 + Go 内核 + stdio——但 `claw-agent` 在三处明显更完整：

| | AIClaw 现状 | claw-agent |
| --- | --- | --- |
| 线协议 | 自定 JSON Lines，53 个命令的白名单 | JSON-RPC 2.0，session→turn→item 三层模型 |
| 审批 | **没有**。工具直接执行 | 执行命令、调外部工具默认要用户确认，危险命令硬拒绝 |
| computer use | osascript 后端，**做不到右键/双击/移动/滚轮**，schema 里只好删掉 | 委托宿主用 `desktopCapturer` 截屏、按平台合成输入，八个动作齐全 |
| 上下文管理 | 无。撑满即失败 | 主动压缩、截断恢复、中断后历史修复 |
| 会话检索 | FTS5 全文 | 同上，另有 `session/search` 协议方法 |

审批那条是决定性的：AIClaw 的插件系统能让模型执行任意本机命令，而它**没有任何确认环节**。
`claw-agent` 把审批做进了循环本身，不是事后补的拦截器。

computer use 那条也值得说：我在 AIClaw 里被迫从工具 schema 删掉的四个手势，
在 上游 里是**能做的**，因为它在宿主侧合成事件而不是 shell 出去调 osascript。

## 目标结构

```text
tools/claw-agent/         # 照搬：Go 内核（循环/工具/MCP/技能/记忆/SQLite/JSON-RPC）
packages/agent-client/    # 照搬：TS 客户端
apps/desktop/             # 照搬（去内部平台）：Electron 宿主 + Vue 界面
  src/main/               #   窗口、配置、会话、computer、技能、更新
  src/renderer/           #   App.vue + views/ + styles.css
internal/plugin/          # 保留：插件系统（bundle/manifest/权限/通道）
internal/plugins/         # 保留：computer-use / wechat / wecom / connector
internal/model/           # 保留其中的 Provider / SearchEngineConfig / Plugin 等
internal/store/           # 保留：插件与配置的持久化
scripts/                  # 照搬：冒烟测试
.github/workflows/        # 保留：我们已验证的发布流水线
```

删除：`tools/claw-mcp/`（3297 行，纯内部平台）、`CapabilitiesView.vue`、`catalog.ts`，
以及 `config.ts` / `SettingsView.vue` / `SkillsView.vue` 里的内部平台分支。

AIClaw 这边被替换掉的：`internal/core`、`internal/appserver`、`internal/appservice`、
`internal/protocol`、`cmd/aiclaw-core`、`electron/`、`renderer/`。

## 三处必须自己设计的接缝

照搬解决不了这三个地方——它们是「保留我们的」与「照搬他们的」正面相撞的点。

### 1. 模型配置：多 Provider 落到单 ModelConfig

`claw-agent` 的 `ModelConfig` 是**每会话一份**的 `{baseUrl, model, reasoningEffort,
temperature, maxTokens, contextWindow}`，没有 Provider 概念；API Key **只从进程环境变量来，
不经过协议帧**——这是他们有意的安全设计，保留。

AIClaw 的 `Provider` 是 SQLite 里的多条记录（`name/type/base_url/api_key/models[]/enabled`），
thread 引用 `provider_id + model_name`。

做法：**Provider 表保留，内核直接读它**。`claw-agent` 多了一个 `providers` 包，
用原来的 `gormstore` 打开 `~/.aiclaw/aiclaw.db`（旧版就在这个位置，升级上来
配过的模型服务原样可用），并通过 `provider/list|create|update|delete|models`
五个 JSON-RPC 方法暴露给宿主。`ModelConfig` 多一个 `providerId`：填了它，
Key 与端点由内核按 id 到库里查——**Key 不经协议帧**，宿主只拿到 `apiKeySet`
一位，比原设计「spawn 时经环境变量注入一把 Key」更进一步：多个 Provider
各有各的 Key，会话之间切换不用重启内核。环境变量 `AICLAW_LLM_KEY` 保留为
`providerId = 0` 时的兜底。

**实现时的两处修正**：

- 原计划让 `tools/claw-agent` 保持独立 Go 模块。实际合并进了根模块
  `github.com/chowyu12/aiclaw`（`go 1.27`）：内核要 import `internal/store`
  与 `internal/model`，单模块最直接；`modernc.org/sqlite` 与 `glebarez/sqlite`
  同时在依赖里，都是纯 Go，`CGO_ENABLED=0` 交叉编译到 Windows 验证过。
- Key 存在 SQLite 的 `providers.api_key` 列里，**明文**——这是 AIClaw 旧版的
  做法，按「保留现在模型配置」的决定沿用。它与 上游 「凭据只进钥匙串」
  的规则相悖，记在这里：将来若要加密这一列，改 `providers` 包一处即可，
  宿主与协议都不用动。

宿主侧不再有 `credentials.bin` 与 `ModelCatalog`（那是打公司模型网关的
`/v1/model-profiles`）。模型清单是 Provider 记录里的字符串数组，用户手写
或点「从端点拉取」并进去；上下文窗口由用户在配置页填。

### 2. 扩展模型：插件系统吃掉技能与 MCP 两页

AIClaw 的插件系统（bundle + manifest + 权限 + 贡献点）与 上游 的「技能 + MCP」
是两套模型。按决定：**以插件系统为准，技能页和 MCP 页重做成插件系统的两个视图**。

接缝天然存在：`claw-agent` 的 `session/start` 接受一组 `MCPServerConfig`，
而插件系统的 `mcpServers` 贡献点产出的正是这个。所以插件系统成为
**会话配置的来源**，而不是另一套并行的东西：

```text
插件（启用）→ 贡献点 → ┬ mcpServers → session/start 的 MCPServerConfig
                      ├ skills     → claw-agent 的技能目录
                      └ tools      → 宿主能力（computer use）
```

技能页展示的是「插件贡献的技能」，MCP 页展示的是「插件贡献的 MCP server」
加上用户手工添加的——与今天 AIClaw 里 MCP 既可手工添加也可由插件贡献是一致的。

**实现时的落点**（第 4 步）：

- 插件系统整个跑在内核里：`tools/claw-agent/internal/pluginhost` 包住
  `internal/plugin` 的 Installer / ConfigService / Host，暴露 `plugin/*`、
  `channel/*`、`wechat/*` 十三个 JSON-RPC 方法；宿主开会话前调一次
  `plugin/contributions`，把 MCP server、技能目录、computer use 开关并进
  `session/start`。
- **computer use 只有插件这一个开关**。上游 配置页上那个复选框删了；
  内置的 computer-use 插件启用即开，manifest 上原来的「仅 macOS」限制去掉——
  截屏与输入合成由宿主按平台做，Windows 也行。`internal/plugins/computeruse`
  （osascript 后端）随之作废，第 6 步删。
- 通道（微信、企业微信）收到的消息经 `server/channel.go` 的 `channelGateway`
  变成一轮：没见过的外部会话只记下等放行；放行了的跑在一个持久的受限会话里
  （`c_` 前缀，桌面侧边栏能看到历史），审批档位「无人值守」，工具只有只读的
  加放行时逐个勾的。内核通知在这里翻成旧的 `internal/protocol.Event`，
  通道插件代码一行没改。
- 一个二进制里 `modernc.org/sqlite` 与 `glebarez/go-sqlite` 都注册名为
  `sqlite` 的驱动，进程一启动就 panic。内核的会话库改用 glebarez 那个分支
  （它不用 FTS5，LIKE 检索不受影响）。第 3 步的二进制其实带着这个问题——
  桌面冒烟不拉内核，没测出来；现在多了一条直接驱动内核的 JSON-RPC 冒烟。

### 3. 搜索引擎：claw-agent 里没有它的位置

上游 的 web search 在 `claw-mcp/internal/provider/websearch.go` 里，
属于要删掉的内部平台部分。`claw-agent` 的工具注册表里**没有** web search。

做法：AIClaw 的搜索引擎配置保留，实现为一个**内置 MCP server**——
宿主在组装 `session/start` 的 MCP 列表时，若用户配了搜索引擎就加上它。
这样搜索能力走的是 claw-agent 已有的 MCP 通道，不用改内核的工具注册表；
换引擎、关掉引擎都只是改会话配置。

**实现时的落点**（第 5 步）：那个 MCP server 就是内核二进制自己——
`claw-agent mcp-search --app-db=…`，用 `mark3labs/mcp-go` 在 stdio 上暴露一个
只读的 `web_search` 工具，实现复用旧版的 `internal/tools/websearch`（Tavily /
SerpAPI / 阿里云 IQS）。宿主在有「启用且配了 Key」的引擎时把它挂进会话并标
`trusted`；每次调用重新读当前生效的引擎（列表里第一个启用的），换引擎不用重挂。
配置走 `search/list|create|update|delete|test` 五个方法，新增「搜索引擎」页，
带「试一下」真搜一次。

## 数据迁移

v2.0.2 的用户库里有会话、Provider、插件、搜索引擎、记忆。两套 SQLite schema 不同。

- **Provider / SearchEngineConfig / Plugin / PluginConfig / ChannelBinding**：
  表结构不动，`internal/store` 保留，这些直接沿用。
- **会话与消息**：AIClaw 的 `Thread` + `RolloutItem` 与 claw-agent 的会话库结构不同。
  **不做自动迁移**：历史会话以只读形式保留在旧表里，新会话走新库。
  理由是两边的条目模型（rollout kind vs item kind）不是一一对应的，
  强行翻译会产生看起来对、实际错位的历史，比明说「旧会话在这里、只读」更糟。
  这一条必须写进 release notes。

## 打包与发布

按决定取长：

- **打包**用 上游 的 `@electron/packager` 规则（含图标、Windows 版本资源 `resedit`）
- **发布**仍走我们的 GitHub Actions：三平台矩阵、`if-no-files-found: error`、
  tag annotation 作 release notes、SHA256SUMS

上游 的 `.gitlab-ci.yml`（11k 行）不迁移——它面向 GitLab，而这个仓库在 GitHub。

## 交付顺序

每一步结束时仓库必须是可构建的。

1. **搬内核**：`tools/claw-agent` + `packages/agent-client` 进来，删 `claw-mcp`。
   此时旧的 `electron/` 仍在跑，两套并存。
2. **搬宿主与界面**：`apps/desktop` 进来，摘掉内部平台（`CapabilitiesView`、`catalog.ts`、
   `config.ts` 的内部平台分支）。此时新界面能起来但还没有插件/搜索引擎。
3. **接模型配置**：内核读 Provider 表并暴露 `provider/*` 方法；会话按 `providerId`
   选模型服务，Key 在内核侧解析。新增「模型服务」页，配置页只留默认模型的选择。
4. **接插件系统**：`pluginhost` 进内核，贡献并进 `session/start`；新增「插件」页
   （权限、配置、通道状态、外部会话放行、微信扫码），技能页与 MCP 页标出插件来源。
5. **接搜索引擎**：`claw-agent mcp-search` 内置 MCP server，有启用的引擎时挂载；
   新增「搜索引擎」页。
6. **打包发布**：`@electron/packager` + 现有 GitHub Actions，删掉 `electron/`、`renderer/`、
   `cmd/aiclaw-core`、`internal/core` 等被替换的部分。

   落点：一台 ubuntu runner 出四个 zip（macOS arm64 / x64、Windows x64、Linux x64；
   内核 `CGO_ENABLED=0` 交叉编译，exe 元数据用 resedit，不需要 wine），命名
   `AIClaw-<tag>-<mac-arm64|mac-x64|win-x64|linux-x64>.zip`，与宿主 `updater.ts`
   里的资源名一一对应。发布 job 沿用原来的：tag 注释作说明、SHA256SUMS、空产物
   直接失败。更新器改查 GitHub Releases 的 `releases/latest`；macOS 一键升级由
   应用自己下载 zip、`ditto` 解包、替换正在运行的 .app 并重开（不再依赖外部脚本）。
   删掉的 Go 包按 `go list -deps ./tools/claw-agent` 之外的集合来定：`internal/core`、
   `appserver`、`appservice`、`memory`、`parser`、`provider`、`scheduler`、`selfupdate`、
   `workspace`、`tools/*`（除 websearch）、`plugins/computeruse`、`pkg/{harness,httputil,
   modelcaps,sse}`，以及 `cmd/aiclaw-core`。`internal/model` 与 `gormstore` 里旧会话的
   表与方法保留——库里的旧数据还在，只是没有界面读它。

## 风险与边界

- **这是一次换心手术**，不是重构。`internal/core` 及其上的一切（rollout 模型、
  工具分发、附件回灌、工具策略闸门）会被 claw-agent 的等价物取代。
  我为 AIClaw 写的那些测试大部分会随实现一起删除。
- **审批是新增的用户可见行为**。今天 AIClaw 执行命令不问，换核后会问。
  这是安全上的改进，但会改变使用手感，必须写进 release notes。
- **历史会话不迁移**。旧的 `threads` / `rollout_items` 表原样留在 `~/.aiclaw/aiclaw.db`
  里，新会话在 Electron userData 下的会话库；3.0 的界面不显示旧会话。
- **不做 OS 级沙箱**——这是 上游 明确记录的决定，照搬过来同样成立：
  命令直接在用户机器上跑，只有路径收敛与审批两道防护。
- **上游的 AGENTS.md 有 57k**，包含大量项目约定。搬代码时要一并读，
  否则会写出与它风格冲突的代码。
