# 从 Wails 迁移到 Electron 的设计方案

## 为什么这件事可行且成本可控

先说结论：**Go 核心一行都不用改**。

`internal/` 下 19 个包对 Wails 的引用数是 **0**。`internal/appserver` 的包注释在
local-first 重构时就写明了它是「与传输无关的本地命令边界……终端应用、IDE 桥接或
stdio 传输都可以提交协议命令并消费协议事件」。也就是说 Electron 要接入的那个缝，
当初就留好了，只是一直没人用。

真正与 Wails 耦合的只有 `desktop/` 这一层：

| 组成 | 规模 | 迁移动作 |
| --- | --- | --- |
| `desktop/main.go` 的应用外壳 | 30 行 | 重写为 Electron main process |
| Wails runtime API | **4 个**（`EventsEmit`、`OpenMultipleFilesDialog`、`OpenDirectoryDialog` 及其 options 类型） | 换成 Electron 等价 API |
| 绑定到前端的 `App` 方法 | **53 个** | 变成显式的 IPC 契约 |
| Vue 前端 | 2885 行 | **原样搬运**，只换数据访问层 |
| `internal/` Go 核心 | 19 包 | **不动** |
| 发布流水线 | `release.yml` | 换成 electron-builder |

所以这不是一次重写，而是**换掉一层外壳并把隐式绑定变成显式协议**。

## 迁移的动机与代价

这份文档不预设「应该迁」。把两边的实际差异摆出来：

**Electron 能带来的**

- 生态与可招聘性：Node/TS 的桌面生态（自动更新、崩溃上报、原生模块、E2E 测试）远比 Wails 成熟。
- 渲染一致性：Chromium 随包分发，不再依赖系统 WebView。当前 Wails 在 macOS 用 WKWebView、Windows 用 WebView2、Linux 用 WebKitGTK，**三套渲染引擎的行为差异是现实存在的**（`-tags webkit2_41` 这个构建标记就是它的痕迹）。
- 工具链稳定性：本次 v1.19.0 之前遇到的问题——wails CLI 内置的 `golang.org/x/tools` 读不了新 Go 的导出数据，导致本地 `wails dev` 直接不可用——是 Wails「用 Go 工具链解析你的项目」这一设计的固有脆弱点。Electron 不解析 Go 代码，不存在这类耦合。

**代价**

- **安装包体积**：当前三平台产物是 macOS 20.5MB / Windows 10.8MB / Linux 10.1MB。Electron 因为自带 Chromium，同等应用通常在 **80–120MB**，即 4–10 倍。
- **内存占用**：多出一个 Chromium 主进程 + 渲染进程，常驻内存通常比 WebView 方案高 100–200MB。
- **多一个进程要管**：Go 核心变成 sidecar，需要处理启动、健康检查、崩溃重启、优雅退出、以及**孤儿进程**（Electron 被强杀时 Go 进程不能留着）。这是本方案最主要的新增复杂度。
- **两套构建产物**：Go sidecar 需要为每个目标平台交叉编译，再打进 Electron 包。

## 目标架构

```text
┌─────────────────────────────────────────────────────┐
│ Electron main process (TypeScript)                  │
│  ├─ 窗口与生命周期                                    │
│  ├─ 原生对话框（文件/目录选择）                          │
│  ├─ sidecar supervisor：拉起 / 健康检查 / 重启 / 收尾    │
│  └─ IPC 路由：renderer ⇄ sidecar                     │
└───────────────┬─────────────────────┬───────────────┘
                │ contextBridge       │ stdio (JSON Lines)
┌───────────────▼───────────┐ ┌───────▼─────────────────┐
│ Renderer (现有 Vue 应用)    │ │ aiclaw-core (Go sidecar) │
│  仅换掉 wailsjs 数据层      │ │  cmd/aiclaw-core/main.go │
└───────────────────────────┘ │  internal/** 原样复用      │
                              └─────────────────────────┘
```

三层职责边界：

- **renderer** 不碰文件系统、不开进程、不直连 sidecar。`nodeIntegration: false`、
  `contextIsolation: true`，一切经 preload 暴露的白名单通道。
- **main** 只做外壳与路由，**不含业务逻辑**。它不解析 turn、不认识插件，
  只负责把请求转给 sidecar、把事件推给 renderer。
- **sidecar** 是现在的 Go 核心，唯一的业务实现者，继续独占 SQLite。

