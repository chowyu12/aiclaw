# Codex Computer Use App 能力与证据盘点

本报告记录 2026-11-05 对本机已发布产物的静态检查，并区分原始观察、推断和 AIClaw 的实现选择。**目前只恢复了可见接口及部分客户端调用链，不能据此声称已还原或实现 Codex Computer Use 的全部功能。**

## 目标与证据身份

| 对象 | 身份 |
| --- | --- |
| 原生目标 | `~/.codex/computer-use/Codex Computer Use.app/Contents/MacOS/SkyComputerUseService` |
| Info.plist 观察 | 版本 `26.1022.1001641`，bundle ID `com.openai.sky.CUAService`；plist 由普通系统工具读取 |
| 原生 SHA-256 | `e798d6a292fb5d93536ae9f6954e9809f0833bcf38d1ff50624f1e2268723dea` |
| 架构与 UUID | arm64，`AD22C18C-5BF8-30E8-A197-CD63389E6DD9` |
| REA 原生 Evidence | `ev_c1289ed3aa4747118abea3b131feaefea873a0f3b72f3312ca033074590540c6`，操作 `inspect_macho`，provider `native-macos` |
| JavaScript 目标 | `/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/@oai/cua`，包声明版本 `0.2.6` |
| JavaScript artifact SHA-256 | `81b1b2dbb790389b1db8cf9ceb390fde027b7122e39d105f67043aa223ee8071`（REA 目录产物身份，非单文件 hash） |
| REA JavaScript Evidence | `ev_9cc5d4580a91dd0813fb6cd91f3e98489cc3b816096562b16cb82d59078949f5`，操作 `analyze_javascript_application` |

原始输出保存在 [本轮证据目录](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search)。[Mach-O Evidence](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/macho.json) 和 [JavaScript Evidence](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/client.json) 是完整 REA JSON。`exports-demangled.txt` 是从 Mach-O Evidence 导出符号经过 `swift-demangle` 的辅助文本，不是另一份 REA Evidence。

JavaScript 分析明确只选取 `package.json`、`dist/project/cua/sky_js/src/targets/mac/{client,list_apps,get_app_state,set_value}.js`，有 **539 个 inventory 文件未参与源码分析**。静态结果不证明运行时可达性、鉴权、权限、成功率或真实输入效果。

第一次 Hopper 初始分析在 120 秒内未就绪，返回 `cleanup_incomplete/provider_timeout`。临时分析文档已通过 UI 关闭并停止反汇编；失败诊断保存在 [procedures.json](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/procedures.json)，不用于支持行为结论。

随后提高等待预算，REA/Hopper 已成功反编译 `performPaste(...)` 的入口 `0x100081504`，得到 Evidence `ev_944f0f0752a93085b533d18b96000ed37e6151b62d0ecbfb2bd2638bd0818457`，原始结果见 [paste-pseudocode.json](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/paste-pseudocode.json)。该结果仅是 **Swift async 入口 thunk**：保存参数、分配 task context，再执行 `swift_task_switch(sub_100081574, ...)`。入口 thunk 本身没有展示剪贴板或事件处理，不能据此声称已经恢复粘贴算法、剪贴板恢复策略或输入投递机制。

进一步通过独立的持久 REA MCP 会话追踪 `0x100081574`，避免每个地址重新建立分析库。Hopper 实际推进至 pass 11/11，但仍在 900 秒 startup 预算耗尽前没有完成 bridge readiness，返回 `provider_timeout/cleanup_incomplete`；已排队的 callees 查询被同一保留清理状态阻止。失败原始结果分别保存在 [paste-continuation-081574-final.json](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/paste-continuation-081574-final.json) 与 [paste-continuation-081574-callees.json](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/paste-continuation-081574-callees.json)。它们是工具失败诊断，不是函数体证据。本次没有恢复 continuation 的实际实现，因此 AIClaw 的富文本转换、剪贴板保留/恢复及 Command-V 投递均为项目自己的实现选择，不能称为与 Codex 原生算法一致。 `close_binary` 也未收到认证关闭回执；随后已通过 Hopper UI 手动关闭本轮临时分析文档并确认回到空白 Untitled 窗口。尝试保存分析库时，已安装的 Hopper Demo 明确提示不允许保存，因此未保留可供复用的 `.hop` 数据库，也未绕过该限制。

