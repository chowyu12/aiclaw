import { app, BrowserWindow, clipboard, dialog, ipcMain, nativeImage, Notification, shell, systemPreferences } from "electron";
import { join, dirname } from "node:path";
import { existsSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import type { PendingApproval, PendingUserInput } from "@aiclaw/agent-client";
import { ConfigStore, type McpServer } from "./config.js";
import { SkillManager } from "./skills.js";
import { SessionManager } from "./session.js";
import { Scheduler } from "./scheduler.js";
import { formatWhen, type ScheduledTaskInput } from "../shared/schedule.js";
import { Updater } from "./updater.js";
import { DiagnosticsLog, buildReport } from "./diagnostics.js";
import { LogFile } from "./logfile.js";
import { openFromChat } from "./open-file.js";
import { readMedia, stageAudio } from "./media-files.js";
import { IPC } from "../shared/ipc.cjs";
import type { ApprovalPayload, QuestionAnswer, QuestionPayload } from "../shared/types.js";

const here = dirname(fileURLToPath(import.meta.url));

// 必须在第一次取 userData 之前设置：Electron 默认拿 package.json 的 name，
// 带 scope 的话数据目录会变成 "@aiclaw/desktop" 这种带斜杠的两级目录。
app.setName("aiclaw");

/**
 * 应用图标。由 `scripts/make-icon.cjs` 从品牌标识生成，提交在仓库里。
 *
 * 不设的话 dock 里挂的是 Electron 自带的原子图标——那是「这是个还没做完的
 * demo」最直接的信号。macOS 上要走 dock.setIcon：窗口的 icon 选项在 macOS
 * 上不生效，那条路只管 Windows 与 Linux。
 *
 * 打包之后系统用的是 app bundle 里的图标（由打包配置指定），这段主要管开发
 * 期运行；两边都设不冲突。文件丢了就跳过——图标不该成为启不起来的理由。
 */
const iconPath = join(here, "../../assets/icon.png");

function applyAppIcon(): void {
  if (!existsSync(iconPath)) return;
  const image = nativeImage.createFromPath(iconPath);
  if (image.isEmpty()) return;
  app.dock?.setIcon(image);
}

const store = new ConfigStore();
const skills = new SkillManager(store);
// SessionManager 要问技能：会话启动时把当前启用的技能目录下发给内核。
const sessions = new SessionManager(store, skills);

/** 定时任务开的会话归进侧边栏的这个内置分组。 */
const SCHEDULED_GROUP = "__scheduled__";

const scheduler = new Scheduler(store.dataDir, {
  async runTask(task) {
    const title = `⏰ ${task.name} · ${formatWhen(new Date())}`;
    const text = `（这是定时任务「${task.name}」自动发起的，用户此刻可能不在电脑前。）\n\n${task.prompt}`;
    const sessionId = await sessions.runBackgroundSession({ workspace: task.workspace, title, text });
    const groups = store.readGroups();
    store.writeGroups({ assignments: { ...groups.assignments, [sessionId]: SCHEDULED_GROUP } });
    return sessionId;
  },
  notify(task, ok, detail) {
    if (!Notification.isSupported()) return;
    const notification = new Notification({
      title: ok ? `定时任务「${task.name}」完成了` : `定时任务「${task.name}」没做完`,
      body: ok ? "点这里查看结果" : detail.slice(0, 120) || "点这里查看",
    });
    notification.on("click", () => {
      if (mainWindow) {
        if (mainWindow.isMinimized()) mainWindow.restore();
        mainWindow.show();
        mainWindow.focus();
      }
      const target = scheduler.list().find((item) => item.id === task.id)?.lastSessionId;
      if (target) push(IPC.onOpenSession, target);
    });
    notification.show();
  },
});
scheduler.on("changed", (tasks) => push(IPC.onSchedules, tasks));
const updater = new Updater();
// 内核 stderr 与宿主自己的关键事件都进这里，攒一小段供「诊断」页复制。
const diagnostics = new DiagnosticsLog();
// 内存那份给「现在出了什么事」，文件那份给「昨天那次」——用户第二天才提起时，
// 内存里的早冲没了，应用可能还重启过。按天滚动，只留 7 天。
const logFile = new LogFile(store.homeDir);
sessions.on("log", (source: string, chunk: string) => {
  diagnostics.push(source, chunk);
  logFile.append(source, chunk);
});

let mainWindow: BrowserWindow | null = null;
/** 待回应的审批。渲染层只拿到 id，真正的回应句柄留在主进程。 */
const pendingApprovals = new Map<string, PendingApproval>();
/** 同一批审批推给渲染层的样子。渲染进程重新加载后按它补发（见 approvalPending）。 */
const pendingPayloads = new Map<string, ApprovalPayload>();
/** 模型提的、用户还没回答的问题（ask_user）。与审批同一套：回应句柄留在主进程。 */
const pendingQuestions = new Map<string, { question: PendingUserInput; payload: QuestionPayload }>();

function logApp(line: string): void {
  diagnostics.push("app", line);
  logFile.append("app", line);
}

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1180,
    height: 800,
    minWidth: 860,
    minHeight: 600,
    title: "AIClaw",
    backgroundColor: "#f4f7f4",
    // macOS 忽略这个字段（那边走 app.dock.setIcon），Windows 与 Linux 看它。
    icon: existsSync(iconPath) ? iconPath : undefined,
    webPreferences: {
      preload: join(here, "../preload/index.cjs"),
      // 三个都不能松：渲染层跑的是会展示模型输出的页面，
      // 一旦模型输出能拿到 Node 能力，审批就绕过去了——而审批是仅剩的闸。
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  // 外链一律交给系统浏览器。应用内不做通用导航。
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    void shell.openExternal(url);
    return { action: "deny" };
  });
  mainWindow.webContents.on("will-navigate", (event, url) => {
    const devServer = process.env.VITE_DEV_SERVER_URL;
    if (devServer && url.startsWith(devServer)) return;
    event.preventDefault();
    void shell.openExternal(url);
  });

  const window = mainWindow;
  const load = (): void => {
    const devServer = process.env.VITE_DEV_SERVER_URL;
    if (devServer) {
      void window.loadURL(devServer);
    } else {
      void window.loadFile(join(here, "../renderer/index.html"));
    }
  };
  load();

  // 渲染进程没了就重新加载页面。**实际会发生**：电脑休眠几天，系统把渲染进程
  // 回收掉，Electron 不会自己重建，窗口就一直白着，只能退出重开。内核与会话
  // 都在主进程这边活着，重新加载之后渲染层照常 runtime.start()（内核已在跑就
  // 直接返回）、重新拉会话列表与待回应的审批，接着用。
  // 一分钟内连着没了三次就不再拉：那是页面自己起不来，拉了也是白转圈。
  const gone: number[] = [];
  window.webContents.on("render-process-gone", (_event, details) => {
    logApp(`渲染进程退出：${details.reason}（exitCode ${details.exitCode}）`);
    if (details.reason === "clean-exit" || window.isDestroyed()) return;
    const now = Date.now();
    while (gone.length > 0 && now - gone[0]! > 60_000) gone.shift();
    if (gone.length >= 3) {
      logApp("渲染进程一分钟内退出了 3 次，不再自动重新加载");
      return;
    }
    gone.push(now);
    // **不能在这个回调里同步 load()**：那会在 Chromium 还没拆完旧渲染进程时给
    // webContents 重复挂观察者，撞上它的断言（Observers can only be added once!），
    // 整个主进程连同内核一起崩掉——实测过。推到回调之外再加载。
    setTimeout(() => {
      if (!window.isDestroyed()) load();
    }, 500);
  });
  mainWindow.on("closed", () => {
    mainWindow = null;
  });
}

