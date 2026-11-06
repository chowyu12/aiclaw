# Computer Use 基础能力对标与验收台账

目标建立日期：2026-11-06。用户已确认包含目标窗口标识、控制状态、暂停/恢复和停止入口等可视化体验。起始代码基线为 `3ad42fd4`（v3.7.44）；下表记录目标开始时的状态，后续需以实际验收结果更新，不能仅因接口已添加就改为完成。

## 对标范围与完成标准

本目标对齐可验证的桌面控制行为，不以复用 Codex helper、同名 IPC、工具数量或静态符号数量作为完成标准。已有九类动作、生命周期、代码编排和剪贴板保护应复用，新增能力保持现有审批、目标验证、取消和不重放输入的约束。

每个新增基础能力均需同时具备产品入口、明确状态/错误、针对性测试及受控真实场景回执。macOS 原生能力需用临时测试 App 验证；Windows/Linux 能力需在相应原生 CI 中验证。完整包启动与源码单元测试分开记录。不得向已有业务聊天发送测试消息，或编辑用户文档来代替测试 fixture。

音频录制、系统 Calendar/Messages 接口、长期观察历史/Skysight、锁屏 Guardian 和特权安装属于独立子系统。已发布产物存在相关接口的静态证据，但不能据此自动把这些子系统计入基础桌面动作范围，或宣称其行为已恢复。若后续纳入，需要独立验收项。

## 当前 Codex 产物与证据身份

本轮未运行 Codex helper、未发送私有 IPC、未启动 Hopper；REA CLI 只做静态检查。REA MCP 在当前会话不可见；只读 doctor 显示 Codex 注册已对齐，没有修改 MCP 配置或安装分析引擎。

| 对象 | 本轮确认的身份与覆盖 |
| --- | --- |
| 原生目标 | `/Users/zhouyy/.codex/computer-use/Codex Computer Use.app/Contents/MacOS/SkyComputerUseService` |
| 原生版本/架构 | Info.plist `26.1027.1001730`；arm64；UUID `2DE42A7D-4595-32F4-B86A-17D9E7305FC1` |
| 原生 SHA-256 | `b64e219f30c655407e485d5fab47d354ea6398842fc49a6bad917810df54c862` |
| 原生 REA Evidence | `ev_5341c002db8c0c39cf9c0a70ab32b338293343c5a09c66fd994c569a5313f0c5`；`inspect_macho`；provider `native-macos` |
| JavaScript 目标 | `/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/@oai/cua`；包声明 `0.2.6` |
| JavaScript 整目录 SHA-256 | `e3183cb68cc879c06ce6d7cf9098689139931bdf52e9aa8c63939c5e72137c93`，不是单文件摘要 |
| JavaScript REA Evidence | `ev_f3c02711e4116a2ca77af93ed066eef6841385016acdc52b19aabcd682d033a5`；选中 `package.json`、mac `client.js` 和 `native-pipe.js`，553 个 inventoried 文件未参与分析 |

完整证据与派生文本：

- [当前 Mach-O Evidence](/Users/zhouyy/.codex/visualizations/2026/11/06/01a1adfb-4653-7b53-b81f-62dbebeed8c0/computer-use-parity/current-macho.json)
- [当前 JavaScript Evidence](/Users/zhouyy/.codex/visualizations/2026/11/06/01a1adfb-4653-7b53-b81f-62dbebeed8c0/computer-use-parity/current-client.json)
- [从当前 Evidence 导出并 demangle 的选定符号](/Users/zhouyy/.codex/visualizations/2026/11/06/01a1adfb-4653-7b53-b81f-62dbebeed8c0/computer-use-parity/current-selected-exports-demangled.txt)（派生文本，不是独立 REA Evidence）

旧报告 [Codex 能力盘点](codex-computer-use-evidence.md:9) 的 native 版本为 `26.1022.1001641`、SHA-256 为 `e798d6…`，其地址不能用于当前二进制。本轮逐文件比较确认旧选中的 mac `client.js`、`list_apps.js`、`get_app_state.js` 和 `set_value.js` 内容摘要仍相同，但 `package.json` 与目录身份已变化。旧静态证据只能按各自目标身份使用。旧报告中“当前开发 ad-hoc”及“Windows/Linux 尚未验收”等时间点陈述不能覆盖 v3.7.44 后的实现状态。