## 已确认的桌面调用模型

选中 JavaScript 的 `MacComputerUseClient` 保留 `Map` 缓存 transport，默认协议标识为 `CodexComputerUseIPC-5`。`listApps()`、`startApp()`、`getAppState()` 分别构造 `ComputerUseIPCListAppsRequest`、`ComputerUseIPCAppStartRequest`、`ComputerUseIPCAppGetSkyshotRequest`。输入操作统一通过 `ComputerUseIPCAppPerformActionRequest` 发送 `{app, action}`。这些是已发布 JavaScript 的静态调用观察；原生目标也导出了对应 handler，构成客户端和原生侧的接口对应关系，但未完成一次受控的原生端到端协议探测。

`getAppState` 传递 `disableDiff`。原生导出有 `updateSkyshot(treeCache:disableAXDiffing:skipScreenshot:)`（`0x10006ef20`）、`lastAXTree`、`RefetchableSkyshotAXTree.refetchTree()`（`0x10023b154`）和 `RefetchableUIElement.refetchIfNeeded(...)`（`0x10023c16c`）。这支持“存在 AX 树缓存、差异和重新获取接口”的判断；何时刷新、如何处理用户干预、如何判定元素失效仍需伪代码或运行时验证。

应用列表的 JS 返回映射包含 bundle ID/displayName、isRunning、lastUsedDate/useCount。原生 `DiscoveredApp` 另有 appPath、isFrontmost。目标导入了 `MDQueryCreate/Execute/SetSearchScope` 等 Spotlight 接口，但尚未建立这些导入到 ListApps handler 的引用链；**不能仅凭导入把应用搜索算法描述为已确认的 Spotlight 实现**。

## 九种原生 Action

REA 导出 `ComputerUseClient.ComputerUseIPCAction` 的 nominal descriptor：`0x10128e9dc`。普通 Python 对同一 SHA-256 产物解析 Swift reflection 元数据：descriptor 的 `+16` 相对指针指向 `0x1012e6758`（文件偏移 `19818328`），记录数为 9。下表的 case 名来自该 descriptor，独立于 JS 中拼接的对象键；这部分辅助解析保存在 [reflection-action-fields.json](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/reflection-action-fields.json)，**不是 REA 原生解析能力的输出**。

| Action | 已发布 JS 编码要点 | 原生相关符号地址 | 调查开始时 AIClaw 基线（非最终状态） |
| --- | --- | --- | --- |
| `click` | elementID 或 coordinate，clickCount、mouseButton、key、duration | `click(elementID:...)` `0x100074ec8`；坐标 `0x10007faf0` | 语义/坐标、次数、按键类型；修饰键/按住时长尚未对齐 |
| `performSecondaryAction` | action、elementID | `0x100076fd0` | 已有固定工具 |
| `setValue` | elementID、value | `0x100078ef0` | 已有固定工具 |
| `selectText` | elementID、text、prefix/suffix、selection | `0x10007c430` | 调查开始时未暴露，OCU 真实 manifest 支持，可接入 |
| `scroll` | elementID 或 coordinate、direction、pages | `scroll(deltaX:deltaY:)` `0x100081a28` | 已有元素/方向/页数；坐标路径未对齐 |
| `drag` | from/to 坐标 | 与坐标点击导出共享相关参数，独立 handler 体未知 | 已有坐标工具；原生投递机制未证明相同 |
| `pressKey` | key、duration | `performKeyboardAction(...)` `0x10007a564` | 已有组合键；按住时长未对齐 |
| `type` | text | 同上 | 已有工具；Unicode 效果需 live fixture 验证 |
| `paste` | text、format | `performPaste(...)` `0x100081504` | 尚未接入；不能将 typeText 视为等价的富文本粘贴 |