## 传输层：stdio + JSON Lines

sidecar 用 stdin/stdout 承载 JSON Lines（一行一条消息），stderr 留给日志。

**为什么不是本地 HTTP**：本机 HTTP 端口对同机所有进程可见，等于把一个能读写用户
全部会话、凭据和文件的接口暴露给任何本地程序。要挡住就得再做一套 token 认证和
端口协商，而 stdio 天然只有父进程可达，零额外认证。local-first 重构已经明确
「不复活旧的 web API」，本方案延续这条边界。

**消息形态**（直接复用 `internal/protocol` 的 `Command` / `Event`）：

```jsonc
// → sidecar：一次请求
{"id": "42", "kind": "turn.start", "thread_id": "…", "input": "…"}
// ← sidecar：该请求的流式事件，用 id 关联
{"id": "42", "event": {"kind": "assistant.delta", "delta": "…"}}
{"id": "42", "event": {"kind": "turn.completed", "…": "…"}}
// ← sidecar：请求终态
{"id": "42", "ok": true}
{"id": "42", "ok": false, "error": "…"}
```

要点：

- **`id` 关联请求与事件**，而不是靠顺序。桌面端本来就支持多会话后台并行
  （`BackgroundChats`），事件必须能被正确归属到发起方。
- **一行一条、行内不得含裸换行**，JSON 编码天然满足。
- **背压**：Go 侧写出前检查缓冲，assistant delta 这类高频事件在积压时合并
  （当前 Wails 的 `EventsEmit` 是即发即弃，换成 stdio 后必须显式处理，否则
  一个卡住的 renderer 会把 sidecar 的写阻塞住）。
- **stderr 不参与协议**，只做日志，避免把日志混进消息流。

## IPC 契约：把 53 个绑定方法显式化

Wails 的 `Bind: []interface{}{app}` 是**隐式**的——加一个导出方法就自动多一个
前端可调用入口，没有清单、没有审阅点。这次迁移应当把它变成显式契约，这是迁移
本身带来的一项实际收益。

做法是在 Go 侧新增一个命令分发器，把现有 53 个方法注册成命名命令：

```go
// cmd/aiclaw-core/dispatch.go
type Handler func(ctx context.Context, params json.RawMessage, emit Emitter) (any, error)

// commands is the complete surface the renderer can reach. A method that is
// not listed here cannot be called, however it is exported.
var commands = map[string]Handler{
    "providers.list":   ...,
    "chat.start":       ...,
    "plugins.list":     ...,
    // …
}
```

命名从当前的方法名规范化为 `<域>.<动作>`，同时暴露一个 `meta.commands` 命令返回
清单，供前端在启动时做一次契约校验（前端期望的命令集与 sidecar 实际提供的不一致
时立刻报错，而不是等到用户点按钮才失败）。

**风险**：53 个方法逐个搬运是最容易出低级错误的一步（漏一个、参数顺序错、
可选参数语义变化）。缓解措施见「验证策略」。

## 4 个 Wails runtime API 的替代

| 现状 | Electron 替代 | 备注 |
| --- | --- | --- |
| `runtime.EventsEmit(ctx, name, data)` | sidecar 发事件行 → main → `webContents.send` | 唯一一处调用（`app.go` 的 `emitDesktopEvent`），事件名 `chat:delta` / `chat:progress` / `chat:finished` 保持不变 |
| `runtime.OpenMultipleFilesDialog` | `dialog.showOpenDialog({properties:['openFile','multiSelections']})` | 对话框必须留在 main process——sidecar 是无头进程，弹不出窗 |
| `runtime.OpenDirectoryDialog` | `dialog.showOpenDialog({properties:['openDirectory']})` | 插件安装目录选择 |
| `options.DragAndDrop.EnableFileDrop` + 前端 `OnFileDrop` | renderer 的原生 `drop` 事件 + `webUtils.getPathForFile` | **Electron 32+ 移除了 `File.path`**，必须用 `webUtils`，这是最容易踩的一个坑 |

`BrowserOpenURL` → `shell.openExternal`，但**必须加协议白名单**（只允许 http/https），
否则 renderer 里的任意链接都能触发本机命令——当前 Wails 版本也没做这个校验，
迁移时应当补上。

