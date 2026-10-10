/**
 * 主进程与渲染层之间的契约。
 *
 * 渲染层只能通过这里列出的通道说话——preload 里不暴露任何通用的
 * invoke 逃生门，否则 contextIsolation 就白开了。
 *
 * 这是 .cts：preload 在 sandbox:true 下必须是 CommonJS，主进程是 ESM，
 * 运行时常量放 CJS 两边都能 import。
 */

export const IPC = {
  configRead: "config:read",
  configWrite: "config:write",
  purgeAll: "data:purge",

  runtimeStart: "runtime:start",
  runtimeStop: "runtime:stop",
  runtimeStatus: "runtime:status",

  sessionWork: "session:work",
  goalSet: "goal:set",
  goalUpdate: "goal:update",
  sessionFork: "session:fork",
  sessionRecover: "session:recover",
  changesList: "changes:list",
  changesUndo: "changes:undo",
  sessionStart: "session:start",
  sessionResume: "session:resume",
  sessionSend: "session:send",
  sessionInterrupt: "session:interrupt",
  sessionList: "session:list",
  sessionSearch: "session:search",
  sessionWorkspace: "session:workspace",
  sessionRename: "session:rename",
  sessionDelete: "session:delete",
  sessionArchive: "session:archive",
  sessionArchived: "session:archived",
  sessionConfigure: "session:configure",

  providerList: "provider:list",
  providerCreate: "provider:create",
  providerUpdate: "provider:update",
  providerDelete: "provider:delete",
  providerModels: "provider:models",
  providerAutoMark: "provider:autoMark",

  searchList: "search:list",
  searchCreate: "search:create",
  searchUpdate: "search:update",
  searchDelete: "search:delete",
  searchTest: "search:test",

  pluginList: "plugin:list",
  pluginInstall: "plugin:install",
  pluginToggle: "plugin:toggle",
  pluginDelete: "plugin:delete",
  pluginConfig: "plugin:config",
  pluginSetConfig: "plugin:setConfig",
  pluginContributions: "plugin:contributions",
  channelStatus: "channel:status",
  channelBindings: "channel:bindings",
  channelAuthorize: "channel:authorize",
  channelRevoke: "channel:revoke",
  connectionCreate: "connection:create",
  connectionRename: "connection:rename",
  connectionDelete: "connection:delete",
  emailTest: "plugin:emailTest",
  voicePermission: "voice:permission",
  scheduleList: "schedule:list",
  scheduleSave: "schedule:save",
  scheduleDelete: "schedule:delete",
  scheduleToggle: "schedule:toggle",
  scheduleRunNow: "schedule:runNow",
  voiceTranscribe: "voice:transcribe",
  wechatLoginStart: "wechat:loginStart",
  wechatLoginPoll: "wechat:loginPoll",

  // 当前版本号。单独一个通道：检查更新要等网络，而版本号侧边栏一开就要显示。
  appVersion: "app:version",
  browserBridgeStatus: "browserBridge:status",
  browserBridgeRepair: "browserBridge:repair",
  browserBridgeReveal: "browserBridge:reveal",
  browserBridgeOpenPage: "browserBridge:openPage",
  // 写系统剪贴板。走主进程而不是渲染层的 navigator.clipboard：后者要求窗口
  // 在前台，不满足时它会失败，而 execCommand 兜底会静默失败——按钮上写着「已复制」，
  // 剪贴板里却什么都没有。
  clipboardWrite: "clipboard:write",
  updateCheck: "update:check",
  updateInstall: "update:install",
  updatePrepare: "update:prepare",

  mcpOAuthLogin: "mcp:oauthLogin",
  mcpOAuthStatus: "mcp:oauthStatus",
  mcpOAuthLogout: "mcp:oauthLogout",
  mcpRead: "mcp:read",
  mcpWrite: "mcp:write",
  mcpProbe: "mcp:probe",
  diagnosticsRead: "diagnostics:read",
  workspaceReveal: "workspace:reveal",
  fileOpen: "file:open",
  /** 读一个图片/音频文件，回 data URL，界面内联显示用。 */
  fileMedia: "file:media",
  /** 把贴进来的音频落到磁盘，回路径。 */
  audioStage: "audio:stage",

  skillList: "skill:list",
  skillToggle: "skill:toggle",
  skillDelete: "skill:delete",
  skillOpenDir: "skill:openDir",

  groupRead: "group:read",
  groupCreate: "group:create",
  groupRename: "group:rename",
  groupCollapse: "group:collapse",
  groupDelete: "group:delete",
  sessionAssign: "session:assign",

  profileList: "profile:list",

  approvalRespond: "approval:respond",
  approvalPending: "approval:pending",
  questionRespond: "question:respond",
  questionPending: "question:pending",
  usageSummary: "usage:summary",
  pickDirectory: "dialog:pickDirectory",

  /** 主进程 → 渲染层的推送通道。 */
  onAgentEvent: "push:agentEvent",
  onApproval: "push:approval",
  onRuntimeStatus: "push:runtimeStatus",
  onBrowserBridge: "push:browserBridge",
  onQuestion: "push:question",
  /** 定时任务列表变了（建、改、删、跑完）。 */
  onSchedules: "push:schedules",
  /** 要渲染层打开某个会话（点了定时任务的系统通知）。 */
  onOpenSession: "push:openSession",
  onUpdateProgress: "push:updateProgress",
} as const;