## 当前可确认的 Codex 基础接口线索

| 观察 | 本轮证据 | 允许的结论与限制 |
| --- | --- | --- |
| 应用、观察和九类动作 | 选中 JS 保留 `listApps/startApp/getAppState/getAppPolicy` 和 `click/drag/paste/performSecondaryAction/pressKey/scroll/setValue/selectText/typeText`；默认 `CodexComputerUseIPC-5` | 这是已发布客户端的静态编码面；不证明任意 App 的运行成功率或私有输入机制 |
| 持续窗口捕获 | 当前 native `AppStartCaptureRequest.handle` `0x10016707c`；`AppNextCaptureUpdateRequest.handle` `0x10016cc9c` | 证明 handler 存在；帧节流、取消、过期、错误和 wire 默认值未恢复 |
| 捕获过渡/表现 | `AppStartCaptureRequest.init(app:requestID:permissionRequestID:animationTarget:version:)`；`Appshot.AppshotCaptureTransition` 含 `setScreenshot`、`overlayWindows`、`waitForMagicMove`；animation target 含目标 frame、颜色、corner radius、presentation style 和 display | 证明存在捕获过渡的数据模型；不能据此认定用户截图左上角标识由它绘制，也不能认定是系统共享标识 |
| 状态菜单 | 当前 native `CodexStatusItemMenuStateRequest.handle` `0x10016cf30` | 另有 `ComputerUse.activeApplications` 字段，证明菜单状态可携带活跃应用列表；菜单布局、按钮行为和显示条件未知 |
| 事件和暂停/恢复 | 当前导出包含 `EventStreamService.start/status/stop` 及 `SkysightService.start/status/pause/resume/stop` | 存在这些子系统的静态线索；Skysight 暂停不等于已证明整个桌面执行会话的暂停语义 |
| 发送者身份 | 当前导出存在 `ComputerUseIPCSenderContextResolver` 和多个 `handle(senderContext:)` | 证明存在发送者上下文抽象；其签名/证书要求、允许进程及授权策略均未恢复 |

没有在本轮选中的 mac JS 中建立 `AppStartCapture/NextCaptureUpdate/EventStream/StatusItem` 的客户端调用链。选定符号名搜索未找到 `SharingIndicator`、`CaptureBadge` 或 `stopCapture` 精确名称，不代表这些能力不存在；被 strip、改名、动态注册或未选中代码均仍是未知。

## 基础能力验收清单

状态说明：**已有**表示起始代码已有实现和前序验收，仍需在本轮改动后回归；**部分**表示只覆盖明确子集；**开放**表示目标尚未实现/尚未验收。外观可以采用 AIClaw 自有设计，系统共享提示不得伪造成系统授予的控制权限。