前端 `OpenAttachment` / `RevealAttachment` / `OpenOutputFile` 这几个方法现在在 Go 侧
用 `exec.Command` 调 `open`/`explorer`/`xdg-open`。迁移后应改为 main process 的
`shell.openPath` / `shell.showItemInFolder`——少一次进程创建，且路径转义由 Electron 负责。

## 附件预览：一处需要重新设计的地方

当前实现把图片读出来编码成 `data:` URI 塞进 JSON 返回给前端
（`attachments.go:223` 和 `:384`）。这在 Wails 里勉强可用，搬到 Electron 后有两个问题：

1. **经过 stdio 的 JSON 会被 base64 撑爆**。附件上限 20MB，base64 后约 27MB，
   一条消息 27MB 会让行缓冲和 JSON 解析都很难看，且阻塞同一条管道上的其他事件。
2. Electron 的默认 CSP 通常需要为 `data:` 显式放行 `img-src`。

建议改为**自定义协议**：main process 注册 `aiclaw://attachment/<uuid>`，
用 `protocol.handle` 流式读取本地文件返回，前端 `<img src="aiclaw://…">`。
这样预览不进 IPC、不进内存、天然支持 range 请求。

这是迁移中**唯一必须改动现有行为**的地方；其余都是等价替换。

## 进程生命周期

sidecar 的失败模式必须显式处理——这是新增的主要复杂度来源。

```text
app.whenReady
  → spawn sidecar（--data-dir 指向 ~/.aiclaw）
  → 等待 handshake（sidecar 输出 {"ready":true,"version":"…"}）
     ├─ 超时 10s：显示可诊断的错误窗，不静默白屏
     └─ 版本不匹配：拒绝启动（避免新前端配旧核心）
  → 创建 BrowserWindow

sidecar 意外退出
  → 指数退避重启（1s→30s 封顶，连续 5 次后停止并提示）
  → 重启期间 renderer 显示「核心已断开」而不是假装正常

app quit / window-all-closed
  → 向 sidecar 发 shutdown 命令，等待最多 5s
  → 超时则 SIGTERM，再 2s 后 SIGKILL
  → 无论哪条路径都必须确保进程不残留
```

**孤儿进程**是这里最容易出的事故：Electron 被 `kill -9` 时不会执行退出钩子，
sidecar 会留下来继续持有 SQLite。缓解手段是 sidecar 自己监视 stdin——
父进程消失时 stdin 会 EOF，此时主动退出。这条是**必须实现**的，不是可选优化。

注意当前 macOS 行为是 `HideWindowOnClose: true`（关窗不退出），Electron 下要用
`window-all-closed` 不 quit + dock 点击 `activate` 重新显示来对齐，否则会变成
关窗即退出，是个可感知的行为回退。

## 目录结构

```text
cmd/aiclaw-core/          # 新增：sidecar 入口，复用 internal/**
  main.go                 #   stdio 读写循环
  dispatch.go             #   53 个命令的注册表
  dialogs.go              #   需要 main process 协助的命令（转发回 Electron）
electron/                 # 新增：Electron 外壳
  main.ts                 #   窗口、生命周期、sidecar supervisor
  preload.ts              #   contextBridge 白名单
  ipc.ts                  #   renderer ⇄ sidecar 路由
  protocol.ts             #   aiclaw:// 附件协议
  package.json            #   electron-builder 配置
renderer/                 # 由 desktop/frontend 迁移而来
  src/App.vue             #   原样
  src/bridge.ts           #   新增：替代 wailsjs/go/main/App
desktop/                  # 迁移完成后删除
internal/                 # 不动
```

`renderer/src/bridge.ts` 是关键的**单点替换**：它导出与当前 `wailsjs/go/main/App`
**同名同签名**的 53 个函数，内部改为走 `window.aiclaw.invoke()`。这样
`App.vue` 的 import 语句几乎不用改，2885 行前端代码得以原样复用。

## 构建与发布

`release.yml` 当前用 wails CLI 构建三平台。迁移后：

```text
test（不变）→ go test ./... + 前端 type-check/test/build
build-core  → 单个 runner 交叉编译 aiclaw-core 全部平台（CGO_ENABLED=0，已实测）
package     → electron-builder，按平台产出 dmg / nsis / AppImage
release     → 与现在相同：tag annotation 作为 release notes + SHA256SUMS
```

