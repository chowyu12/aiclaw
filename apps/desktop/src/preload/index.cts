import { contextBridge, ipcRenderer } from "electron";
import { IPC } from "../shared/ipc.cjs";

/**
 * 只暴露具名方法，不暴露通用的 invoke。
 *
 * 渲染层会展示模型输出，把任意通道的调用能力交给它等于让
 * 审批形同虚设——没有沙箱之后审批是唯一的闸，所以这里每加一个方法都要想清楚。
 */
const api = {
  config: {
    read: () => ipcRenderer.invoke(IPC.configRead),
    write: (patch: unknown) => ipcRenderer.invoke(IPC.configWrite, patch),
  },
  data: {
    purgeAll: () => ipcRenderer.invoke(IPC.purgeAll),
  },
  runtime: {
    start: () => ipcRenderer.invoke(IPC.runtimeStart),
    stop: () => ipcRenderer.invoke(IPC.runtimeStop),
    status: () => ipcRenderer.invoke(IPC.runtimeStatus),
  },
  session: {
    work: (id: string) => ipcRenderer.invoke(IPC.sessionWork, id),
    setGoal: (id: string, goal: unknown) => ipcRenderer.invoke(IPC.goalSet, id, goal),
    updateGoal: (id: string, update: unknown) => ipcRenderer.invoke(IPC.goalUpdate, id, update),
    fork: (id: string, itemId?: string) => ipcRenderer.invoke(IPC.sessionFork, id, itemId),
    recover: (id: string, requestId: string, action: "resume" | "dismiss") => ipcRenderer.invoke(IPC.sessionRecover, id, requestId, action),
    changes: (id: string) => ipcRenderer.invoke(IPC.changesList, id),
    undo: (id: string, changeId: string) => ipcRenderer.invoke(IPC.changesUndo, id, changeId),
    start: (input?: unknown) => ipcRenderer.invoke(IPC.sessionStart, input),
    resume: (sessionId: string) => ipcRenderer.invoke(IPC.sessionResume, sessionId),
    send: (input: unknown) => ipcRenderer.invoke(IPC.sessionSend, input),
    interrupt: (sessionId: string) => ipcRenderer.invoke(IPC.sessionInterrupt, sessionId),
    list: () => ipcRenderer.invoke(IPC.sessionList),
    search: (keyword: string) => ipcRenderer.invoke(IPC.sessionSearch, keyword),
    setWorkspace: (input: unknown) => ipcRenderer.invoke(IPC.sessionWorkspace, input),
    rename: (sessionId: string, title: string) => ipcRenderer.invoke(IPC.sessionRename, { sessionId, title }),
    remove: (sessionId: string) => ipcRenderer.invoke(IPC.sessionDelete, sessionId),
    /** 归档（archived=false 为恢复），连同它开出的子 agent。 */
    archive: (sessionId: string, archived: boolean) => ipcRenderer.invoke(IPC.sessionArchive, { sessionId, archived }),
    archived: () => ipcRenderer.invoke(IPC.sessionArchived),
    configure: (input: unknown) => ipcRenderer.invoke(IPC.sessionConfigure, input),
    assign: (input: unknown) => ipcRenderer.invoke(IPC.sessionAssign, input),
  },
  providers: {
    list: () => ipcRenderer.invoke(IPC.providerList),
    create: (params: unknown) => ipcRenderer.invoke(IPC.providerCreate, params),
    update: (params: unknown) => ipcRenderer.invoke(IPC.providerUpdate, params),
    remove: (id: number) => ipcRenderer.invoke(IPC.providerDelete, id),
    /** 到端点拉模型名。不落库。 */
    models: (id: number) => ipcRenderer.invoke(IPC.providerModels, id),
    /** 按 models.dev 自动标记能力。只加不减。 */
    autoMark: (id: number) => ipcRenderer.invoke(IPC.providerAutoMark, id),
  },
  search: {
    list: () => ipcRenderer.invoke(IPC.searchList),
    create: (params: unknown) => ipcRenderer.invoke(IPC.searchCreate, params),
    update: (params: unknown) => ipcRenderer.invoke(IPC.searchUpdate, params),
    remove: (id: number) => ipcRenderer.invoke(IPC.searchDelete, id),
    /** 用一个引擎真搜一次。 */
    test: (input: unknown) => ipcRenderer.invoke(IPC.searchTest, input),
  },
  plugins: {
    list: () => ipcRenderer.invoke(IPC.pluginList),
    /** 从一个目录装插件。目录由 dialog.pickDirectory 选出来。 */
    install: (path: string) => ipcRenderer.invoke(IPC.pluginInstall, path),
    toggle: (input: unknown) => ipcRenderer.invoke(IPC.pluginToggle, input),
    remove: (uuid: string) => ipcRenderer.invoke(IPC.pluginDelete, uuid),
    /** 渠道插件的配置按连接存，给 connectionId。 */
    config: (uuid: string, connectionId = "") => ipcRenderer.invoke(IPC.pluginConfig, { uuid, connectionId }),
    setConfig: (input: unknown) => ipcRenderer.invoke(IPC.pluginSetConfig, input),
    contributions: () => ipcRenderer.invoke(IPC.pluginContributions),
    /** 邮件插件：用已存的配置试着登录收信、发信服务器。 */
    testEmail: (uuid: string) => ipcRenderer.invoke(IPC.emailTest, uuid),
  },
  channels: {
    status: () => ipcRenderer.invoke(IPC.channelStatus),
    bindings: () => ipcRenderer.invoke(IPC.channelBindings),
    authorize: (input: unknown) => ipcRenderer.invoke(IPC.channelAuthorize, input),
    revoke: (input: unknown) => ipcRenderer.invoke(IPC.channelRevoke, input),
    /** 渠道插件的连接：一个微信号、一个企微机器人一个。 */
    createConnection: (pluginUuid: string, name = "") => ipcRenderer.invoke(IPC.connectionCreate, { pluginUuid, name }),
    renameConnection: (uuid: string, name: string) => ipcRenderer.invoke(IPC.connectionRename, { uuid, name }),
    deleteConnection: (uuid: string) => ipcRenderer.invoke(IPC.connectionDelete, uuid),
  },
  schedules: {
    list: () => ipcRenderer.invoke(IPC.scheduleList),
    save: (input: unknown) => ipcRenderer.invoke(IPC.scheduleSave, input),
    remove: (id: string) => ipcRenderer.invoke(IPC.scheduleDelete, id),
    toggle: (id: string, enabled: boolean) => ipcRenderer.invoke(IPC.scheduleToggle, { id, enabled }),
    runNow: (id: string) => ipcRenderer.invoke(IPC.scheduleRunNow, id),
  },
  voice: {
    /** 麦克风授权：granted / denied。macOS 第一次会弹系统授权框。 */
    permission: () => ipcRenderer.invoke(IPC.voicePermission),
    /** 录好的 WAV 交给听写模型，返回文字。 */
    transcribe: (wav: Uint8Array) => ipcRenderer.invoke(IPC.voiceTranscribe, wav),
  },
  wechat: {
    loginStart: () => ipcRenderer.invoke(IPC.wechatLoginStart),
    loginPoll: (input: unknown) => ipcRenderer.invoke(IPC.wechatLoginPoll, input),
  },
  mcp: {
 login: (input: unknown) => ipcRenderer.invoke(IPC.mcpOAuthLogin, input),
 status: (url: string) => ipcRenderer.invoke(IPC.mcpOAuthStatus, url),
 logout: (url: string) => ipcRenderer.invoke(IPC.mcpOAuthLogout, url),
    read: () => ipcRenderer.invoke(IPC.mcpRead),
    write: (servers: unknown) => ipcRenderer.invoke(IPC.mcpWrite, servers),
    probe: (server: unknown) => ipcRenderer.invoke(IPC.mcpProbe, server),
  },
  skills: {
    list: () => ipcRenderer.invoke(IPC.skillList),
    toggle: (input: unknown) => ipcRenderer.invoke(IPC.skillToggle, input),
    remove: (id: string) => ipcRenderer.invoke(IPC.skillDelete, id),
    openDir: () => ipcRenderer.invoke(IPC.skillOpenDir),
  },
  groups: {
    read: () => ipcRenderer.invoke(IPC.groupRead),
    create: (name: string) => ipcRenderer.invoke(IPC.groupCreate, name),
    rename: (input: unknown) => ipcRenderer.invoke(IPC.groupRename, input),
    remove: (groupId: string) => ipcRenderer.invoke(IPC.groupDelete, groupId),
    /** 折叠 / 展开一个分组（同时记下「看过了」）。 */
    collapse: (groupId: string, collapsed: boolean) => ipcRenderer.invoke(IPC.groupCollapse, { groupId, collapsed }),
  },
  profiles: {
    list: () => ipcRenderer.invoke(IPC.profileList),
  },
  diagnostics: {
    read: () => ipcRenderer.invoke(IPC.diagnosticsRead),
  },
  files: {
    /** 打开模型在对话里提到的一个路径。主进程会校验，渲染层不碰文件系统。 */
    revealWorkspace: (path: string) => ipcRenderer.invoke(IPC.workspaceReveal, path),
    open: (path: string) => ipcRenderer.invoke(IPC.fileOpen, path),
    /** 读一个图片/音频文件用于内联显示。校验同样在主进程。 */
    media: (path: string) => ipcRenderer.invoke(IPC.fileMedia, path),
  },
  audio: {
    /** 把一段音频落到磁盘，返回路径；发消息时把路径交给内核转写。 */
    stage: (input: unknown) => ipcRenderer.invoke(IPC.audioStage, input),
  },
  clipboard: {
    /** html 可选：给了就同时放一份富文本，粘进 Word、飞书之类的应用时保留格式。 */
    write: (text: string, html?: string) => ipcRenderer.invoke(IPC.clipboardWrite, text, html),
  },
  update: {
    version: () => ipcRenderer.invoke(IPC.appVersion),
    check: () => ipcRenderer.invoke(IPC.updateCheck),
    install: () => ipcRenderer.invoke(IPC.updateInstall),
    prepare: () => ipcRenderer.invoke(IPC.updatePrepare),
  },
  /** 模型提的问题（ask_user）：回答、跳过，以及渲染进程重新加载后拉回还没回答的。 */
  question: {
    respond: (id: string, answer: unknown) => ipcRenderer.invoke(IPC.questionRespond, { id, answer }),
    pending: () => ipcRenderer.invoke(IPC.questionPending),
  },
  /** 用量（设置 → 用量）。 */
  usage: {
    summary: (days: number) => ipcRenderer.invoke(IPC.usageSummary, days),
  },
  /** 与用户浏览器里「AIClaw 浏览器助手」扩展的连接（设置 → 浏览器）。 */
  browserBridge: {
    status: () => ipcRenderer.invoke(IPC.browserBridgeStatus),
    /** 换一个配对码，返回新码。 */
    repair: () => ipcRenderer.invoke(IPC.browserBridgeRepair),
    /** 在访达 / 资源管理器里打开扩展目录。 */
    reveal: () => ipcRenderer.invoke(IPC.browserBridgeReveal),
    /** 用某个浏览器打开它的扩展管理页，同时显示扩展目录、把路径放进剪贴板。 */
    openPage: (browserId: string) => ipcRenderer.invoke(IPC.browserBridgeOpenPage, browserId),
  },
  approval: {
    /** 还在等回应的审批。渲染进程重新加载后用它补回来，推送只会来一次。 */
    pending: () => ipcRenderer.invoke(IPC.approvalPending),
    respond: (id: string, approved: boolean, scope?: "once" | "session") =>
      ipcRenderer.invoke(IPC.approvalRespond, { id, approved, scope }),
  },
  dialog: {
    pickDirectory: () => ipcRenderer.invoke(IPC.pickDirectory),
  },
  on: {
    agentEvent: (handler: (payload: unknown) => void) => {
      const listener = (_event: unknown, payload: unknown) => handler(payload);
      ipcRenderer.on(IPC.onAgentEvent, listener);
      return () => ipcRenderer.off(IPC.onAgentEvent, listener);
    },
    approval: (handler: (payload: unknown) => void) => {
      const listener = (_event: unknown, payload: unknown) => handler(payload);
      ipcRenderer.on(IPC.onApproval, listener);
      return () => ipcRenderer.off(IPC.onApproval, listener);
    },
    question: (handler: (payload: unknown) => void) => {
      const listener = (_event: unknown, payload: unknown) => handler(payload);
      ipcRenderer.on(IPC.onQuestion, listener);
      return () => ipcRenderer.off(IPC.onQuestion, listener);
    },
    browserBridge: (handler: (payload: unknown) => void) => {
      const listener = (_event: unknown, payload: unknown) => handler(payload);
      ipcRenderer.on(IPC.onBrowserBridge, listener);
      return () => ipcRenderer.off(IPC.onBrowserBridge, listener);
    },
    schedules: (handler: (payload: unknown) => void) => {
      const listener = (_event: unknown, payload: unknown) => handler(payload);
      ipcRenderer.on(IPC.onSchedules, listener);
      return () => ipcRenderer.off(IPC.onSchedules, listener);
    },
    openSession: (handler: (payload: unknown) => void) => {
      const listener = (_event: unknown, payload: unknown) => handler(payload);
      ipcRenderer.on(IPC.onOpenSession, listener);
      return () => ipcRenderer.off(IPC.onOpenSession, listener);
    },
    runtimeStatus: (handler: (payload: unknown) => void) => {
      const listener = (_event: unknown, payload: unknown) => handler(payload);
      ipcRenderer.on(IPC.onRuntimeStatus, listener);
      return () => ipcRenderer.off(IPC.onRuntimeStatus, listener);
    },
    updateProgress: (handler: (payload: unknown) => void) => {
      const listener = (_event: unknown, payload: unknown) => handler(payload);
      ipcRenderer.on(IPC.onUpdateProgress, listener);
      return () => ipcRenderer.off(IPC.onUpdateProgress, listener);
    },
  },
};

contextBridge.exposeInMainWorld("aiclaw", api);

export type AiclawApi = typeof api;