同一产物 reflection 辅助解析还得到：`PasteFormat` 为 `text/md/html`；`TextSelection` 为 `text/cursorBefore/cursorAfter`；`AppModifyRequest.Modification` 为 `activate/deactivate`。详情见 [reflection-ipc-fields.json](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/reflection-ipc-fields.json)。这些是类型形状线索，枚举 case 名不等于已验证的 wire raw value；也不证明底层如何操作剪贴板、如何恢复剪贴板或如何发送输入。

调查开始时，`computer_query` 来自 AIClaw 所用 Open Computer Use 的扩展能力；最终实现已改为自有 Swift 查询。此次检查的 Codex macOS `client.js` 未暴露相同方法，选中原生导出也未恢复出对应 query handler；因覆盖不完整，结论仅为“本次未建立对应关系”。不能将 AIClaw/OCU 的 max_nodes、2 秒 deadline、BFS 或匹配策略冒充 Codex 的实现。

## 可见 IPC 请求完整清单

在当前 REA 导出集合中，恢复了 **40 类 Request 类型**。其中 39 类有可见 `handle(senderContext:)` 符号；`MessagesCommitSendRequest` 还存在接收文件句柄的重载。表内地址均为 **handler 地址**，不是调用现场或执行记录。字段名通过对同一 hash 产物的 Swift reflection 辅助解析获得，不能仅凭它保证编码键或 wire schema；未恢复默认值、可空性、范围或权限规则。

| Request（省略 `ComputerUseIPC` 前缀） | handler 地址 | 可见存储字段名（wire schema 未验证） |
| --- | --- | --- |
| `AppGetSkyshotRequest` | `0x1001661ec` | `app,disableDiff` |
| `AppModifyRequest` | `0x10015c464` | `app,modification` |
| `AppNextCaptureUpdateRequest` | `0x10016ce7c` | `requestID` |
| `AppPerformActionRequest` | `0x10015d9e4` | `app,action` |
| `AppStartCaptureRequest` | `0x10016725c` | `app,requestID,permissionRequestID,animationTarget,version` |
| `AppStartRequest` | `0x10015b818` | `app` |
| `AppStopRequest` | `0x10015c320` | `app` |
| `CalendarCreateEventRequest` | `0x100172d44` | `calendarID,title,startDate,endDate,isAllDay,location,notes,url,recurrenceRFC2445,attendees` |
| `CalendarDeleteEventRequest` | `0x1001730dc` | `id,span` |
| `CalendarGetEventRequest` | `0x100172b44` | `id` |
| `CalendarListCalendarsRequest` | `0x10017290c` | `` |
| `CalendarListEventsRequest` | `0x1001729c0` | `startDate,endDate,calendarIDs,limit,cursor` |
| `CalendarUpdateEventRequest` | `0x100172f10` | `id,calendarID,title,startDate,endDate,isAllDay,location,notes,url,recurrenceRFC2445,attendees,span` |
| `CodexStatusItemMenuStateRequest` | `0x10016d110` | `` |
| `CodexTurnEndedRequest` | `0x10016d0e4` | `threadID,turnID` |
| `EventStreamStartRequest` | `0x10016e63c` | `` |
| `EventStreamStatusRequest` | `0x10016e9a0` | `` |
| `EventStreamStopRequest` | `0x10016ee78` | `reason` |
| `FrontmostWindowRequest` | `0x10015b740` | `` |
| `ListAppsRequest` | `0x10015b060` | `` |
| `MessagesCommitSendRequest` | `0x1001738e0` | `planID,text` |
| `MessagesCommitSendRequest`（附件文件句柄重载） | `0x100173938` | `planID,text` |
| `MessagesCountActivityRequest` | `0x100173bb0` | `fromDate,toDate,interval,chatType,chatGUIDs,breakdown,rankBy,chatLimit,cursor` |
| `MessagesFindChatsRequest` | `0x10017318c` | `participants,exactParticipants,name,fromDate,toDate,unreadOnly,limit` |
| `MessagesPrepareSendRequest` | `0x1001736b8` | `chatGUID,recipients,text,attachments` |
| `MessagesReadImageRequest` | `0x100173d84` | `id` |
| `MessagesReadMessagesRequest` | `0x1001734d4` | `chatGUID,fromDate,toDate,unreadOnly,limit,cursor` |
| `MessagesSearchChatsRequest` | `0x10017331c` | `query,limit` |
| `MessagesSearchMessagesRequest` | `0x1001735f0` | `text,chatGUIDs,participants,exactParticipants,fromDate,toDate,limit,cursor` |
| `SkysightClearHistoryRequest` | `0x100171920` | `scope,interval` |
| `SkysightGetSettingsRequest` | `0x100171524` | `` |
| `SkysightPauseRequest` | `0x10017023c` | `duration` |
| `SkysightResumeRequest` | `0x10017101c` | `` |
| `SkysightStartRequest` | `0x10016f37c` | `` |
| `SkysightStatusRequest` | `0x10016f884` | `` |
| `SkysightStopRequest` | `0x10016fd5c` | `` |
| `SkysightUpdateObservationPolicyRequest` | `0x1001717e0` | `target,observe` |
| `SkysightUpdateSettingsRequest` | `0x1001716d0` | `settings` |
| `StartAudioRecordingRequest` | `0x10015b1c8` | `maxDurationMilliseconds` |
| `StopAudioRecordingRequest` | `0x10015b418` | `` |