function push(channel: string, payload: unknown): void {
  mainWindow?.webContents.send(channel, payload);
}

function registerIpc(): void {
  ipcMain.handle(IPC.configRead, () => store.readConfig());
  // 改配置不用重启运行时：模型、审批档位这些都是按会话下发的。
  ipcMain.handle(IPC.configWrite, async (_event, patch: Record<string, unknown>) => {
    const result = await store.writeConfig(patch);
    // 通道会话（微信、企业微信）是内核自己建的，角色配置要推过去才用得上。
    if ("roles" in patch) await sessions.syncChannelMedia();
    if ("browser" in patch || "browserBackend" in patch) await sessions.syncBrowserBridge();
    return result;
  });
  ipcMain.handle(IPC.appVersion, () => app.getVersion());
  ipcMain.handle(IPC.browserBridgeStatus, () => sessions.browserBridge());
  ipcMain.handle(IPC.browserBridgeRepair, () => sessions.regeneratePairToken());
  ipcMain.handle(IPC.browserBridgeOpenPage, (_event, browserId: unknown) =>
    sessions.openExtensionPage(typeof browserId === "string" ? browserId : ""),
  );
  ipcMain.handle(IPC.browserBridgeReveal, async () => {
    const dir = sessions.browserBridge().extensionDir;
    const failure = await shell.openPath(dir);
    if (failure) throw new Error(`打不开扩展目录 ${dir}：${failure}`);
    return dir;
  });
  ipcMain.handle(IPC.clipboardWrite, (_event, text: unknown, html?: unknown) => {
    // 只收字符串，而且有上限：渲染层展示的是模型输出，不该借这个口子往剪贴板里
    // 塞任意大小的东西。一段回答再长也到不了这个数。
    if (typeof text !== "string") throw new Error("只能复制文本");
    if (text.length > 2_000_000) throw new Error("内容太长，没有复制");
    // 同时放一份 HTML：粘进 Word、飞书、邮件这类富文本应用时保留标题、列表、表格；
    // 粘进纯文本的地方拿到的仍是 Markdown 原文（与 Codex 0.154 同一个改进）。
    if (typeof html === "string" && html && html.length <= 4_000_000) {
      clipboard.write({ text, html });
    } else {
      clipboard.writeText(text);
    }
    return true;
  });
  ipcMain.handle(IPC.updateCheck, () => updater.check());
  // 下载进度往渲染层推：没有进度的话，用户点完「升级」看到的是一个不动的
  // 按钮，几十秒后应用突然退出——那不是慢，是没有反馈，但感觉比慢更糟。
  ipcMain.handle(IPC.updatePrepare, () =>
    updater.prepare((received, total) => push(IPC.onUpdateProgress, { received, total })),
  );
  ipcMain.handle(IPC.updateInstall, () =>
    updater.install((received, total) => push(IPC.onUpdateProgress, { received, total })),
  );
  ipcMain.handle(IPC.purgeAll, async () => {
    await sessions.stop();
    store.purgeAll();
  });

  ipcMain.handle(IPC.runtimeStart, () => sessions.start());
  ipcMain.handle(IPC.runtimeStop, () => sessions.stop());
  ipcMain.handle(IPC.runtimeStatus, () => ({ state: sessions.running ? "ready" : "stopped" }));

  ipcMain.handle(IPC.sessionStart, (_event, input?: { workspace?: string }) =>
    sessions.startSession(input?.workspace),
  );
  ipcMain.handle(IPC.sessionResume, (_event, sessionId: string) => sessions.resumeSession(sessionId));
  ipcMain.handle(IPC.sessionSend, (_event, input) => sessions.sendTurn(input));
  ipcMain.handle(IPC.sessionInterrupt, (_event, sessionId: string) => sessions.interrupt(sessionId));
  ipcMain.handle(IPC.sessionList, async () => {
    const list = await sessions.listSessions();
    // 顺手清掉指向已删会话的分组归属，别让分组里留下看不见的幽灵成员。
    // 归档的会话还在，只是不在列表里：它们的归属要留着，恢复时回到原来的分组。
    const archived = await sessions.listArchived().catch(() => []);
    store.pruneGroupAssignments([...list, ...archived].map((session) => session.id));
    return list;
  });
  ipcMain.handle(IPC.sessionArchive, (_event, input: { sessionId: string; archived: boolean }) =>
    sessions.archiveSession(String(input.sessionId), input.archived === true),
  );
  ipcMain.handle(IPC.sessionArchived, () => sessions.listArchived());
  ipcMain.handle(IPC.sessionSearch, (_event, keyword: string) => sessions.searchSessions(keyword));
  ipcMain.handle(
    IPC.sessionWorkspace,
    (_event, input: { sessionId: string; workspace: string }) =>
      sessions.setWorkspace(input.sessionId, input.workspace),
  );
  ipcMain.handle(IPC.sessionDelete, (_event, sessionId: string) => sessions.deleteSession(sessionId));
  ipcMain.handle(
    IPC.sessionConfigure,
    (_event, input: { sessionId: string; providerId: number; model: string; contextWindow: number }) =>
      sessions.configureSession(input.sessionId, input.providerId, input.model, input.contextWindow),
  );

  ipcMain.handle(IPC.providerList, () => sessions.listProviders());
  ipcMain.handle(IPC.providerCreate, (_event, params) => sessions.createProvider(params));
  ipcMain.handle(IPC.providerUpdate, (_event, params) => sessions.updateProvider(params));
  ipcMain.handle(IPC.providerDelete, (_event, id: number) => sessions.deleteProvider(id));
  ipcMain.handle(IPC.providerModels, (_event, id: number) => sessions.fetchProviderModels(id));
  ipcMain.handle(IPC.providerAutoMark, (_event, id: number) => sessions.autoMarkProvider(id));

  ipcMain.handle(IPC.searchList, () => sessions.listSearchEngines());
  ipcMain.handle(IPC.searchCreate, (_event, params) => sessions.createSearchEngine(params));
  ipcMain.handle(IPC.searchUpdate, (_event, params) => sessions.updateSearchEngine(params));
  ipcMain.handle(IPC.searchDelete, (_event, id: number) => sessions.deleteSearchEngine(id));
  ipcMain.handle(IPC.searchTest, (_event, input: { id: number; query: string }) =>
    sessions.testSearchEngine(input.id, input.query),
  );

  ipcMain.handle(IPC.pluginList, () => sessions.listPlugins());
  ipcMain.handle(IPC.pluginInstall, (_event, path: string) => sessions.installPlugin(path));
  ipcMain.handle(IPC.pluginToggle, (_event, input: { uuid: string; enabled: boolean }) =>
    sessions.togglePlugin(input.uuid, input.enabled),
  );
  ipcMain.handle(IPC.pluginDelete, (_event, uuid: string) => sessions.deletePlugin(uuid));
  // 渠道插件的配置按连接存：带上 connectionId。
  ipcMain.handle(IPC.pluginConfig, (_event, input: string | { uuid: string; connectionId?: string }) =>
    typeof input === "string" ? sessions.pluginConfig(input) : sessions.pluginConfig(input.uuid, input.connectionId ?? ""),
  );
  ipcMain.handle(
    IPC.pluginSetConfig,
    (_event, input: { uuid: string; key: string; value: string; connectionId?: string }) =>
      sessions.setPluginConfig(input.uuid, input.key, input.value, input.connectionId ?? ""),
  );
  ipcMain.handle(IPC.connectionCreate, (_event, input: { pluginUuid: string; name?: string }) =>
    sessions.connectionCreate(input.pluginUuid, input.name ?? ""),
  );
  ipcMain.handle(IPC.connectionRename, (_event, input: { uuid: string; name: string }) =>
    sessions.connectionRename(input.uuid, input.name),
  );
  ipcMain.handle(IPC.connectionDelete, (_event, uuid: string) => sessions.connectionDelete(uuid));
  ipcMain.handle(IPC.pluginContributions, () => sessions.pluginContributions());
  ipcMain.handle(IPC.channelStatus, () => sessions.channelStatus());
  ipcMain.handle(IPC.channelBindings, () => sessions.channelBindings());
  ipcMain.handle(IPC.channelAuthorize, (_event, params) => sessions.authorizeChannel(params));
  ipcMain.handle(IPC.channelRevoke, (_event, key) => sessions.revokeChannel(key));
  ipcMain.handle(IPC.emailTest, (_event, uuid: string) => sessions.emailTest(uuid));
  // 麦克风授权：macOS 要应用自己去问一次，系统才会弹框；拒绝过的只能去系统设置里开。
  ipcMain.handle(IPC.voicePermission, async () => {
    if (process.platform !== "darwin") return "granted";
    const status = systemPreferences.getMediaAccessStatus("microphone");
    if (status === "not-determined") {
      return (await systemPreferences.askForMediaAccess("microphone")) ? "granted" : "denied";
    }
    return status === "granted" ? "granted" : "denied";
  });
  ipcMain.handle(IPC.scheduleList, () => scheduler.list());
  ipcMain.handle(IPC.scheduleSave, (_event, input: ScheduledTaskInput) => scheduler.save(input));
  ipcMain.handle(IPC.scheduleDelete, (_event, id: string) => scheduler.remove(String(id)));
  ipcMain.handle(IPC.scheduleToggle, (_event, input: { id: string; enabled: boolean }) =>
    scheduler.setEnabled(String(input.id), input.enabled === true),
  );
  ipcMain.handle(IPC.scheduleRunNow, (_event, id: string) => scheduler.runNow(String(id)));
  ipcMain.handle(IPC.voiceTranscribe, (_event, wav: Uint8Array) => sessions.transcribeVoice(new Uint8Array(wav)));
  ipcMain.handle(IPC.wechatLoginStart, () => sessions.wechatLoginStart());
  ipcMain.handle(IPC.wechatLoginPoll, (_event, input: { uuid: string; token: string; connectionId?: string }) =>
    sessions.wechatLoginPoll(input.uuid, input.token, input.connectionId ?? ""),
  );

  ipcMain.handle(IPC.mcpRead, () => store.readMcpServers());
  ipcMain.handle(IPC.mcpWrite, (_event, servers: McpServer[]) => store.writeMcpServers(servers));
  ipcMain.handle(IPC.mcpProbe, (_event, server: McpServer) => sessions.probeMcp(server));

  // 对话里点一个文件名就打开它。路径来自模型输出，所以校验全在主进程做：
  // 基准是当前会话的工作区，可执行的那几类只「在访达里显示」，凭据目录拒绝。
  ipcMain.handle(IPC.fileOpen, (_event, path: string) =>
    openFromChat(path, {
      base: sessions.currentWorkspace(),
      protectedPaths: [store.dataDir],
    }),
  );
  // 内联显示产出物：路径同样来自模型输出，校验与打开走同一条线。
  ipcMain.handle(IPC.fileMedia, (_event, path: string) =>
    readMedia(path, { base: sessions.currentWorkspace(), protectedPaths: [store.dataDir] }),
  );
  ipcMain.handle(IPC.audioStage, (_event, input: { name: string; data: string }) =>
    stageAudio(input.name, input.data),
  );

  ipcMain.handle(IPC.diagnosticsRead, () => {
    const config = store.readConfig();
    return buildReport(
      {
        version: app.getVersion(),
        electron: process.versions.electron ?? "",
        node: process.versions.node ?? "",
        platform: process.platform,
        arch: process.arch,
        paths: {
          日志: logFile.directory,
          应用数据: app.getPath("userData"),
          "技能与记忆": store.homeDir,
          模型配置库: store.appDbPath,
          // 工作区是按会话来的，这里给的是当前那个会话的。
          当前会话工作区: sessions.currentWorkspace() || "（未设置）",
        },
        runtime: {
          状态: sessions.running ? "ready" : "stopped",
          // 只给 id：名字与端点在库里，Key 更不能出现在这儿。
          模型服务: config.providerId ? `#${config.providerId}` : "（未选）",
          模型: config.model,
          审批档位: config.profile,
        },
        mounts: sessions.lastMounts(),
      },
      diagnostics.all(),
    );
  });

  ipcMain.handle(IPC.skillList, () => skills.list());
  ipcMain.handle(IPC.skillToggle, (_event, input: { id: string; enabled: boolean }) =>
    skills.setEnabled(input.id, input.enabled),
  );
  ipcMain.handle(IPC.skillDelete, (_event, id: string) => skills.remove(id));
  ipcMain.handle(IPC.skillOpenDir, async () => {
    // 建出来再打开：目录不存在时 openPath 会静默失败，用户以为点了没反应。
    mkdirSync(skills.dir, { recursive: true });
    await shell.openPath(skills.dir);
    return skills.dir;
  });
  ipcMain.handle(IPC.groupRead, () => store.readGroups());
  ipcMain.handle(IPC.groupCollapse, (_event, input: { groupId: string; collapsed: boolean }) =>
    store.setGroupCollapsed(String(input.groupId), input.collapsed === true),
  );
  ipcMain.handle(IPC.groupCreate, (_event, name: string) => {
    const current = store.readGroups();
    const group = { id: `g_${Date.now().toString(36)}`, name: name.trim() || "新分组" };
    return store.writeGroups({ groups: [...current.groups, group], assignments: current.assignments });
  });
  ipcMain.handle(IPC.groupRename, (_event, input: { groupId: string; name: string }) => {
    const current = store.readGroups();
    return store.writeGroups({
      groups: current.groups.map((group) =>
        group.id === input.groupId ? { ...group, name: input.name.trim() || group.name } : group,
      ),
      assignments: current.assignments,
    });
  });
  ipcMain.handle(IPC.groupDelete, (_event, groupId: string) => {
    const current = store.readGroups();
    // 删分组不删会话：成员回到「未分组」。反过来做用户会丢对话记录。
    const assignments = Object.fromEntries(
      Object.entries(current.assignments).filter(([, id]) => id !== groupId),
    );
    return store.writeGroups({
      groups: current.groups.filter((group) => group.id !== groupId),
      assignments,
    });
  });
  ipcMain.handle(
    IPC.sessionAssign,
    (_event, input: { sessionId: string; groupId: string | null }) => {
      const current = store.readGroups();
      const assignments = { ...current.assignments };
      if (input.groupId) assignments[input.sessionId] = input.groupId;
      else delete assignments[input.sessionId];
      return store.writeGroups({ groups: current.groups, assignments });
    },
  );

  ipcMain.handle(IPC.profileList, () => SessionManager.profiles());

  ipcMain.handle(
    IPC.approvalRespond,
    (_event, payload: { id: string; approved: boolean; scope?: "once" | "session" }) => {
      const request = pendingApprovals.get(payload.id);
      if (!request) {
        // 审批已经超时或那一轮已被中断。静默返回而不是抛错：
        // 用户点了一下没反应比弹一个看不懂的错误好。
        return false;
      }
      pendingApprovals.delete(payload.id);
      pendingPayloads.delete(payload.id);
      sessions.approve(request, payload.approved, payload.scope);
      return true;
    },
  );

  ipcMain.handle(IPC.approvalPending, () => [...pendingPayloads.values()]);
  ipcMain.handle(IPC.questionPending, () => [...pendingQuestions.values()].map((entry) => entry.payload));
  ipcMain.handle(IPC.questionRespond, (_event, payload: { id: string; answer: QuestionAnswer }) => {
    const entry = pendingQuestions.get(payload.id);
    // 这一轮已经结束或被中断：内核那边不等了，静默返回。
    if (!entry) return false;
    pendingQuestions.delete(payload.id);
    const answer = payload.answer ?? {};
    entry.question.respond({
      selected: Array.isArray(answer.selected) ? answer.selected.filter((item) => typeof item === "string").slice(0, 10) : [],
      text: typeof answer.text === "string" ? answer.text.slice(0, 4000) : "",
      skipped: answer.skipped === true,
    });
    return true;
  });
  ipcMain.handle(IPC.usageSummary, (_event, days: unknown) =>
    sessions.usageSummary(typeof days === "number" && days > 0 ? Math.min(Math.trunc(days), 366) : 30),
  );

  ipcMain.handle(IPC.pickDirectory, async () => {
    if (!mainWindow) return null;
    const result = await dialog.showOpenDialog(mainWindow, {
      properties: ["openDirectory", "createDirectory"],
      title: "选择 Agent 的工作目录",
    });
    return result.canceled ? null : (result.filePaths[0] ?? null);
  });
}

