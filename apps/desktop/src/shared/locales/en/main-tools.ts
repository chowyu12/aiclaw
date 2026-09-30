/**
 * 英文词典：主进程里的工具回复与报错（中文原文 → 英文）。
 *
 * 浏览器、computer use、定时任务工具的结果与报错，诊断信息，打开 / 内联显示文件的报错。
 * 这些既显示在界面的步骤详情里，也回给模型，跟界面语言走。
 * 写给模型的操作提示与不可信边界不在这里——那些在源码里固定英文。
 */
export const mainTools: Record<string, string> = {
  // ---------- 浏览器（main/browser.ts） ----------
  "不认识的浏览器操作：{action}": "Unknown browser action: {action}",
  "浏览器里还没有打开任何页面，先用 browser_navigate 打开一个网址。":
    "No page is open in the browser yet. Open a URL with browser_navigate first.",
  "只能打开 http/https 网址，给的是：{url}": "Only http/https URLs can be opened; got: {url}",
  "在你的浏览器里只能打开 http/https 网址。": "Only http/https URLs can be opened in your browser.",
  "已打开。": "Opened.",
  "没有 {index} 号元素：编号来自上一次快照，页面可能已经变了，先 browser_snapshot。":
    "No element [{index}]: numbers come from the last snapshot and the page may have changed. Take a browser_snapshot first.",
  "{index} 号元素现在不可见，先滚动或展开它所在的区域。":
    "Element [{index}] isn't visible right now. Scroll to it or expand the section it's in first.",
  "已点击 {index} 号。": "Clicked [{index}].",
  "没有 {index} 号元素：先 browser_snapshot 拿最新编号。":
    "No element [{index}]: take a browser_snapshot to get fresh numbers.",
  "{index} 号不是输入框。": "[{index}] isn't an input.",
  "已填入并提交 {index} 号。": "Typed into [{index}] and submitted.",
  "已填入 {index} 号。": "Typed into [{index}].",
  "{index} 号不是下拉框。": "[{index}] isn't a select.",
  "下拉框里没有「{value}」，可选：{options}": "The select has no option “{value}”. Options: {options}",
  "已在 {index} 号里选了「{value}」。": "Selected “{value}” in [{index}].",
  "没有 {index} 号元素。": "No element [{index}].",
  "已滚动。": "Scrolled.",
  "没有可以后退的页面。": "There's no page to go back to.",
  "已后退。": "Went back.",
  "已按 {key}。": "Pressed {key}.",
  "已截图（{width}×{height}）。": "Screenshot taken ({width}×{height}).",
  "现在用的是 AIClaw 自带的浏览器窗口，只有一个页面：{url}。要操作你自己浏览器里的标签页，在设置 → 浏览器里改成「用我的浏览器」。":
    "Using AIClaw's built-in browser window, which has a single page: {url}. To work with tabs in your own browser, switch to “Connect my browser” in Settings → Browser.",
  "现在用的是 AIClaw 自带的浏览器窗口，还没有打开页面。":
    "Using AIClaw's built-in browser window; no page is open yet.",
  "只有在「用我的浏览器」模式下才能接管标签页（设置 → 浏览器）。":
    "Tabs can only be taken over in “Connect my browser” mode (Settings → Browser).",
  "已切到标签页 {tabId}。": "Switched to tab {tabId}.",
  "AIClaw 浏览器": "AIClaw Browser",
  "页面加载超时（30 秒）": "Page load timed out (30 s)",
  "后退后页面加载超时": "Page load timed out after going back",
  "没有编号为 {tabId} 的网页标签页，先 browser_tabs 看一眼。":
    "No web tab numbered {tabId}. Check with browser_tabs first.",
  "脚本出错": "Script error",
  "页面脚本出错：{detail}": "Page script error: {detail}",
  "不认识的按键：{key}": "Unknown key: {key}",
  "截图失败：{error}。后台标签页有时截不了图，操作仍可按 browser_snapshot 的编号进行。":
    "Screenshot failed: {error}. Background tabs sometimes can't be captured; you can still act by the numbers from browser_snapshot.",
  "浏览器": "Browser",

  // ---------- 浏览器快照与标签页列表（main/browser-snapshot.ts） ----------
  "页面：{title}": "Page: {title}",
  "（无标题）": "(untitled)",
  "网址：{url}": "URL: {url}",
  "滚动位置：{percent}%（整页约 {pages} 屏）": "Scroll position: {percent}% (the page is about {pages} screens)",
  "（没有找到可交互的元素）": "(no interactive elements found)",
  "可交互元素（{total} 个，只列前 {shown} 个）：": "Interactive elements ({total}, showing the first {shown}):",
  "可交互元素（{total} 个）：": "Interactive elements ({total}):",
  "值={value}": "value={value}",
  "已选": "checked",
  "未选": "unchecked",
  "↓视口外": "↓off-screen",
  "正文（{url}，只给前 {limit} 字符，共 {total}）：": "Main text ({url}; first {limit} of {total} characters):",
  "正文（{url}）：": "Main text ({url}):",
  "（页面没有可读的正文）": "(the page has no readable text)",
  "浏览器里没有打开的网页标签页。": "No web tabs are open in the browser.",
  "浏览器里有 {count} 个网页标签页。": "The browser has {count} web tabs open.",
  "正在操作": "in use",
  "用户正在看": "user is viewing",
  "AIClaw 开的": "opened by AIClaw",
  "（{marks}）": " ({marks})",
  "，": ", ",
  "……还有 {count} 个没列出": "…and {count} more not listed",

  // ---------- 浏览器扩展（main/browser-bridge.ts） ----------
  "端口 {port} 被别的程序占着，浏览器扩展连不上来":
    "Port {port} is in use by another program, so the browser extension can't connect",
  "没能开始监听：{error}": "Couldn't start listening: {error}",
  "浏览器扩展的连接已关闭": "The browser extension connection was closed",
  "浏览器扩展 {seconds} 秒没有回应（{op}）": "The browser extension didn't respond within {seconds} s ({op})",
  "浏览器扩展断开了": "The browser extension disconnected",
  "换成了另一个浏览器里的扩展": "Switched to the extension in another browser",
  "浏览器扩展报错": "The browser extension reported an error",
  "浏览器扩展没连上：确认 Chrome / Edge 里装了「AIClaw 浏览器助手」并填了配对码（设置 → 浏览器）。":
    "The browser extension isn't connected: make sure the AIClaw browser extension (“AIClaw 浏览器助手”) is installed in Chrome / Edge and the pairing code is entered (Settings → Browser).",

  // ---------- computer use（main/computer.ts、computer-keys.ts） ----------
  "当前最前面的应用是 AIClaw 自己（{app}），已拒绝这次屏幕操作。computer use 用来驱动别的应用；点自己的窗口意味着可能在点审批弹窗。请先切到你要操作的那个应用。":
    "The frontmost app is AIClaw itself ({app}), so this screen action was refused. Computer use is for driving other apps; clicking AIClaw's own window could mean clicking an approval dialog. Switch to the app you want to operate first.",
  "没有屏幕录制权限，截不了屏。到「系统设置 → 隐私与安全性 → 屏幕录制」里勾上 AIClaw，然后重启应用。":
    "No Screen Recording permission, so the screen can't be captured. Enable AIClaw under System Settings → Privacy & Security → Screen Recording, then restart the app.",
  "没有取到屏幕画面。可能是屏幕录制权限刚授予、还没重启应用。":
    "Couldn't capture the screen. Screen Recording permission may have just been granted and the app not restarted yet.",
  "已截屏。屏幕逻辑尺寸 {width}×{height}，图片是 {scale} 倍分辨率。":
    "Screenshot taken. Logical screen size {width}×{height}; the image is at {scale}× resolution.",
  "{platform} 上还没有实现屏幕输入；目前支持 macOS 与 Windows。":
    "Screen input isn't implemented on {platform} yet; macOS and Windows are supported.",
  "没有辅助功能权限，动不了鼠标键盘。到「系统设置 → 隐私与安全性 → 辅助功能」里勾上 AIClaw。":
    "No Accessibility permission, so the mouse and keyboard can't be controlled. Enable AIClaw under System Settings → Privacy & Security → Accessibility.",
  "屏幕操作失败：{error}": "Screen action failed: {error}",
  "已执行 {action}。": "Done: {action}.",
  "macOS 上暂不支持单独移动鼠标（AppleScript 没有这个能力）。":
    "Moving the mouse on its own isn't supported on macOS yet (AppleScript can't do it).",
  "不认识的动作：{action}": "Unknown action: {action}",
  "按键组合为空": "The key combination is empty",
  "不认识的修饰键：{key}": "Unknown modifier key: {key}",

  // ---------- 定时任务工具（main/scheduler.ts） ----------
  "上次 {when} {status}": "Last: {when} {status}",
  "缺少任务内容": "Missing task details",
  "（没有下一次）": "(no next run)",
  "已建好定时任务「{name}」（编号 {id}）：{rule}，下次 {next}。到点时 AIClaw 要开着才会跑；用户可以在「设置 → 定时任务」里查看、暂停或修改。":
    "Created scheduled task “{name}” (id {id}): {rule}, next run {next}. AIClaw must be running at that time for it to run; the user can view, pause or edit it in Settings → Scheduled tasks.",
  "没有编号为 {id} 的定时任务（先用 schedule_list 看看）": "No scheduled task with id {id} (check with schedule_list first)",
  "已删掉定时任务「{name}」。": "Deleted scheduled task “{name}”.",
  "不认识的操作：{action}": "Unknown action: {action}",
  "正在跑": "running",
  "完成": "done",
  "失败": "failed",
  "错过了": "missed",

  // ---------- 诊断信息（main/diagnostics.ts、index.ts） ----------
  "AIClaw 诊断信息": "AIClaw diagnostics",
  "版本：{version}　Electron {electron}　Node {node}": "Version: {version}  Electron {electron}  Node {node}",
  "平台：{platform} {arch}": "Platform: {platform} {arch}",
  "（无）": "(none)",
  "{key}：{value}": "{key}: {value}",
  "运行时": "Runtime",
  "挂载": "Mounts",
  "目录": "Directories",
  "最近日志（{count} 行）": "Recent logs ({count} lines)",
  "日志": "Logs",
  "应用数据": "App data",
  "技能与记忆": "Skills & memory",
  "模型配置库": "Model config database",
  "当前会话工作区": "Current chat workspace",
  "（未设置）": "(not set)",
  "状态": "Status",
  "（未选）": "(none selected)",

  // ---------- 打开 / 内联显示文件（main/open-file.ts、open-file-rules.ts、media-files.ts） ----------
  "路径是空的": "The path is empty",
  "找不到 {path}": "Not found: {path}",
  "系统打不开它（{error}），已在访达里标出来": "The system couldn't open it ({error}), so it was revealed in Finder instead",
  "这是存放凭据的位置，不能从对话里打开": "This location stores credentials and can't be opened from a chat",
  ".{extension} 文件打开就等于执行，先在访达里给你标出来":
    "Opening a .{extension} file runs it, so it was revealed in Finder instead",
  "这个文件带可执行权限，打开就等于执行，先在访达里给你标出来":
    "This file is executable and opening it would run it, so it was revealed in Finder instead",
  "这个目录里的文件不能显示": "Files in this folder can't be displayed",
  "不支持内联显示 {type}": "Inline display isn't supported for {type}",
  "这种文件": "this kind of file",
  "这不是一个文件": "This isn't a file",
  "文件太大（{size} MB），在访达里打开看吧": "The file is too large ({size} MB); open it in Finder instead",
  "音频是空的": "The audio is empty",
  "音频太大（{size} MB，上限 25 MB）": "The audio is too large ({size} MB; the limit is 25 MB)",
  "不支持这种音频格式（{format}）": "Unsupported audio format ({format})",

  // ---------- 技能（main/skills.ts） ----------
  "「{name}」来自{source}（{dir}），请到那边删除。": "“{name}” comes from {source} ({dir}); delete it there.",
  "技能名为空": "The skill name is empty",
};