另外 `AppPolicyRequest` 的 nominal type descriptor 为 `0x10128e8fc`，字段为 `app`。JS 明确发送此请求，原生也有 `AppPolicyResult` 和 `allowPersistentApproval/decision/target` 字段。当前导出集合没有对应 `handle`，不能推断该请求不存在或一定允许操作。

## 能力分组与调查开始时的 AIClaw 基线

| 能力组 | 目标证据 | 调查开始时 AIClaw 路径/差距（非最终状态） |
| --- | --- | --- |
| 应用发现与生命周期 | ListApps、AppStart/Stop/Modify、FrontmostWindow、AppPolicy | 可列出 OCU 应用，观察明确目标；没有专门 start/stop/activate/deactivate/frontmost/policy 工具。AIClaw 的宿主授权与自操作限制是独立设计，不能冒充 Codex 原生授权 |
| 一次性观察与输入 | GetSkyshot、PerformAction、九种 Action | 已有截图、AX 树、基础输入工具和 query；selectText 可在 pinned runtime 能力范围内补齐；paste/部分参数缺失 |
| 持续捕获 | AppStartCapture、AppNextCaptureUpdate | 未接入；单次截图不等于连续捕获/增量订阅 |
| 事件流 | EventStream Start/Status/Stop | 未接入；聊天工具日志不等于原生输入/观察事件流 |
| 历史观察与策略（Skysight） | Start/Stop/Pause/Resume/Status、settings、observation policy、clear history | 未实现相应桌面历史服务。reflection 显示观察规则有 app/url scope、bundleID/urlDomain；实际存储、采样、清理语义未恢复 |
| 音频 | StartAudioRecording/StopAudioRecording | 未通过 computer-use 接入；AIClaw 其他语音输入能力不能直接算作系统音频录制等价实现 |
| 日历 | Calendar 列表、区间分页事件、单事件 CRUD | 未接入；这些是系统日历接口，不是模拟 Calendar UI 的普通点击 |
| 消息 | 聊天发现/搜索、消息读取/搜索、图片读取、活动统计、PrepareSend/CommitSend | 未接入；Prepare/Commit 两阶段字段可见，具体审核绑定/附件约束/发送授权未知。没有执行真实消息发送 |
| 轮次与菜单 | CodexTurnEnded、CodexStatusItemMenuState | AIClaw 有自己的轮次结束/会话清理；没有同名状态菜单服务 |
| 权限与安装辅助 | 独立 App 包、Info.plist 权限说明、bundle 内 Installer/Guardian | AIClaw 使用独立 OCU helper 处理 Accessibility/Screen Recording；不能认为复用其签名就具备 Codex Guardian/安装器/锁屏能力 |