| ID / 优先级 | 基础能力 | AIClaw 起始源码与状态 | 可执行的完成验收 |
| --- | --- | --- | --- |
| B01 / P0 | 可见控制会话：目标窗口标识、控制中状态、窗口跟随 | `tools/computer-use/Sources/Observation.swift:174` 是单次截图；无独立控制标识。**开放** | 对临时 App 开始会话，仅目标窗口出现提示；移动、缩放、切换目标时跟随正确；目标关闭/会话结束/宿主退出后消失；提示不劫持键盘焦点、不落入观察结果、不成为操作目标；界面明确区分观察与控制 |
| B02 / P0 | 暂停、恢复、停止及独立停止入口 | `computer.ts` 和 `Service.swift` 有轮次/断连取消；没有完整产品控制条。**部分** | 暂停后新动作和已排队但未执行动作均不得派发；观察行为遵循明确暂停策略；恢复必须重新观察；停止应释放按住输入、关闭会话与标识，取消等待和队列；快捷键/状态入口在 AIClaw 非前台时也可用；多会话作用范围明确 |
| B03 / P0 | 权限与运行状态、诊断和重新检测 | `index.ts:772` 启动检查；`computer-runtime.ts:97` setup；`Models.swift:50` 缺权限引导。**部分** | 设置入口展示本平台 helper 安装/连接状态、权限、能力限制；重新检测返回真实状态；只在用户点击时打开设置/请求权限；覆盖缺失、拒绝、撤销、helper 损坏/无法连接及已授权不弹窗；不把“应用存在”当作授权成功 |
| B04 / P0 | 原生客户端鉴权 | `Service.swift:407` 只验同 UID；宿主 `computer-runtime.ts:94` 只验证服务端可执行摘要。**开放** | 正式签名 AIClaw 可连接；同用户其他可执行程序不可直接调用原生输入；拒绝过期/伪造身份及连接代际混淆；开发通道显式且不能降级正式通道；断连立即撤销授权；签名与更新后端到端握手通过。具体实现为 AIClaw 自有方案，不宣称等同未恢复的 Codex sender policy |
| B05 / P1 | 应用发现、窗口选择、启动/退出/激活/隐藏 | `Service.swift:142`、`Observation.swift`、`computer-tools.json`。**已有** | 多窗口同名/无窗口/进程替换均能消歧；仅操作选定 App；正常退出遇保存对话框保留未确认；发现结果无动作授权；拒绝 AIClaw 自操作；无任意 shell/路径启动回退 |
| B06 / P1 | 九类基础输入、Unicode/富文本/剪贴板与连续执行 | `Actions.swift`、`Markdown.swift`、`computer.ts`、`computer_test.go:144`。**已有** | 临时 fixture 上验证每类动作和业务回读；中文/emoji/组合字符/UTF-16选择范围、Markdown/HTML消费、原格式恢复及并发复制保护；exec 中审批拒绝/取消/未知结果截断后续；不自动重放输入 |
| B07 / P1 | 后台操作与能力报告 | `Actions.swift:103` 只允许受限 AX 语义操作；`computer-policy.ts:48` 明列限制。**部分** | 每个平台/动作报告真实能力；支持的语义路径用双 App 证明前台、鼠标、剪贴板保持；新增后台输入必须验证目标 PID/窗口与实际效果；不支持的路径输入前拒绝，不能以转前台伪装后台成功；无法证明的私有机制保持未知 |
| B08 / P1 | 原生持续捕获、AX 变化与增量 | `computer-watch.ts:89` 基于单次截图轮询；两样本 AX splice/PNG去重。**部分** | SCStream/原生 AX 事件产生有序、可取消的窗口更新；静止不重复全帧；变化帧和 AX 更新可重建；背压/消费者落后能重置；采集不得污染动作索引；窗口关闭、权限撤销、进程替换、暂停/停止/空闲均可清理；CPU/帧率有界并记录指标 |
| B09 / P1 | 常用应用策略与可验证工作流 | `computer-policy.ts:10` 仅 Finder/Safari/TextEdit，其他通用回退。**部分** | 为明确支持的常用 App 补正确策略，按精确应用标识和平台匹配；从新观察解析控件、不固化索引；用只读场景或独立 fixture 验证搜索、定位、编辑/保存等适用工作流；未知 App 能正确通用回退；不代用户发送真实业务消息 |
| B10 / P1 | Windows/Linux 基础控制及产品入口 | `windows/Native.cs`、`windows/Clipboard.cs`、`linux/service.py`、`linux/clipboard.py` 与 `computer-platforms.yml`。**已有 Windows/X11；Wayland 开放** | 本轮新增状态/暂停/停止/生命周期在两平台完整包验收；Win32/UIA 与 X11/AT-SPI 的输入、文本、富文本、剪贴板、取消及自操作拒绝回归。Wayland 在 portal-backed 会话实现且实际验收前必须继续明确不支持，不能把 X11 测试算作 Wayland 支持 |
| B11 / P1 | 失败恢复、会话隔离与资源清理 | `computer.ts`、`computer-watch.ts`、`Service.swift` 已有观测代际、全局队列和断连取消。**已有，新增能力需扩展** | 跨聊天/跨轮次不能使用旧观测、订阅或控制权限；用户接管、窗口切换、超时、断连、helper重启后停止当前动作并要求重观测；不自动重放；退出后无悬空流、提示窗口、按住键鼠或无限后台进程 |
| B12 / P1 | 安装、稳定签名与升级授权 | v3.7.44 `mac-signing.mjs`、`build-computer-runtime.mjs`、`computer-install.ts` 已固定自签身份和输入指纹复用。**已有** | 本轮原生变化仍由原证书签名；主版本变化复用原包；helper变化后 DR相同且真实 AX/录屏权限保持；正式包/开发包安装隔离策略明确；签名损坏不覆盖可用版本。Apple公证/Developer ID 分发属于独立交付条件，不由自签测试证明 |

