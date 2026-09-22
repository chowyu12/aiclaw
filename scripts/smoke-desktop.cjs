/**
 * 桌面宿主冒烟：验 preload 真的注入了、渲染层真的起得来。
 *
 * 为什么单独有这个：agent 那条冒烟走的是 agent-client → claw-agent，
 * 完全不碰 Electron。于是 preload 在 sandbox:true 下加载失败这种事——
 * 类型检查过、构建过、agent 冒烟全绿——照样能一路漏到用户手里，
 * 表现是界面一片空白。这个脚本堵的就是那一段。
 *
 * 跑法：npx electron scripts/smoke-desktop.cjs
 * 需要有图形会话（Linux CI 上套 xvfb-run）。
 */
const { app, BrowserWindow, ipcMain, nativeImage } = require("electron");
const path = require("path");

const DESKTOP = path.join(__dirname, "..", "apps", "desktop");
const PRELOAD = path.join(DESKTOP, "dist", "preload", "index.cjs");
const PAGE = path.join(DESKTOP, "dist", "renderer", "index.html");
// 主进程这边是普通 Node 环境，相对 require 正常——受限的只有 sandbox 下的 preload。
const { IPC } = require(path.join(DESKTOP, "dist", "shared", "ipc.cjs"));

// preload 必须暴露的命名空间。渲染层每一处 window.aiclaw.X 都要在这里有对应，
// 少一个就是某个页面会在运行时炸。
const REQUIRED = [
  "config",
  "data",
  "runtime",
  "session",
  "profiles",
  "groups",
  "providers",
  "plugins",
  "channels",
  "wechat",
  "mcp",
  "skills",
  "approval",
  "dialog",
  "on",
];

const results = [];
function check(name, ok, detail = "") {
  results.push({ name, ok, detail });
}

// 用独立的 userData：真实应用有单实例锁，共用目录会让这个脚本
// 在开发者正开着应用时直接退出，看起来像"通过了"。
app.setName("aiclaw-smoke");
app.setPath("userData", path.join(app.getPath("temp"), "aiclaw-smoke"));

app.whenReady().then(async () => {
  const preloadErrors = [];
  const consoleErrors = [];

  // 用假数据应答渲染层启动时会问的那几个通道。不接真实主进程是刻意的：
  // 这里要验的是"桥接通了、界面起得来"，接上真实 ConfigStore 会连带碰用户的
  // 本机配置和钥匙串。但通道名取自真实的 IPC 常量，改名了这里会一起报错。
  ipcMain.handle(IPC.configRead, () => ({
    providerId: 0,
    model: "",
    reasoningEffort: "medium",
    contextWindow: 0,
    workdir: "/tmp/smoke",
    profile: "on-write",
    retentionDays: 30,
  }));
  // 应用一启动就拉运行时、列会话、读模型服务；这里都给空的。
  ipcMain.handle(IPC.runtimeStart, () => undefined);
  ipcMain.handle(IPC.sessionList, () => []);
  ipcMain.handle(IPC.providerList, () => []);
  ipcMain.handle(IPC.profileList, () => [
    { id: "on-write", label: "默认", description: "执行命令与外部工具需确认" },
  ]);
  ipcMain.handle(IPC.groupRead, () => ({ groups: [], assignments: {} }));
  ipcMain.handle(IPC.mcpRead, () => []);
  ipcMain.handle(IPC.skillList, () => []);

  const win = new BrowserWindow({
    show: false,
    webPreferences: {
      preload: PRELOAD,
      // 必须与 apps/desktop/src/main/index.ts 里的一致，否则这个冒烟
      // 验的就不是真实配置下的行为了。
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  win.webContents.on("preload-error", (_event, _preloadPath, error) => {
    preloadErrors.push(error && error.message ? error.message : String(error));
  });
  win.webContents.on("console-message", (event) => {
    if (event.level === "error") consoleErrors.push(event.message);
  });

  try {
    await win.loadFile(PAGE);
  } catch (error) {
    check("渲染层加载", false, String(error));
    return finish();
  }
  check("渲染层加载", true, path.relative(process.cwd(), PAGE));

  check(
    "preload 无加载错误",
    preloadErrors.length === 0,
    preloadErrors.join("；") || "无",
  );

  const bridge = await win.webContents.executeJavaScript(
    "({ type: typeof window.aiclaw, keys: window.aiclaw ? Object.keys(window.aiclaw) : [] })",
  );
  check("window.aiclaw 已注入", bridge.type === "object", bridge.type);

  const missing = REQUIRED.filter((key) => !bridge.keys.includes(key));
  check(
    "命名空间齐全",
    missing.length === 0,
    missing.length ? `缺：${missing.join("、")}` : `${bridge.keys.length} 个`,
  );

  // 渲染层挂载了就说明 Vue 没在启动阶段抛。
  const mounted = await win.webContents.executeJavaScript(
    "!!document.querySelector('.shell')",
  );
  check("Vue 应用已挂载", mounted === true, mounted ? ".shell" : "根节点为空");

  // 这一条是全文重点：配置页曾经因为 preload 没加载而整页空白，而上面每一项
  // 单独看都"没报错"。所以直接断言落地的那一页真的渲染出了东西。
  // 一个模型服务都没有时应用落到「模型服务」页（那里有「添加」按钮），
  // 有的话落到配置页（那里是一组 label）——两种都算渲染出来了。
  const fields = await waitFor(
    win,
    "document.querySelectorAll('.settings label, .page .add button').length",
    (count) => typeof count === "number" && count > 0,
  );
  check(
    "配置页渲染出字段",
    typeof fields === "number" && fields > 0,
    fields ? `${fields} 个` : "一个都没有——配置页是空的",
  );

  const blocked = await win.webContents.executeJavaScript(
    "document.querySelector('.error-bar') ? document.querySelector('.error-bar').innerText : ''",
  );
  check("没有启动期错误条", !blocked, blocked || "无");

  // 应用图标：主进程按 dist/main 的相对路径去找它，路径写错的表现是 dock 里
  // 挂着 Electron 自带的原子图标，而**没有任何报错**——和 preload 那一类
  // 故障一模一样，所以一并钉在这里。
  const ICON = path.join(DESKTOP, "dist", "main", "..", "..", "assets", "icon.png");
  const icon = nativeImage.createFromPath(ICON);
  const size = icon.isEmpty() ? null : icon.getSize();
  check(
    "应用图标可加载",
    !icon.isEmpty() && size.width >= 512 && size.width === size.height,
    size ? `${size.width}x${size.height}` : "读不出来——dock 里会是 Electron 原子图标",
  );

  finish();

  // waitFor 轮询一个页面内表达式直到满足条件。bootstrap 是异步的，
  // 加载完成那一刻界面还没拿到配置，直接断言会稳定地假失败。
  async function waitFor(target, expression, predicate, timeoutMs = 5000) {
    const deadline = Date.now() + timeoutMs;
    let last;
    while (Date.now() < deadline) {
      last = await target.webContents.executeJavaScript(expression);
      if (predicate(last)) return last;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    return last;
  }

  function finish() {
    for (const { name, ok, detail } of results) {
      console.log(`  ${ok ? "ok " : "FAIL"} ${name}${detail ? ` — ${detail}` : ""}`);
    }
    const passed = results.filter((r) => r.ok).length;
    console.log(`${passed}/${results.length} 通过`);
    app.exit(passed === results.length ? 0 : 1);
  }
});