以上两张覆盖表保留调查开始时的代码基线，包括先前已有、未提交的查询改动；不能将其误读为最终架构。表中的“未接入”指当时核对的 AIClaw Computer Use 工具边界，不排除其他插件或手动 UI 操作间接实现相同业务结果。不能将间接操作等同于已经实现原生 API。

## 最终实现：自有原生桌面服务

用户确认范围为：**完整实现桌面控制，其余提供证据与差距清单**；随后明确放弃 Open Computer Use 执行层。最终代码使用自有 Swift **AIClaw Computer Use.app + Unix socket IPC**，不依赖 OCU、Codex helper、用户 MCP 配置或 Electron 内加载的驱动。此前基于 OCU 的短暂接入工作不是最终交付方案，也不构成 Codex 行为证据。

当前开发分支支持 macOS 14.4+、Windows 与 Linux X11，分别使用自有 Swift、Win32/UIA、X11/AT-SPI 执行层；三平台注册 18 个原生工具和 4 个宿主工具，不加载第三方桌面驱动。Linux Wayland 尚未实现并明确拒绝。详细执行与打包设计见 [AIClaw 原生桌面控制](computer-use.md)。

| 已实现能力 | 最终代码行为与证据边界 |
| --- | --- |
| 18 个原生工具与独立 IPC | 自有协议 `AIClawComputerUseIPC-1`，握手校验工具、版本及启动时可执行 SHA-256（macOS）或绑定源码 SHA-256（Windows/Linux）。请求命名参考恢复的 Codex 接口形状，**不宣称 wire 兼容或逐行还原** |
| 应用与窗口 | NSWorkspace 启动、正常退出、激活/隐藏；应用发现合并有时限的 Spotlight 索引及运行态，返回窗口候选；前台窗口读取。Spotlight 算法是项目选择，未证明等同 Codex |
| 观察与查询 | ScreenCaptureKit 捕获所选窗口，PNG 长边最多 1280；可选 AX 树、显式窗口 ID、按文字/角色定向查询；query 仍是 AIClaw 扩展 |
| 九种动作 | 点击、辅助操作、设值、文本选择、滚动、拖动、按键、输入、粘贴；补齐点击修饰键/按住时长、按键时长与坐标滚动 |
| 文本与剪贴板 | 原生 UTF-16 选择范围校验、prefix/suffix 消歧；text/Markdown/HTML 粘贴、原剪贴板保存与并发复制保留；行为需 live 验收，不能仅凭工具名断言与 Codex 等价 |
| 输入投递 | 公开原生 API。严格后台模式支持已验收的语义动作，拒绝不支持的输入且不回退前台；坐标与键盘操作需要前台并核对目标，可能切换焦点和指针。未复刻尚未恢复的 Codex 私有投递 |
| 宿主扩展 | 代码模式顺序编排、轮次内轮询观察、可重建 AX 行增量和 PNG 去重，以及 Finder/Safari/TextEdit 指引；均有独立测试，不能据此宣称与 Codex 私有实现等价 |
| 会话与授权 | Go 审批、宿主最新 observation_id、每连接 AX 句柄和目标重新校验；原生全局队列覆盖整个异步操作，断连取消并丢弃待执行请求；同 UID peer、socket 0600、固定 `~/.aiclaw/computer-use/` 安装位置，可执行 hash 分端点 |
| 权限与产物 | 独立应用拥有 AX/录屏授权；缺权限显示说明并提供系统设置入口，不自动 grant；universal 原生构建先在 macOS 产出，再供 Linux 打包 job 使用 |

`AppModify.deactivate` 在 AIClaw 中明确选择 hide；`AppStop` 选择正常请求退出，遇保存对话框保留“未确认”状态，不强制 kill。这是项目实现决策；Codex 对应函数体尚未恢复，不能宣称语义已完全相同。动作后的截图和目标业务效果仍须单独验证。