`glebarez/sqlite` 是纯 Go 实现（基于 `modernc.org/sqlite`），**不需要 cgo**。
这一点已经实测确认：用一个最小入口跑 `CGO_ENABLED=0`，SQLite 连接与 AutoMigrate
正常，且 `darwin/amd64`、`darwin/arm64`、`windows/amd64`、`linux/amd64` 四个目标
全部交叉编译通过。**所以 sidecar 可以在单一 runner 上一次交叉编译出全部平台**，
不需要三个 runner 各自原生编译，这比当前的 Wails 构建矩阵更简单。

代码签名与公证是**新增的必做项**：Wails 当前只做 ad-hoc 签名
（`codesign --sign -`），用户下载后需要手动放行。Electron 的自动更新（如果启用）
强制要求正式签名，所以迁移是顺带把签名做正规的时机。

## 验证策略

53 个方法逐个搬运是本次迁移最大的错误来源，所以验证必须是机械的、不靠人眼：

1. **契约快照测试**：从当前 Wails 生成的 `wailsjs/go/main/App.d.ts` 提取 53 个方法
   的名称与参数类型，作为基线快照。新的 `bridge.ts` 必须满足同一份快照——
   漏掉或改错一个方法在编译期就会失败。
2. **sidecar 协议测试**（Go）：用假 stdin/stdout 驱动 `cmd/aiclaw-core`，
   逐个命令断言请求→事件→终态的形状。这层不需要 Electron，跑得快。
3. **Electron 主进程测试**：supervisor 的失败路径必须有测试——
   sidecar 启动超时、中途崩溃、退避上限、退出时不留孤儿进程。
   这几条是纯逻辑，可用假 spawn 覆盖。
4. **端到端**（Playwright + Electron）：建 provider → 发一轮对话 → 装插件 →
   配置 → 启用。当前这条链路只有我手动点过，迁移正好补上自动化。
5. **并行迁移期**：`desktop/`（Wails）与 `electron/` 共存一段时间，同一个
   `internal/` 核心。两边都能跑起来时再删 Wails，避免出现「迁到一半两边都不可用」。

## 分步交付

每一步结束时应用都必须是可运行的。

1. **抽出 sidecar**：新增 `cmd/aiclaw-core`，stdio 循环 + 命令注册表 + 协议测试。
   此时 Wails 版本仍然正常工作，`desktop/*.go` 未动。
2. **Electron 骨架**：main + preload + supervisor，窗口里先加载一个只调
   `meta.commands` 的最小页面，验证进程模型与生命周期。
3. **前端桥接**：`renderer/` 引入现有 Vue 代码 + `bridge.ts`，跑通契约快照测试。
   此时两个外壳并存。
4. **原生能力对齐**：文件对话框、拖放（`webUtils`）、`shell.openPath`、
   `aiclaw://` 附件协议、macOS 关窗行为。
5. **打包与签名**：electron-builder 三平台产出，接上现有的 tag → release 流程。
6. **删除 Wails**：`desktop/` 整个移除，`release.yml` 切换，go.mod 去掉 wails 依赖。

## 设计边界与已知风险

- **不迁移 Go 核心**。任何需要改 `internal/` 的迁移方案都说明边界画错了。
- **不引入本地 HTTP**。sidecar 只经 stdio 通信，理由见上。
- **不在 renderer 开 node**。`contextIsolation` 不可关闭；插件系统已经让本应用
  能执行任意本机命令，再把 node 暴露给渲染层是不可接受的叠加风险。
- **体积回退是确定的，不是风险**：安装包会从 ~10–20MB 涨到 ~100MB。如果分发
  体积是硬约束，这个方案就不该做——那种情况下更合理的选择是修 Wails 的工具链
  问题（本次 v1.19.0 已经证明升级 CLI 即可解决），而不是换外壳。
- **Linux 的 WebKitGTK 差异会消失，但 AppImage 的沙箱问题会出现**——
  Electron 在部分发行版需要 `--no-sandbox` 或正确的 chrome-sandbox 权限位，
  这是一类新的分发问题。
- **未验证的前提**：Electron 侧的一切都还没实测——进程 supervisor 的失败路径、
  `webUtils.getPathForFile` 的拖放行为、`aiclaw://` 协议的流式读取、以及
  electron-builder 的三平台产出。本文档对 Go 侧的判断有实测支撑（零 Wails 引用、
  无 cgo、四平台交叉编译通过），对 Electron 侧的判断只是基于其公开行为的推断。
