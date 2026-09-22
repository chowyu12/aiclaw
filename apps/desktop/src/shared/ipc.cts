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

  sessionStart: "session:start",
  sessionResume: "session:resume",
  sessionSend: "session:send",
  sessionInterrupt: "session:interrupt",
  sessionList: "session:list",
  sessionSearch: "session:search",
  sessionWorkspace: "session:workspace",
  sessionDelete: "session:delete",
  sessionConfigure: "session:configure",

  providerList: "provider:list",
  providerCreate: "provider:create",
  providerUpdate: "provider:update",
  providerDelete: "provider:delete",
  providerModels: "provider:models",

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
  wechatLoginStart: "wechat:loginStart",
  wechatLoginPoll: "wechat:loginPoll",

  updateCheck: "update:check",
  updateInstall: "update:install",
  updatePrepare: "update:prepare",

  mcpRead: "mcp:read",
  mcpWrite: "mcp:write",
  mcpProbe: "mcp:probe",
  diagnosticsRead: "diagnostics:read",
  fileOpen: "file:open",

  skillList: "skill:list",
  skillToggle: "skill:toggle",
  skillDelete: "skill:delete",
  skillOpenDir: "skill:openDir",

  groupRead: "group:read",
  groupCreate: "group:create",
  groupRename: "group:rename",
  groupDelete: "group:delete",
  sessionAssign: "session:assign",

  profileList: "profile:list",

  approvalRespond: "approval:respond",
  pickDirectory: "dialog:pickDirectory",

  /** 主进程 → 渲染层的推送通道。 */
  onAgentEvent: "push:agentEvent",
  onApproval: "push:approval",
  onRuntimeStatus: "push:runtimeStatus",
  onUpdateProgress: "push:updateProgress",
} as const;
