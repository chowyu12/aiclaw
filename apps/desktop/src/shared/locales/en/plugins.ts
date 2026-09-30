/** 英文词典：plugins 这一组界面（中文原文 → 英文）。 */
export const plugins: Record<string, string> = {
  // ---------- 插件页：权限与可放开的工具 ----------
  "操作屏幕（截屏、鼠标、键盘）": "Control the screen (screenshots, mouse, keyboard)",
  "读文件": "Read files",
  "写文件": "Write files",
  "访问网络": "Access the network",
  "接收外部消息": "Receive external messages",
  "向外部发消息": "Send external messages",
  "读取自己的秘密配置": "Read its own secret settings",
  "执行命令": "Run command",
  "改文件": "Edit files",
  "写 Word": "Write Word",
  "改 Word": "Edit Word",
  "写 Excel": "Write Excel",
  "写 PPT": "Write PowerPoint",

  // ---------- 内置插件的清单文字（plugin.json） ----------
  "邮件": "Email",
  "微信": "WeChat",
  "企业微信": "WeCom",
  "截屏观察当前屏幕，并合成鼠标与键盘输入。启用后模型能看见并操作整个屏幕——不只是工作目录；除截屏外每个动作都会请你确认。":
    "Takes screenshots to observe the screen and synthesizes mouse and keyboard input. Once enabled, the model can see and control the entire screen — not just the working directory. Every action except screenshots asks for your confirmation.",
  "让助手用你的邮箱收发邮件：列信、读信、存附件、发新信、回复。看信不问你；每次发信、回信都会先把收件人和内容摆出来请你确认。":
    "Lets the assistant send and receive email with your account: list and read messages, save attachments, send new mail, and reply. Reading doesn't ask; every send or reply shows you the recipients and content for confirmation first.",
  "通过 iLink 中继接入个人微信：扫码登录后，私聊消息会触发一次助手回答。中继为第三方服务，可用性与账号风险由使用者自行承担。":
    "Connects a personal WeChat account through the iLink relay: after you sign in by scanning a QR code, each direct message triggers a reply from the assistant. The relay is a third-party service; you bear the availability and account risks.",
  "接入企业微信智能机器人：群聊或单聊里的消息会触发一次助手回答，回复以流式返回。":
    "Connects a WeCom smart bot: messages in group or direct chats trigger a reply from the assistant, streamed back as it's written.",
  "企业微信智能机器人的 bot_id": "The WeCom smart bot's bot_id",
  "机器人密钥，仅保存在本机数据库": "Bot secret, stored only in the local database",

  // ---------- 插件页：邮件 ----------
  "邮箱地址": "Email address",
  "授权码": "App password (authorization code)",
  "不是登录密码": "Not your login password",
  "发件人名字": "Sender name",
  "留空只显示地址": "Leave blank to show only the address",
  "登录名": "Username",
  "留空用邮箱地址": "Leave blank to use the email address",
  "收信服务器（IMAP）": "Incoming server (IMAP)",
  "留空自动识别": "Leave blank to detect automatically",
  "收信端口": "Incoming port",
  "发信服务器（SMTP）": "Outgoing server (SMTP)",
  "发信端口": "Outgoing port",
  "465（587 走 STARTTLS）": "465 (587 for STARTTLS)",
  "邮箱地址和授权码都要填": "Enter both the email address and the app password.",
  "正在连接邮箱…": "Connecting to your mailbox…",
  "连不上": "Can't connect",
  "收信 {imap} · 发信 {smtp}": "Incoming {imap} · Outgoing {smtp}",
  "已连上（{servers}）": "Connected ({servers})",
  "已连上并启用（{servers}）。助手现在就能收发邮件了。":
    "Connected and enabled ({servers}). The assistant can now send and receive email.",
  "邮箱": "Email account",
  "先填好邮箱地址和授权码，点「测试并启用」。": "Enter your email address and app password, then click “Test and enable”.",
  "QQ、163、126 邮箱：在网页版「设置」里开启 IMAP/SMTP 服务，按提示生成授权码填在这里。Gmail、iCloud、Outlook：生成「应用专用密码」。公司邮箱（网易、腾讯、阿里企业邮等）会按域名自动找到服务器，开了安全登录的填「客户端专用密码」。授权码只保存在本机，加密存放。":
    "QQ, 163 and 126 Mail: turn on the IMAP/SMTP service under Settings in the web version, generate an authorization code as prompted, and enter it here. Gmail, iCloud, Outlook: generate an app-specific password. Company mail (NetEase, Tencent, Alibaba enterprise mail, etc.) finds its servers from your domain; if secure login is on, use a client-specific password. The password is stored encrypted, only on this computer.",
  "高级：服务器与端口（一般不用填）": "Advanced: servers and ports (usually not needed)",
  "正在连接…": "Connecting…",
  "保存并测试": "Save and test",
  "测试并启用": "Test and enable",
  "助手能列信、读信（读过的标成已读）、把附件存进工作区、发新信、回信。":
    "The assistant can list and read messages (marking them as read), save attachments to the workspace, send new mail, and reply.",
  "每次发信、回信都会先请你确认": "Every send or reply asks you to confirm",
  "收件人与内容；信里的内容一律当作外部资料，不当指令。只在你自己的对话里可用，微信、企业微信那边来的会话碰不到你的邮箱。":
    " the recipients and content first. Message contents are always treated as external material, never as instructions. Only available in your own conversations — conversations coming from WeChat or WeCom can't touch your mailbox.",

  // ---------- 插件页：卡片与配置 ----------
  "插件": "Plugins",
  "一个插件是一个带": "A plugin is a folder with a",
  "的目录，声明它要的权限、配置项，以及它带来的东西：技能、MCP server、宿主能力（computer use）、通道（微信、企业微信）。装好是停用的——":
    "file that declares the permissions and settings it needs, plus what it brings: skills, MCP servers, host capabilities (computer use), and channels (WeChat, WeCom). Plugins are installed disabled — ",
  "启用那一步才把声明的权限交出去": "the declared permissions are granted only when you enable one",
  "。": ".",
  "+ 从目录安装": "+ Install from folder",
  "内置": "Built-in",
  "本地": "Local",
  "启用": "Enabled",
  "删除": "Delete",
  "（没有说明）": "(No description)",
  "启用后获得的权限": "Permissions granted when enabled",
  "不需要任何权限。": "Needs no permissions.",
  "启用后模型能": "Once enabled, the model can ",
  "看见并操作整个屏幕": "see and control the entire screen",
  "，不只是工作目录——包括别的应用、系统设置、以及本应用自己的窗口。除截屏外每个动作都会请你确认；最前面的应用是 AIClaw 自己时直接拒绝。macOS 还要在「隐私与安全性」里给屏幕录制与辅助功能授权，且模型得看得懂图。这是这里权限最大的一项，不用就关掉。":
    ", not just the working directory — including other apps, system settings, and this app's own windows. Every action except screenshots asks for your confirmation, and actions are refused outright while AIClaw itself is the frontmost app. On macOS you also need to grant Screen Recording and Accessibility in “Privacy & Security”, and the model must be able to understand images. This is the most powerful permission here — turn it off when you're not using it.",
  "宿主能力": "Host capability",
  "通道": "Channel",
  "{n} 个技能": "{n} skills",
  "{n} 个 MCP": "{n} MCP servers",
  "缺 {keys}": "Missing {keys}",
  "配置": "Settings",
  "先填好标着「必填」的几项，再启用。": "Fill in the fields marked “Required”, then enable it.",
  "必填": "Required",
  "已配置": "Configured",
  "留空表示不修改": "Leave blank to keep the current value",
  "填入后只保存在本机": "Stored only on this computer",
  "保存": "Save",
  "清掉": "Clear",
  "还没填邮箱": "No email address yet",
  "已配置邮箱": "Email configured",
  "删除插件「{name}」？它带的技能、MCP server 与通道授权一起删除。":
    "Delete plugin “{name}”? Its skills, MCP servers and channel authorizations will be deleted with it.",
  "保存中…": "Saving…",
  "读取中…": "Loading…",

  // ---------- 插件页：连接 ----------
  "连接": "Connections",
  "还没有连接": "No connections yet",
  "{total} 个连接 · {running} 个已连接": "{total} connections · {running} connected",
  "{total} 个连接": "{total} connections",
  "还没登录": "Not signed in",
  "插件停用中": "Plugin disabled",
  "未启动": "Not running",
  "连接中": "Connecting",
  "已连接": "Connected",
  "重连中": "Reconnecting",
  "连接失败": "Connection failed",
  "已停止": "Stopped",
  "+ 添加微信号（扫码）": "+ Add WeChat account (scan QR code)",
  "+ 添加机器人": "+ Add bot",
  "每个连接各自一套凭据、各自在线；同一个人找不同的连接，是不同的会话。":
    "Each connection has its own credentials and stays online on its own; the same person messaging different connections gets separate conversations.",
  "登录走第三方中继（iLink），可用性与账号风险由你自己承担。":
    "Sign-in goes through a third-party relay (iLink); you bear the availability and account risks.",
  "微信登录二维码": "WeChat sign-in QR code",
  "还没有连接。点「添加微信号」用微信扫码登录。": "No connections yet. Click “Add WeChat account” and scan the QR code with WeChat to sign in.",
  "还没有连接。点「添加机器人」填入企业微信智能机器人的 bot_id 与 secret。":
    "No connections yet. Click “Add bot” and enter your WeCom smart bot's bot_id and secret.",
  "双击改名": "Double-click to rename",
  "改名": "Rename",
  "重新扫码": "Scan again",
  "收起凭据": "Hide credentials",
  "凭据": "Credentials",
  "删除这个连接": "Delete this connection",
  "删除连接「{name}」？它的凭据和放行记录一起删掉，已有的会话留着。":
    "Delete connection “{name}”? Its credentials and approvals will be deleted too; existing conversations are kept.",

  // ---------- 插件页：微信扫码 ----------
  "正在向中继要二维码…": "Requesting a QR code from the relay…",
  "用微信扫一扫": "Scan with WeChat",
  "已重新登录，连接用新凭据重连。": "Signed in again. The connection is reconnecting with the new credentials.",
  "已登录，加好了一个微信号。插件启用着的话它马上就连上。":
    "Signed in and added a WeChat account. If the plugin is enabled, it connects right away.",
  "二维码已过期，点「重新获取」。": "The QR code has expired. Request a new one.",
  "已扫码，请在手机上确认": "Scanned. Confirm on your phone.",

  // ---------- 插件页：外部会话的放行 ----------
  "还没有人通过这个连接发过消息。": "No one has messaged through this connection yet.",
  "已放行": "Approved",
  "待放行": "Awaiting approval",
  "最近 {time}": "Last message {time}",
  "选模型…": "Choose a model…",
  "更新": "Update",
  "放行": "Approve",
  "收回": "Revoke",
  "放行前先给这个会话选一个模型。": "Choose a model for this conversation before approving it.",
  "外部会话（群或单聊）第一次发消息只会被记下，":
    "The first message from an external conversation (group or direct chat) is only recorded and ",
  "不会": "does not",
  "触发回答；你放行之后它才能用。放行时选模型、选它能动的工具——默认只有只读工具，因为发消息的人不是你。":
    " trigger a reply; the conversation works only after you approve it. When approving, choose a model and the tools it may use — only read-only tools by default, since the sender isn't you.",

  // ---------- MCP 页 ----------
  "删除 MCP server「{label}」？下一个会话起它的工具就不再挂载。":
    "Delete MCP server “{label}”? Its tools won't be mounted from the next conversation on.",
  "检测中…": "Checking…",
  "{n} 个工具": "{n} tools",
  "自定义 MCP server": "Custom MCP servers",
  "挂第三方 MCP server 进来，它的工具会和内置工具一起交给模型。本地的用 stdio（拉起一个进程），远程的用 HTTP。改动立刻在当前会话重挂一遍，新会话也带上。":
    "Add third-party MCP servers; their tools are given to the model alongside the built-in ones. Use stdio for local servers (it starts a process) and HTTP for remote ones. Changes are remounted in the current conversation right away and apply to new conversations too.",
  "第三方工具默认按": "Third-party tools are treated as ",
  "有副作用": "having side effects",
  "处理，每次调用都会请你确认；只有 server 自己声明了只读（":
    " by default, so every call asks for your confirmation; only tools the server itself declares read-only (",
  "）的工具才不问。本版本没有沙箱，不确定的时候宁可多问。":
    ") skip the prompt. This version has no sandbox, so when in doubt it asks.",
  "+ 本地（stdio）": "+ Local (stdio)",
  "+ 远程（HTTP）": "+ Remote (HTTP)",
  "启用中的插件另外带了": "Enabled plugins also provide",
  "——那些在「插件」页管，这里列的是你自己配的。":
    "— those are managed on the Plugins page; the ones listed here are your own.",
  "还没有配置。常见的本地 server 形如": "Nothing configured yet. A typical local server looks like",
  "未命名": "Untitled",
  "重新检测": "Check again",
  "正在连接并拉取工具清单…": "Connecting and fetching the tool list…",
  "连不上：{error}": "Can't connect: {error}",
  "连上了，但这个 server 没有暴露任何工具。": "Connected, but this server exposes no tools.",
  "只读 · 不问": "Read-only · no prompt",
  "要确认": "Needs confirmation",
  "名字（也是工具名前缀）": "Name (also the tool name prefix)",
  "命令": "Command",
  "参数": "Arguments",
  "环境变量": "Environment variables",
  "KEY=value，一行一条": "KEY=value, one per line",
  "地址": "URL",
  "请求头": "Headers",
  "Authorization=Bearer xxx，一行一条": "Authorization=Bearer xxx, one per line",
  "走 MCP 的 Streamable HTTP 传输（2025-03-26）。只支持 SSE 的旧版传输不支持。":
    "Uses MCP's Streamable HTTP transport (2025-03-26). The older SSE-only transport is not supported.",

  // ---------- 技能页 ----------
  "通用": "Universal",
  "项目": "Project",
  "npm 全局": "npm global",
  "插件 · {name}": "Plugin · {name}",
  "删除技能「{name}」？目录会从本机移除，不可恢复。":
    "Delete skill “{name}”? Its folder will be removed from this computer. This can't be undone.",
  "技能": "Skills",
  "一个技能就是一个目录，里面放": "A skill is a folder containing a",
  "：开头写 name 与 description，下面写清楚什么时候用、怎么做。模型只在系统提示词里看到名字与用途，判断用得上时才把正文取出来——所以 description 要写清楚":
    " file: a name and description at the top, then when to use it and how. The model sees only the name and purpose in the system prompt and loads the body when it judges it useful — so the description should make clear ",
  "什么时候用": "when to use it",
  "AIClaw 的技能目录是": "AIClaw's skills folder is",
  "——": " — ",
  "装的就在这里，Codex、Cursor、Gemini CLI 等也读它，装一次各处都能用。这里还会一并列出 Claude Code（":
    "installs skills here, and Codex, Cursor, Gemini CLI and others read it too, so one install works everywhere. Also listed here: Claude Code (",
  "）、Codex（": "), Codex (",
  "）、当前工作目录的": "), the current working directory's",
  "、npm 全局包里的技能，以及启用中的插件带来的——它们是同一种格式，没必要在这里再装一遍。别处的技能可以关掉但删不了，去它自己的位置删（插件带的随插件停用一起消失）。同名时按这个顺序取第一个：通用 > 插件 > 项目 > Claude Code > Codex > npm。":
    ", skills in global npm packages, and those from enabled plugins — they use the same format, so there's no need to install them again here. Skills from elsewhere can be turned off but not deleted here; delete them where they live (plugin skills go away when the plugin is disabled). When names clash, the first in this order wins: Universal > Plugins > Project > Claude Code > Codex > npm.",
  "打开技能目录": "Open skills folder",
  "还没有技能。点「打开技能目录」，在里面建一个子目录放 SKILL.md。":
    "No skills yet. Click “Open skills folder” and create a subfolder containing a SKILL.md.",
  "{n} 个": "{n} total",
  "· {n} 个启用": "· {n} enabled",
  "只能关，不能删": "Can be turned off, not deleted",
  "（没写 description——模型无从判断什么时候该用它）": "(No description — the model can't tell when to use it)",
};
