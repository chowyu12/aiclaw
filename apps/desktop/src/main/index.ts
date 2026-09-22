import { app, BrowserWindow, dialog, ipcMain, nativeImage, shell } from "electron";
import { join, dirname } from "node:path";
import { existsSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import type { PendingApproval } from "@aiclaw/agent-client";
import { ConfigStore, type McpServer } from "./config.js";
import { SkillManager } from "./skills.js";
import { SessionManager } from "./session.js";
import { Updater } from "./updater.js";
import { DiagnosticsLog, buildReport } from "./diagnostics.js";
import { LogFile } from "./logfile.js";
import { openFromChat } from "./open-file.js";
import { IPC } from "../shared/ipc.cjs";
import type { ApprovalPayload } from "../shared/types.js";

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

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1180,
    height: 800,
    minWidth: 860,
    minHeight: 600,
    title: "AIClaw",
    backgroundColor: "#f5f7f7",
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

  const devServer = process.env.VITE_DEV_SERVER_URL;
  if (devServer) {
    void mainWindow.loadURL(devServer);
  } else {
    void mainWindow.loadFile(join(here, "../renderer/index.html"));
  }
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
  ipcMain.handle(IPC.configWrite, (_event, patch: Record<string, unknown>) => store.writeConfig(patch));
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
    store.pruneGroupAssignments(list.map((session) => session.id));
    return list;
  });
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
          computer_use: config.enableComputerUse ? "已开启" : "关闭",
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
      sessions.approve(request, payload.approved, payload.scope);
      return true;
    },
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

sessions.on("event", (method, params) => push(IPC.onAgentEvent, { method, params }));

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
  push(IPC.onApproval, payload);
});

sessions.on("status", (state, detail) => {
  const line = `运行时 ${state}${detail ? `：${detail}` : ""}`;
  diagnostics.push("app", line);
  logFile.append("app", line);
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
    registerIpc();
    createWindow();
    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  app.on("window-all-closed", () => {
    if (process.platform !== "darwin") app.quit();
  });

  app.on("before-quit", () => {
    // 让 claw-agent 有机会落盘会话再退出。
    void sessions.stop();
  });
}
