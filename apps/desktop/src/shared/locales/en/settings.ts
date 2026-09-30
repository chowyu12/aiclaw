/** 英文词典：settings 这一组界面（中文原文 → 英文）。 */
export const settings: Record<string, string> = {
  // ---------- 通用 ----------
  "刷新": "Refresh",
  "取消": "Cancel",
  "复制": "Copy",
  "已复制": "Copied",
  "删除": "Delete",
  "启用": "Enabled",
  "重试": "Retry",
  "保存中…": "Saving…",
  "未命名": "Untitled",
  "名字": "Name",
  "类型": "Type",
  "端点": "Endpoint",
  "已配置": "Configured",
  "保存 Key": "Save key",
  "没配 Key": "No key",
  "添加": "Add",
  "。": ".",
  "改动下一个会话生效。": "Takes effect in the next session.",
  "没有匹配「{keyword}」的模型。": "No models match “{keyword}”.",

  // ---------- 配置页：错误 ----------
  "重新生成失败：{error}": "Couldn't regenerate: {error}",
  "打不开扩展页：{error}": "Couldn't open the extensions page: {error}",
  "保存失败：{error}": "Couldn't save: {error}",
  "配置未能加载": "Couldn't load settings",
  "本地配置没读出来，页面暂时不能用。具体原因见页面顶部的错误条；没有错误条的话，重新执行":
    "Local settings couldn't be read, so this page is unavailable for now. See the error bar at the top for details; if there isn't one, run",
  "再启动。": "again and restart.",

  // ---------- 配置页：模型 ----------
  "{n}K 上下文": "{n}K context",
  "{n} 上下文": "{n} context",
  "服务 {id}": "Provider {id}",
  "模型": "Model",
  "新会话默认用哪个模型。端点、Key 与模型清单在「模型服务」页管理。":
    "The model new sessions use by default. Endpoints, keys, and model lists are managed on the “Model providers” page.",
  "还没有能用的模型服务。到「模型服务」页添加一个端点、填上 Key、写上模型名，回来再选。":
    "No usable model providers yet. On the “Model providers” page, add an endpoint, enter a key, and list model names, then come back to choose.",
  "去添加": "Add one",
  "默认模型": "Default model",
  "从已配置的模型服务里选一个": "Choose from your model providers",
  "搜索模型或服务名": "Search models or providers",
  "正在读取…": "Loading…",
  "没有能用的模型。": "No usable models.",
  "没设过的话第一次启动会自动挑一个。新会话用它；对话框上方切模型只影响那一个会话。":
    "If unset, one is picked automatically on first launch. New sessions use it; switching models above the composer only affects that session.",
  "推理档位": "Reasoning effort",
  "上下文窗口": "Context window",
  "选模型时自动填": "Filled in when you pick a model",
  "上下文窗口填了才能在撑满之前主动压缩历史；不填也能跑，只是要等上游报错再压，白花一次请求。":
    "With a context window set, history is compacted before it fills up. Without one it still works, but compaction waits for the upstream error, wasting a request.",
  "选模型时会用清单里记着的值自动填——那个值在「模型服务」页点「按 models.dev 标记能力」时一并写进去。":
    "Picking a model fills it in from the value stored in the list — that value is written when you tag capabilities from models.dev on the “Model providers” page.",

  // ---------- 配置页：多模态 ----------
  "多模态": "Multimodal",
  "对话之外的几件事各自交给一个模型。候选来自「模型服务」页上勾过对应能力的模型——没有哪个对话模型四样都好，而你手上往往各有一个便宜的专用模型。":
    "Tasks beyond chat can each go to their own model. Candidates are models tagged with that capability on the “Model providers” page — no chat model is great at all four, and you often have a cheap specialized model for each.",
  "没有标记为「{role}」的模型": "No models tagged “{role}”",
  "不使用": "None",
  "没配的角色对应的工具不会出现在会话里——给模型一个用不了的工具，它会调、会失败、会重试，而失败原因它无从修复。":
    "Tools for unassigned roles don't appear in sessions — give a model a tool it can't use and it will call it, fail, and retry, with no way to fix the cause.",
  "看图是例外：它不是工具，而是在对话模型不认图时替它读图。":
    "Vision is the exception: it isn't a tool, it reads images for the chat model when that model can't.",

  // ---------- 配置页：执行 ----------
  "执行": "Execution",
  "Agent 在这台电脑上能碰什么、动手前问不问你。":
    "What the agent can touch on this computer, and whether it asks before acting.",
  "工作区是": "The workspace is set ",
  "按会话": "per session",
  "设的，不在这里——在对话页顶部那个「工作区」上点一下就能改，也可以不设。":
    ", not here — click “Workspace” at the top of the chat to change it, or leave it unset.",
  "它决定相对路径按哪儿解析、写哪里不用问你。":
    "It decides how relative paths resolve and where the agent can write without asking.",
  "本版本没有沙箱：Agent 的命令直接在你的电脑上执行。":
    "This version has no sandbox: agent commands run directly on your computer.",
  "危险命令（如": "Dangerous commands (like",
  "）硬拒绝；删除、提权、改系统设置这类会先问你；普通命令不问。":
    ") are always refused; deleting, escalating privileges, or changing system settings asks you first; ordinary commands don't ask.",
  "读文件不限于工作区，但涉及凭据的目录（":
    "Reading files isn't limited to the workspace, but credential directories (",
  "这些）一律拒绝。": "and the like) are always refused.",
  "审批档位": "Approval profile",
  "没经过确认的命令跑在系统沙箱里（macOS）": "Run unapproved commands in the system sandbox (macOS)",
  "开着的时候，不需要确认的命令由系统内核限制：只能写会话工作区与临时目录，读不到":
    "When on, commands that don't need approval are restricted by the OS: they can only write to the session workspace and temp directories, and can't read",
  "这类凭据目录。": "or other credential directories. ",
  "你点过「允许」的命令不受限制": "Commands you clicked “Allow” on aren't restricted",
  "——那正是确认的含义。": " — that's what approval means.",
  "只有某个命令被沙箱挡了、而你确定它没问题时才需要关掉它。":
    "Only turn this off when the sandbox blocks a command you're sure is safe.",
  "Windows 上没有这一层。": "Not available on Windows.",
  "代码模式：工具收进一个": "Code mode: tools are folded into a single",
  "，模型写 JavaScript 调用": " tool, and the model calls them by writing JavaScript",
  "工具多才划算": "It pays off with many tools",
  "：只有内置那几个工具时基本打平（exec 的说明本身有固定开销），而 161 个接口的定义原本约 110KB，收进去之后是 7KB 左右，而这部分":
    ": with only the built-in tools it roughly breaks even (exec's own description has a fixed cost), but 161 API definitions shrink from about 110KB to around 7KB — and that part is ",
  "每次请求都要重发、且不参与上下文压缩": "resent with every request and never compacted",
  "更大的收益是十几次查询可以写成一段循环，中间结果不再进上下文。":
    "The bigger win: a dozen lookups can become one loop, and intermediate results stay out of the context.",
  "代价是模型得会写对代码——小模型在这上面更吃力，换模型之后值得再试一次。":
    "The trade-off is that the model has to write correct code — smaller models struggle more, so it's worth retrying after switching models.",
  "脚本里的每次工具调用": "Every tool call inside a script ",
  "照常走审批": "still goes through approval",
  "，也照常受沙箱限制。": " and is still sandboxed.",
  "开了之后，对话页顶部「工具」菜单里会显示实际省了多少。":
    "Once on, the “Tools” menu at the top of the chat shows how much it actually saves.",

  // ---------- 配置页：浏览器 ----------
  "浏览器：模型按元素编号打开网页、点、填、读（勾上后可以选用你自己的 Chrome / Edge）":
    "Browser: the model opens pages and clicks, fills, and reads by element number (once on, you can use your own Chrome / Edge)",
  "在你的 {browser} 里操作：后台标签页、用你已有的登录、不抢鼠标":
    "Working in your {browser}: background tabs, your existing logins, never takes over the mouse",
  "改回 AIClaw 自带窗口": "Switch back to AIClaw's built-in window",
  "现在用 AIClaw 自带的浏览器窗口（独立的登录，窗口可见）。也可以让它在你自己的浏览器里、用你已有的登录、在后台标签页里操作：":
    "Currently using AIClaw's built-in browser window (separate logins, visible window). It can also work in your own browser, with your existing logins, in background tabs:",
  "正在打开…": "Opening…",
  "连接我的浏览器（{name}）": "Connect my browser ({name})",
  "连接我的浏览器": "Connect my browser",
  "安装扩展": "Install the extension",
  "在浏览器里确认配对": "Confirm pairing in the browser",
  "连上": "Connected",
  "浏览器里弹出了配对页：核对代码一致，在":
    "A pairing page opened in your browser. Check that the code matches, then ",
  "浏览器里": "in the browser",
  "点「允许」。": ", click “Allow”.",
  "在打开的商店页点「获取」，装好后浏览器会自动弹出配对页。":
    "Click “Get” on the store page that opened. Once installed, the browser opens the pairing page automatically.",
  "{name} 的扩展页已经打开，扩展目录的路径也复制好了：打开「开发者模式」→ 点「加载已解压的扩展程序」→ 按":
    "{name}'s extensions page is open and the extension folder path is copied: turn on “Developer mode” → click “Load unpacked” → press",
  "粘贴、回车、点「选择」。": "to paste it, press Return, then click “Select”.",
  "装好后浏览器会自动弹出配对页。": "Once installed, the browser opens the pairing page automatically.",
  "没找到 Chrome / Edge。在浏览器的扩展页里打开「开发者模式」，「加载已解压的扩展程序」选这个目录：":
    "Chrome / Edge not found. On your browser's extensions page, turn on “Developer mode”, then use “Load unpacked” and choose this folder:",
  "重新打开扩展页": "Reopen extensions page",
  "改用 {name}": "Use {name} instead",
  "在访达中显示扩展目录": "Show extension folder in Finder",
  "取消，改回自带窗口": "Cancel and switch back to the built-in window",
  "它能做什么、怎么让它停下来": "What it can do, and how to stop it",
  "AIClaw 只在它自己开的后台标签页（「AIClaw」标签组）里操作，不切换你正在看的页面；要接管你已经打开的页面，它会先在 AIClaw 里请你确认。":
    "AIClaw only works in background tabs it opens itself (the “AIClaw” tab group) and never switches the page you're viewing; before taking over a page you already have open, it asks you in AIClaw.",
  "操作期间浏览器顶部会显示「正在调试此浏览器」，点「取消」就能让它立刻停手；空闲一分钟后提示条自己消失。":
    "While it works, the browser shows a “debugging this browser” banner at the top; click “Cancel” to stop it immediately. The banner disappears after a minute of inactivity.",
  "配对页没弹出来？点浏览器工具栏上的 AIClaw 图标，把配对码粘进去：":
    "Pairing page didn't open? Click the AIClaw icon in the browser toolbar and paste the pairing code:",
  "复制配对码": "Copy pairing code",
  "重新生成": "Regenerate",
  "配对码等于这个浏览器的钥匙，别发给别人。": "The pairing code is the key to this browser — don't share it.",
  "模型拿到的是页面上可交互元素的": "The model gets a ",
  "编号列表": "numbered list",
  "（链接、按钮、输入框），按编号操作，不靠屏幕坐标——比截图便宜、比坐标可靠。":
    " of the page's interactive elements (links, buttons, inputs) and acts by number rather than screen coordinates — cheaper than screenshots and more reliable than coordinates.",
  "窗口是可见的，登录、验证码你随时能接手，登录态跨会话保留。":
    "The window is visible, so you can step in for logins and CAPTCHAs at any time; logins persist across sessions.",
  "打开网址会请你确认": "Opening a URL asks for approval",
  "；页面里的点、填、滚不问，严格档位下每一步都问。":
    "; clicking, filling, and scrolling on the page don't, except in the strict profile, where every step asks.",
  "网页内容一律当作不可信的外部资料交给模型。": "Web content is always passed to the model as untrusted external input.",
  "computer use（截屏 + 鼠标键盘）是一个插件，开关在「插件」页：启用即授权，那一页写着它能碰什么。":
    "Computer use (screenshots + mouse and keyboard) is a plugin, toggled on the “Plugins” page: enabling it grants access, and that page lists what it can touch.",
  "需要操作浏览器之外的应用时用它；只是上网的话浏览器工具更省更准。":
    "Use it to operate apps outside the browser; for just browsing the web, the browser tool is cheaper and more accurate.",

  // ---------- 配置页：数据与诊断 ----------
  "数据": "Data",
  "会话与执行记录全部留在本机，不回写云端。": "Sessions and run history stay on this computer and are never synced to the cloud.",
  "模型服务那边只能看到发给它的请求。": "Model providers only see the requests sent to them.",
  "会话保留天数": "Session retention (days)",
  "清空全部本地数据": "Clear all local data",
  "会话记录、凭据、配置将一并清除，无法恢复。": "Sessions, credentials, and settings will all be erased. This can't be undone.",
  "确认清空": "Clear everything",
  "诊断": "Diagnostics",
  "版本、平台、挂载结果与内核最近的输出，拼成一段可以直接贴给别人的文本。":
    "Version, platform, mount results, and recent core output, combined into text you can paste to someone directly.",
  "凭据已经抹掉，只会显示「已配置 / 未配置」。": "Credentials are redacted and only shown as “configured / not configured”.",
  "收起": "Hide",
  "查看诊断信息": "View diagnostics",

  // ---------- 模型服务 ----------
  "OpenAI 兼容": "OpenAI-compatible",
  "通义千问": "Qwen",
  "Claude（OpenAI 兼容入口）": "Claude (OpenAI-compatible endpoint)",
  "Gemini（OpenAI 兼容入口）": "Gemini (OpenAI-compatible endpoint)",
  "新模型服务": "New model provider",
  "删除模型服务「{name}」？用它的会话下次恢复时要重新选模型。":
    "Delete model provider “{name}”? Sessions using it will need a new model when they resume.",
  "清单为空": "No models",
  "{n} 个模型": "Models: {n}",
  "模型服务": "Model providers",
  "一个模型服务就是一个 OpenAI 兼容端点：填地址、Key，再写上要用的模型名。":
    "A model provider is an OpenAI-compatible endpoint: enter its URL and key, then list the models you want to use.",
  "清单里的模型就是对话页顶部能选的那些；改动立刻生效，新会话与切模型都用得上。":
    "Listed models are the ones you can pick at the top of the chat. Changes apply immediately, for new sessions and model switches alike.",
  "Key 只存进本机的配置库，不写进日志、不进诊断包、不经界面回显。":
    "Keys are stored only in the local settings database — never written to logs, included in diagnostics, or shown in the UI.",
  "Claude 与 Gemini 走的是它们的 OpenAI 兼容入口——原生接口不支持。":
    "Claude and Gemini use their OpenAI-compatible endpoints — native APIs aren't supported.",
  "添加模型服务": "Add model provider",
  "还没有模型服务。添加一个，填上端点与 Key，再写上模型名。":
    "No model providers yet. Add one, enter its endpoint and key, then list model names.",
  "给这个服务起个名字": "Name this provider",
  "填到": "Include the path up to",
  "。留空用该类型的默认端点（OpenAI 兼容类型必须填）。":
    ". Leave blank to use the type's default endpoint (required for OpenAI-compatible).",
  "模型清单": "Models",
  "{n} 个": "{n} total",
  "{n} 个标了能力": "{n} tagged",
  "搜索模型": "Search models",
  "手动添加一个模型名，回车确认": "Add a model name manually, press Return to confirm",
  "还没有模型。从端点拉一遍，或者手动加一个——清单里的模型名就是发给服务的那个名字。":
    "No models yet. Fetch them from the endpoint or add one manually — the name in the list is exactly what's sent to the provider.",
  "从清单里移除": "Remove from list",
  "还有 {n} 个没列出来，搜索名字找它们。": "{n} more not shown — search by name to find them.",
  "勾了能力的模型会出现在「配置 → 多模态」对应角色的候选里。不勾不影响它当对话模型用。":
    "Models tagged with a capability appear as candidates for that role under “Settings → Multimodal”. Untagged models still work as chat models.",
  "拉取中…": "Fetching…",
  "从端点拉取模型名": "Fetch models from endpoint",
  "查询中…": "Looking up…",
  "自动标记能力": "Auto-tag capabilities",
  "查到 {n} 个": "Matched: {n}",
  "，{n} 个表里没有、按名字猜的": ", guessed from name: {n}",
  "，另 {n} 个表里没有，要自己勾。": ", not found: {n} (tag these manually).",
  "{note}。": "{note}.",
  "端点返回 {n} 个，已并进清单。": "Endpoint returned: {n}. Merged into the list.",
  "先保存 Key 才能拉取。": "Save a key before fetching.",

  // ---------- 角色（model-roles.ts） ----------
  "看图": "Vision",
  "听写": "Transcription",
  "朗读": "Speech",
  "画图": "Image generation",
  "对话模型不认图时，用它把图转成文字再交给对话模型":
    "When the chat model can't read images, this one describes them in text for it",
  "把音频转成文字（transcribe_audio 工具）": "Turns audio into text (transcribe_audio tool)",
  "把文字读成语音（speak 工具）": "Reads text aloud (speak tool)",
  "按描述生成图片（generate_image 工具）": "Generates images from descriptions (generate_image tool)",
  "名字像朗读（TTS）模型，却勾了听写": "Name looks like a speech (TTS) model, but Transcription is checked",
  "名字像听写（ASR）模型，却勾了朗读": "Name looks like a transcription (ASR) model, but Speech is checked",

  // ---------- 搜索引擎 ----------
  "SerpAPI 的 api_key": "SerpAPI api_key",
  "阿里云 IQS": "Alibaba Cloud IQS",
  "阿里云 IQS 的 API Key": "Alibaba Cloud IQS API key",
  "删除搜索引擎「{name}」？": "Delete search engine “{name}”?",
  "生效中": "Active",
  "已启用（排在后面，不生效）": "Enabled (lower in the list, not active)",
  "已停用": "Disabled",
  "搜索引擎": "Search engines",
  "配一个引擎、填上 Key、启用，模型就多一个": "Set up an engine, add its key, and enable it to give the model a",
  "工具。": "tool.",
  "一次只用一个：列表里第一个启用且配了 Key 的。改动下一个会话生效。":
    "Only one is used at a time: the first enabled engine with a key. Changes take effect in the next session.",
  "搜索是只读的，调用时不会请你确认。Key 只存进本机的配置库，不回显。":
    "Search is read-only, so it runs without asking. Keys are stored only in the local settings database and never shown again.",
  "添加搜索引擎": "Add search engine",
  "还没有搜索引擎。没有它模型也能用，只是不能联网搜。":
    "No search engines yet. Models work without one; they just can't search the web.",
  "留空用默认端点。自建代理或私有化部署才需要改。":
    "Leave blank for the default endpoint. Only change it for a self-hosted proxy or private deployment.",
  "搜点什么试试": "Try a search",
  "搜索中…": "Searching…",
  "试一下": "Test",
  "先保存 Key 才能试。": "Save a key before testing.",
  "连上了，但没有结果。": "Connected, but no results.",

  // ---------- 用量 ----------
  "读不到用量：{error}": "Couldn't load usage: {error}",
  "用量从 {month} 月 {day} 日开始记录，更早的没有数据。":
    "Usage tracking started on {month}/{day}; there's no data before that.",
  "{day}：输入 {input}，输出 {output}，模型 {models} 次，工具 {tools} 次":
    "{day}: input {input}, output {output}, model calls {models}, tool calls {tools}",
  "用量": "Usage",
  "token、模型调用、工具与技能。所有会话都算，包括微信、企业微信的通道会话。":
    "Tokens, model calls, tools, and skills. Counts every session, including WeChat and WeCom channel sessions.",
  "时间范围": "Time range",
  "{n} 天": "{n}d",
  "还没有记录。用量从这个版本开始记，聊过几轮之后这里就有数了。":
    "Nothing recorded yet. Usage tracking starts with this version — numbers show up here after a few chats.",
  "输入 token": "Input tokens",
  "输出 token": "Output tokens",
  "模型调用": "Model calls",
  "失败 {n}": "Failed: {n}",
  "工具调用": "Tool calls",
  "会话": "Sessions",
  "每天的 token": "Tokens per day",
  "输入": "Input",
  "输出": "Output",
  "每天的 token 用量": "Daily token usage",
  "按模型": "By model",
  "调用": "Calls",
  "失败率": "Failure rate",
  "（未知）": "(unknown)",
  "这段时间没有模型调用。": "No model calls in this period.",
  "按工具": "By tool",
  "工具": "Tools",
  "来源": "Source",
  "平均耗时": "Avg. duration",
  "这段时间没有工具调用。": "No tool calls in this period.",
  "技能": "Skills",
  "{n} 次": "{n}×",
  "这段时间没有取用技能。": "No skills used in this period.",
  "最耗 token 的会话": "Top sessions by tokens",
  "（已删除的会话）": "(deleted session)",
  "这段时间没有会话。": "No sessions in this period.",
  "代码模式": "Code mode",
  "内置": "Built-in",
};