## 交付记录

- 目标起始阶段：本台账和当前产物静态证据已建立；尚未因本台账将任何开放项标为完成。
- 对标行为尚未获得对应运行时证据时，只能写“自有实现通过验收”，不能写“Codex 内部机制完全相同”。
- 实现阶段应逐项登记提交、测试命令、运行平台和回执路径；提交代码、CI通过、Release产物及用户安装效果是独立结论。


## 实施进度（目标保持 active）

以下为当前工作分支验收，尚未发布：

- 已实现全局控制状态、设置诊断、暂停/停止立即断连、独立浮条、快捷停止，以及恢复重新观察。宿主和纯几何/IPC/策略测试已纳入持续检查。
- 原生双向鉴权：真实固定证书正确标识宿主接受；同 UID Node、自报 sender、正确标识 ad-hoc 与同证书错误标识拒绝。服务端也使用内核 peer 身份验证；同 UID 假 listener 在收到应用帧之前被拒绝，真实 Rosetta x64 验证器与 arm64 server 通过。完整签名 Electron 包另作集成验收。
- 原生捕获通过 SCStream 视觉变化、AX 纯语义变化且 PNG 不变、双静止去重、停止唤醒、窗口替换不重定向、四流限额、跨连接 ID/重复等待拒绝、slot 复用和 JSONL 并发；等待更新期间普通请求 1ms 返回。
- Mac 实际浮条已验证窗口跟随、两显示器定位、不抢焦点、目标截图不含浮条、暂停/恢复/停止、真实激活限制。最终采集丢失集成也通过：真实 native stream 停止使等待报错，全局自动暂停，关闭所有输入/观察连接，按住输入取消，另一聊天的排队退出动作不派发；旧观察拒绝，显式恢复后要求新观察。
- macOS 后台 AppKit 完整输入、Electron 受限纯文本/空 textarea 适配、旧字段值/选择范围观察拒绝，以及九类前台操作均已有真实 fixture 回执。非空 rich textarea 与 Cmd/Ctrl/Alt 等范围明确拒绝；不声称支持任意后台键盘。详见 [后台键盘验收](computer-use-background-keyboard.md)。
- 旧语义后台四类动作与 native watch 回归通过。首次捕获实测系统新增 `AXMenuButton Window sharing`，测试严格核对该有界生命周期变化，随后要求 900ms 静止无更新；继续验证 AX delta 重建、PNG 变化、观察权限隔离与停止等待取消。
- Linux arm64 / X11 隔离容器真实暂停/停止、跨聊天排队拒绝、旧观察失效、重新观察并设值、订阅取消、query 几何及既有富文本/剪贴板回归通过。实际浮条在单屏原点为零的 1.5/2 倍缩放均验收通过。非 1 倍多屏/非零原点及混合缩放尚无真实验证，浮条明确隐藏；Wayland 不支持。
- 原生测试证据归档于 `/Users/zhouyy/.codex/visualizations/2026/11/06/01a1adfb-4653-7b53-b81f-62dbebeed8c0/computer-use-parity/`；Linux 回执位于 `build/computer-evidence/linux-control-local/`。
- 完整 `make check` 已通过：396 项宿主/渲染测试（395 通过、1 平台跳过）、19 项更新器、40 项内核冒烟、337 项界面检查、完整类型/Go race/原生鉴权迁移测试。最后原生资源清理与非 macOS 主窗口退出修复单独回归。
- 正式固定证书 Electron 包已验证 production 身份、双向认证、旧空闲签名服务正常迁移、伪造 listener 零应用帧、权限 true 且不弹授权；使用临时只读验收入口，不代表默认 UI 启动验收。当前最终清理修复对应的包正在重验。
- 仍待：最后生命周期针对性回归、最终正式包重验、Windows/Linux x64 当前提交 CI，以及最终逐项清单审计。未完成这些检查前目标保持 active。