// 模型经 schedule_* 工具建、列、删定时任务。
sessions.on("schedule", ({ request, respond, fail }) => {
  try {
    respond({ text: scheduler.handleModelRequest(request) });
  } catch (error) {
    fail(error instanceof Error ? error.message : String(error));
  }
});

sessions.on("event", (method, params) => {
  if (method === "turn/completed") {
    const done = params as { sessionId?: string; error?: string } | undefined;
    if (done?.sessionId) scheduler.onTurnCompleted(done.sessionId, done.error);
  }
  // 一轮结束（完成、失败、中断）时，它还没回应的审批就作废了：内核那边已经
  // 不等了。不清的话渲染层重新加载后会把它们当成待办再弹一遍。
  if (method === "turn/completed") {
    const sessionId = (params as { sessionId?: string } | undefined)?.sessionId;
    for (const [id, request] of pendingApprovals) {
      if (request.sessionId === sessionId) {
        pendingApprovals.delete(id);
        pendingPayloads.delete(id);
      }
    }
    for (const [id, entry] of pendingQuestions) {
      if (entry.payload.sessionId === sessionId) pendingQuestions.delete(id);
    }
  }
  push(IPC.onAgentEvent, { method, params });
});

sessions.on("approval", (request) => {
  const key = String(request.id);
  pendingApprovals.set(key, request);
  const payload: ApprovalPayload = {
    id: key,
    sessionId: request.sessionId,
    kind: request.kind,
    title: request.title,
    detail: request.detail,
    cwd: request.cwd,
    reason: request.reason,
    scopePath: request.scopePath,
  };
  pendingPayloads.set(key, payload);
  push(IPC.onApproval, payload);
});