## 仍未对齐或本轮不实现

| 范围 | 当前差距 |
| --- | --- |
| 私有投递、AX 差异与应用说明 | Codex 的 `disableDiff`、AX cache/refetch、`appSpecificInstructions` 有静态线索；实际策略和私有输入路径未知。AIClaw 使用完整/定向 AX 观察、公开 API 严格后台语义动作和项目维护的应用指引，不提供私有实现等价声明 |
| 持续捕获、事件流 | 已实现宿主周期采样、窗口订阅、AX 行增量与图像去重；尚未实现 Codex 原生 AppStartCapture/AppNextCaptureUpdate、EventStream 或逐帧/原生事件流，不能把轮询等同原生流 |
| Skysight 与历史策略 | 观察历史、暂停/恢复、app/url policy、采样与清理服务未实现；实际存储和采样语义也未恢复 |
| 音频 | Computer Use 的系统音频录制未接入；AIClaw 其他语音输入不计作等价实现 |
| Calendar、Messages | 系统日历 CRUD、聊天/消息搜索读取、图片、统计、PrepareSend/CommitSend 原生接口未重建；未执行真实系统日历变更或消息发送 |
| 锁屏、安装器与 Codex 菜单 | 未实现 Guardian、特权安装/锁屏控制或 Codex 状态菜单；独立应用和系统权限不自动赋予这些能力 |
| 未证实的内部能力 | `moveMouse(...)` 导出地址 `0x10007e40c` 可见，但已恢复九种 Action 和选中 JS client 没有相应公开动作；未建立 resize/minimize 对外入口，不据此证明它们绝对不存在 |

当前开发产物为 ad-hoc 签名；构建支持 `COMPUTER_USE_SIGN_IDENTITY`，但没有配置生产证书或完成公证。不能宣称升级后权限稳定或已具备生产分发身份。

## 实现验证与调查限制

最终实现安装并运行在 `~/.aiclaw/computer-use/AIClaw Computer Use.app`；开发和正式包均复制已验证的辅助 App 到该位置，未修改产物不重复复制。已完成 universal 原生构建、签名完整性与分包校验、真实 18 工具 IPC 握手、原生纯逻辑测试、352 项渲染/宿主测试、相关 Go 包及 TypeScript/Go 检查。

本机辅助功能与屏幕录制状态均为 true。临时 AppKit fixture 实测通过应用发现/启动/正常退出/激活、窗口截图、查询及九类原生动作；text/Markdown/HTML 粘贴、中文/emoji、实际加粗样式和原剪贴板文本恢复均已验证。回执中的 `stepper=1`、拖动/滚动计数与提交的 Unicode 值用于核验效果，未以派发成功代替业务结果。遇到 AX 不确定结果时重新观察并核对回执，不重复提交。

- [实际验收回执 JSON](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/native-live-summary.json)
- [本实现的窗口验收截图](/Users/zhouyy/.codex/visualizations/2026/11/05/01a1aacb-6689-7b11-841f-9bcc35cb2d46/rea-search/native-fixture-verified.png)
- [当前原生架构与验证说明](computer-use.md)

这些是 **AIClaw 自有实现** 的证据，不是 Codex 原生运行证据；实际 x64 输入、任意第三方 App、生产签名、公证和升级权限保持尚未验收。

对 Codex 的调查恢复范围仍是可见导出类型/handler、部分 reflection 形状、五个选中客户端文件和已记录的 async 入口 thunk。未知范围包括被 strip 的符号、动态注册、未选中的 539 个 JS 文件、其余原生函数体、IPC 鉴权细节、私有后台输入、AX diff/应用专用策略及锁屏完整行为。Hopper Demo 无法保留本轮完整分析库，900 秒 continuation 分析未完成。本轮自己的代码与实测不能填补关于 Codex 的这些证据空白，不能称为闭源实现的逐行复刻。当前原生服务信任同 UID 的本机客户端，聊天审批由 AIClaw 的 Go 工具入口执行，并不等价于已验证的 Codex 发送者鉴权。
