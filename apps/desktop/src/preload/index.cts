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
    start: (input?: unknown) => ipcRenderer.invoke(IPC.sessionStart, input),
    resume: (sessionId: string) => ipcRenderer.invoke(IPC.sessionResume, sessionId),
    send: (input: unknown) => ipcRenderer.invoke(IPC.sessionSend, input),
    interrupt: (sessionId: string) => ipcRenderer.invoke(IPC.sessionInterrupt, sessionId),
    list: () => ipcRenderer.invoke(IPC.sessionList),
    search: (keyword: string) => ipcRenderer.invoke(IPC.sessionSearch, keyword),
    setWorkspace: (input: unknown) => ipcRenderer.invoke(IPC.sessionWorkspace, input),
    remove: (sessionId: string) => ipcRenderer.invoke(IPC.sessionDelete, sessionId),
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
    config: (uuid: string) => ipcRenderer.invoke(IPC.pluginConfig, uuid),
    setConfig: (input: unknown) => ipcRenderer.invoke(IPC.pluginSetConfig, input),
    contributions: () => ipcRenderer.invoke(IPC.pluginContributions),
  },
  channels: {
    status: () => ipcRenderer.invoke(IPC.channelStatus),
    bindings: () => ipcRenderer.invoke(IPC.channelBindings),
    authorize: (input: unknown) => ipcRenderer.invoke(IPC.channelAuthorize, input),
    revoke: (input: unknown) => ipcRenderer.invoke(IPC.channelRevoke, input),
  },
  wechat: {
    loginStart: () => ipcRenderer.invoke(IPC.wechatLoginStart),
    loginPoll: (input: unknown) => ipcRenderer.invoke(IPC.wechatLoginPoll, input),
  },
  mcp: {
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
  },
  profiles: {
    list: () => ipcRenderer.invoke(IPC.profileList),
  },
  diagnostics: {
    read: () => ipcRenderer.invoke(IPC.diagnosticsRead),
  },
  files: {
    /** 打开模型在对话里提到的一个路径。主进程会校验，渲染层不碰文件系统。 */
    open: (path: string) => ipcRenderer.invoke(IPC.fileOpen, path),
    /** 读一个图片/音频文件用于内联显示。校验同样在主进程。 */
    media: (path: string) => ipcRenderer.invoke(IPC.fileMedia, path),
  },
  audio: {
    /** 把一段音频落到磁盘，返回路径；发消息时把路径交给内核转写。 */
    stage: (input: unknown) => ipcRenderer.invoke(IPC.audioStage, input),
  },
  update: {
    check: () => ipcRenderer.invoke(IPC.updateCheck),
    install: () => ipcRenderer.invoke(IPC.updateInstall),
    prepare: () => ipcRenderer.invoke(IPC.updatePrepare),
  },
  approval: {
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
