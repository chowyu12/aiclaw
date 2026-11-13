# 内嵌 Cua Driver

AIClaw 使用官方 Cua Driver **0.49.0**，通过同版本 `@trycua/cua-driver` 的 `EmbeddedCuaDriverHost` 从 Electron 主进程直接启动后台子进程。产品不安装独立 CuaDriver.app，也不加载旧的自建执行层。macOS 的辅助功能和屏幕录制权限归属于 AIClaw；开发模式归属于 Electron。已有权限是否有效仍取决于系统对主程序签名身份的判断。

SDK 管理私有端点、父进程存活管道和退出；每个聊天轮次有独立的 MCP 连接、快照和 `AIClaw-…` 光标会话。最后一个连接关闭时停止子进程。主程序退出会等待会话清理。权限只读检查不弹窗，主动授权入口使用 SDK 的主程序权限 API；运行时核对 `check_permissions.source` 必须是 `host` 和 `embedded:true`。

## 后台执行约束

宿主暴露 18 个窗口级 Driver 工具和 1 个宿主窗口等待工具，强制使用精确 PID/window ID。输入优先使用 `delivery_mode=background`；单步操作被驱动明确拒绝为不支持后台时，宿主自动尝试前台执行，无需额外确认。批处理启用驱动的 `foreground_fallback=true`，只对明确被后台拒绝的步骤重试；部分成功时不能整体重放。单步、批量动作、等待条件和末尾观察都不能覆盖路由、会话或交付模式。全局桌面、剪贴板、切前台、任意脚本、隐藏代理光标、模型触发授权及文件输出均不暴露。前台操作按原会话的工具审批策略执行；不能改用 shell/osascript 绕过。

Driver 原生光标独立于用户鼠标；AIClaw 保留聊天页任务状态、暂停/恢复/停止及停止快捷键。暂停和停止立即撤销连接，不等待正在运行的批处理完成后再执行；恢复创建新连接，旧输入不重放。不确定结果必须重新观察，不能自动重试提交。

后台执行不提供独立桌面：用户与 AI 同时编辑同一个目标窗口仍可能发生内容冲突。工具包和应用是否接受后台输入由 Driver 判断，不承诺所有应用都可后台自动化。

## 连续动作

新建前通过 `computer_list_windows` 获取现有窗口 ID，再用 `computer_wait_new_window` 等待新增的具名普通窗口。列表和窗口快照以 JSON 字符串返回，解析其中的 `data` 使用原生快照 ID 与元素 token，不要自行猜测。

菜单命令使用 `computer_invoke_menu` 和应用的实际菜单路径；后台菜单被明确拒绝时，宿主记录原前台窗口、临时激活目标并尝试恢复；如果用户已切换到其他应用，不覆盖用户的切换。对于连续相同的快捷键、菜单、点击和批处理，如果没有可比较的目标窗口变化，宿主拒绝重复派发；被明确拒绝的过期 token 可以换成新快照中的 token。状态变化只表示窗口有变化，仍需验证预期效果。

优先使用 `computer_run_steps` 的 `{tool,args}` 步骤、语义角色/名称、`wait_for`、`expect` 和一次末尾 `observe`。同一批处理限定同一个精确窗口，首个失败后停止；后续界面判断交回模型。窗口读取使用窄查询及 `since:"latest"` 差异，避免完整 AX 树和重复截图。截图输出固定 PNG，兼容内核图片消息。

这减少模型往返，不会消除模型本身延迟或已有会话的大量历史上下文。

## 构建、升级和验证

`tools/cua-driver/runtime.json` 固定 Release、各平台完整归档及 SHA256；构建下载并校验，缓存也核对文件摘要。归档中的独立 App 不进入安装包。仅打包可执行文件、目标平台 SDK/N-API 库、JS 依赖和许可声明；macOS 将这些内嵌二进制与主程序一起签名。

升级时同步修改该清单、`CUA_VERSION` 和 npm SDK 精确版本，然后运行：

```sh
make build-computer
node scripts/update-cua-contract.mjs
make build-go build-desktop typecheck
npm run test:computer-native
npm run test:renderer
npm run smoke:desktop
```

必须审查新工具 schema 和后台限制，不直接把上游全部工具开放给模型。保留 SDK、Driver 和生成契约的一致性；禁止静默下载 latest。

macOS 签名包身份/模块验收（不发输入）：

```sh
node scripts/smoke-packaged-computer.mjs /absolute/path/AIClaw.app/Contents/MacOS/AIClaw
```

真实后台验收（临时 App，仅输入测试内容；不切换用户前台窗口）：

```sh
AICLAW_CUA_BACKGROUND_TEST=1 node scripts/smoke-packaged-computer.mjs /absolute/path/AIClaw.app/Contents/MacOS/AIClaw
```

验收覆盖后台中文填写、点击、效果校验、截图、AIClaw 光标启用，以及操作各阶段的前台应用和鼠标位置。用户主动切应用或移动鼠标会使位置断言失败，需要结合执行阶段判断；它不代表所有应用、显示器或桌面空间均经过验证。Windows/Linux 的包布局有回归测试，真实桌面验收需在相应系统另行执行。

上游：[Release](https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.49.0)、[内嵌宿主说明](https://github.com/trycua/cua/blob/cua-driver-rs-v0.49.0/libs/cua-driver/rust/Skills/cua-driver/EMBEDDING.md)。旧 Swift/Windows/Linux 实现和对应实验脚本仅作历史对照；当前 `createComputerRuntime` 不会回退到这些执行层。

TextEdit 实测中，后台新建与文本输入可用，macOS 将后台“保存”和“保存为”菜单置灰。宿主会在确认拒绝后尝试前台菜单；命令已发送不等于保存成功，必须验证保存结果。其他应用的后台菜单能力需要分别验证。