sessions.on("browserBridge", (view) => push(IPC.onBrowserBridge, view));

sessions.on("userInput", (question) => {
  const payload: QuestionPayload = {
    id: String(question.id),
    sessionId: question.request.sessionId,
    turnId: question.request.turnId,
    question: question.request.question,
    options: question.request.options ?? [],
    multiSelect: question.request.multiSelect === true,
    at: Date.now(),
  };
  pendingQuestions.set(payload.id, { question, payload });
  push(IPC.onQuestion, payload);
});

sessions.on("status", (state, detail) => {
  logApp(`运行时 ${state}${detail ? `：${detail}` : ""}`);
  // 运行时停了，所有待回应的审批都作废。
  if (state !== "ready" && state !== "starting") {
    pendingApprovals.clear();
    pendingPayloads.clear();
    pendingQuestions.clear();
  }
  push(IPC.onRuntimeStatus, { state, detail });
});

// 单实例：两个实例会同时写同一个会话目录，互相踩。
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.focus();
    }
  });

  void app.whenReady().then(() => {
    applyAppIcon();
    // 技能与记忆从 userData 搬到 ~/.aiclaw。只搬一次，目标已存在就跳过。
    store.migrateHomeData();
    store.migrateSkills();
    registerIpc();
    createWindow();
    void sessions.syncBrowserBridge();
    scheduler.start();
    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  app.on("window-all-closed", () => {
    if (process.platform !== "darwin") app.quit();
  });

  app.on("before-quit", () => {
    // 让 claw-agent 有机会落盘会话再退出。
    scheduler.stop();
    void sessions.stop();
  });
}
