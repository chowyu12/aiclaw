# macOS 后台键盘：公开 PID 投递可行性探针

状态：macOS 受限后台按键使用公开 PID 投递；Cocoa literal text 使用 PID Unicode，web plain field/空 text area 在派发前选定公开 AX literal adapter。AppKit 与独立 Electron fixture 已有真实消费证据，完整截图value/selection锚点的最终Electron input验收已通过；不能宣称任意应用或任意组合键均支持。

## 公开 API 与证据边界

- 本机 macOS SDK `CoreGraphics.framework/Headers/CGEvent.h:372` 声明 `CGEventPostToPid(pid_t, CGEventRef)`，最低 macOS 10.11；前面的 `CGEventPostToPSN` 标记为应改用 PID 版本。它以进程为路由目标，不提供窗口或可编辑控件参数。[Apple API](https://developer.apple.com/documentation/coregraphics/cgevent/posttopid(_:))
- [`CGEventSourceStateID.privateState`](https://developer.apple.com/documentation/coregraphics/cgeventsourcestateid/privatestate) 使用私有事件状态表。这不等于获得目标应用控制权限，也不保证目标框架处理后台输入。
- [`keyboardSetUnicodeString`](https://developer.apple.com/documentation/coregraphics/cgevent/keyboardsetunicodestring(stringlength:unicodestring:)) 可覆盖事件上的 Unicode 内容；Apple 明确指出应用框架可能忽略该内容并自行按虚拟键码和事件状态翻译。因此 Unicode、IME 与跨框架行为必须实测。
- 选定 PID 不证明窗口和文本焦点正确。任何支持都需在每次派发前验证固定窗口、已观察且仍聚焦的可编辑元素；不能为“让它成功”而激活应用、移动鼠标或回退全局 event tap。

## 产品实现与限制

1. 复用 `interaction_mode=background`。不新增 `sky_key` 私有模式，不借用 Codex 运行时。
2. 普通最新观察创建可选的 focused-element proof：进程与启动代际、明确窗口 ID/几何、目标 AXWindow、聚焦 AX 元素的引用及角色/标题/描述/标识符，以及私有 SHA-256 文本摘要和 UTF-16 选区（不把实际文本放入metadata）。截图前后所有锚点必须一致，首次输入前再次核验；同字段文字或光标变化亦要求重新观察。超过两百万UTF-16单位或不可读文本/选区不能建立proof。观察、动作、窗口切换或取消沿用现有代际失效；没有 proof 时只拒绝后台键盘，不改变已有前台路径。
3. 起步只探测 `AXTextField/AXTextArea`，且元素明确可编辑、属于指定窗口、不是 secure/protected 控件。每个键以及每个 Unicode chunk 前重新校验相同元素与窗口。目标 app 已在自己的目标窗口保留文本焦点时才尝试；不设置 AXFocused 来制造前提。
4. 使用 `CGEventSource(stateID: .privateState)` 和 `postToPid(originalPID)`，不调用 `actionActivate` 或 `.post(tap:)`。Cocoa Unicode 沿用 UTF-16 有界 chunk，避免拆开 surrogate pair。web字段在任何键派发前选择AX literal路径：读取完整value/selection，明确两者可写，保留plain AXTextField前后文本，AXTextArea仅允许空值与空选区；单次写入后回读，失败不重放，绝不在已发PID键后自动转AX。修饰键需成对处理且按逆序释放；首批可先只启用实测通过的普通键和 Unicode。
5. 发下键后立即建立固定原 PID 的 release 清理；取消、超时、焦点变化均尽力发对应 key-up。释放不改发到新 PID，原进程已退出时不向复用该 PID 的新进程投递。
6. 派发前后核对其他前台应用 PID、真实鼠标坐标、剪贴板 changeCount。任何变化标记未知并停止，不能自动恢复前台/指针或重放动作。应用框架可能自行激活；这仍算该场景不满足严格后台条件。
7. `postToPid` 返回 void，不提供消费确认。产品回执只能描述投递路线，并让现有观察/验证核对业务效果；不能把函数返回当作文字已写入。真实探针必须从 fixture 文本、UTF-16 选区和 keyDown/up 回执证明消费。

## 双 App 探针与验收

探针仅允许两个独立的自有临时 fixture，不接触企业微信、浏览器业务页面或真实文档。发送者必须有可验证的辅助功能授权；不通过修改 TCC 或偷偷切换目标前台来修复失败。

| 阶段 | 场景 | 通过标准 |
| --- | --- | --- |
| 准备 | target 有已明确聚焦的 NSTextView，sentinel 保持前台；记录两个 PID、目标 window ID、frame、焦点引用、鼠标和剪贴板代际 | 前台是 sentinel，target 的 AX 焦点能唯一证明属于指定窗口；任一不确定则零事件退出 |
| Unicode | 向 target PID 投递中文、emoji、组合字符 | target 文本包含精确插入值和 UTF-16 选区；sentinel 文本及键盘事件计数不变；前台、鼠标、剪贴板不变 |
| 基础按键 | 向 target 投递 ArrowLeft/ArrowRight/Backspace 等受控键 | fixture keyDown/up 成对，选区/文本的实际变化符合预期；无前台/指针/剪贴板变化 |
| 按住取消 | 投递一个有持续时间的键，在 key-down 后取消 | 原 target 收到对应 key-up；私有源按键状态释放；不得向 sentinel 或复用 PID 投递 |
| 不确定目标 | 无 focused window/field、窗口更换、frame更改、角色/标识变化、secure控件、进程重启、缺权限 | 在第一个 key-down 前拒绝，两个 fixture 事件计数均无新增；没有激活或全局回退 |
| 用户接管 | 已排队下一 chunk 前改变前台/目标焦点 | 后续 chunk 不派发，必要 key-up 完成；回执保留可能已输入的部分，不自动重试 |
| 不支持框架 | AppKit 以外的引擎自行忽略 Unicode/后台事件 | 记录实际失败与能力范围；不以转前台、剪贴板粘贴或 AX setValue 冒充 PID 后台输入通过；可在派发前明确选择并单独标识经过验证的AX literal实现 |

正式安全前置函数与 proof 失效覆盖放在 `tools/computer-use/Tests/ActionsTests.swift`；早期未接产品的候选 guard 及其测试已移除。真实验收脚本为 `scripts/smoke-computer-background-keyboard.cjs`，其 AppKit target/sentinel 和独立 Electron target 均为自有临时 App。剪贴板仅只读全部格式并做 SHA-256 比较，不写 marker，不覆盖用户内容。

## 已获得的实际回执

- AppKit：`/tmp/aiclaw-background-keyboard-live.log` 已通过中文、emoji、组合字符跨 UTF-16 chunk 的精确文本消费、Left 的真实选区变化，前台/鼠标/全格式剪贴板不变，sentinel 零输入。请求十秒按住，在断连取消后 23ms 收到对应 key-up。
- AppKit 安全负例：query 缺完整截图 proof、focused field 变化、窗口移动、secure field、focused window 变化均在 key-down 前拒绝，fixture 输入事件增量为零。
- 进程代际：raw-spawn fixture 的 `NSRunningApplication.launchDate` 实际为 nil，不能依赖它判断 PID 重用。产品改用公开 `proc_pidinfo(PROC_PIDTBSDINFO)` 的同 UID 启动秒/微秒，并保留缓存 application 的终止状态和精确 bundle ID 验证；读不到代际时拒绝。
- Electron：应用级 AXFocusedUIElement 在后台不可用，但完整未截断AX树可证明唯一明确AXFocused=true的editable，且祖先属于固定窗口。产品只在此证据齐备时使用，不猜“第一个editable”。
- Electron 的 CGEvent Unicode 和公开 NSEvent windowNumber 探针均曾真实失败：keydown出现但DOM无可信input/业务值更新，故没有发布为Electron literal text实现，也未派发后自动重试AX。失败日志为 `/tmp/aiclaw-background-keyboard-electron-input-pid.log`。
- Electron 独立HTML input的最终验收 `/tmp/aiclaw-background-keyboard-electron-input-final.log` 已通过：选中seed中间ee后得到 `s中文 👩🏽‍💻 é 跨分块输入Zd`，UTF16选区[21,0]，可信DOM input与仅由该事件更新的business_value精确相同；Left真实消费，十秒hold断连46ms释放。receipt明确为AX literal，并不声称键盘等价。同field文字/光标变化分别对type_text与press_key测试，均输入前拒绝，文本/选区/DOM输入/原生按键无动作造成的变化。
- 十二种普通/Shift及编辑导航键的逐键副作用探针在 `/tmp/aiclaw-background-keyboard-electron-key-probe.log` 全部保留前台Sentinel的文本、选区、marked state、keydown/up计数以及前台PID/指针/所有剪贴板格式。目标a/Shift+a/删除和方向键有实际消费；Tab使目标焦点改变并正确返回需重观察，不能承诺其后仍可沿用旧观察。
- 较早一次连续验收曾发现Sentinel末尾字符缺失，原因未归属。保留该未知事实；加强后的逐步骤文本/选区/marked state对照与十二键独立探针没有复现，不能把早期现象无证据归为用户输入。


目前仅允许普通/Shift 按键和最多 16000 UTF-16 单位的后台 Unicode 输入；Cmd/Ctrl/Alt、后台鼠标、粘贴、滚动和拖动保持明确拒绝。具体平台声明必须由对应后端与真实验收决定。

最终Electron空textarea的可信DOM/business消费与非空rich area零派发拒绝分别保存在 `/tmp/aiclaw-background-keyboard-electron-empty-final.log` 和 `/tmp/aiclaw-background-keyboard-electron-rich-refusal-final.log`。持久化验收副本与源码SHA256位于 `/Users/zhouyy/.codex/visualizations/2026/11/06/01a1adfb-4653-7b53-b81f-62dbebeed8c0/computer-use-parity/background-keyboard-final/`。最终AppKit完整后台及全部九类前台回归均已通过，日志分别为 `aiclaw-background-keyboard-appkit-final.log`（23ms取消释放、同field value/caret旧观察0派发）与 `aiclaw-computer-nine-actions-final.log`。旧语义后台与watch最终全部通过：首次seq1→2的真实变化是系统加入AXMenuButton Window sharing，并有对应PNG变化，ax_event_count仍为0。修正测试前提为验证该有界启动生命周期（只容许明确Window sharing AX插入与对应图像变化，最多5帧），随后仍严格要求900ms无更新，再验证业务AX delta重建、图像更新、独立动作观察不失效与stop取消等待。`aiclaw-background-semantic-final.log`保留完整metadata，`aiclaw-semantic-watch-start.png`与`aiclaw-semantic-watch-sharing-0.png`已视图核对：只有系统窗口共享胶囊新出现，未把真实变化当假噪声忽略。
