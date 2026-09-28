/**
 * 「用我的浏览器」端到端：真的浏览器 + 真的扩展 + 真的 AgentBrowser。
 *
 * 由 scripts/e2e-browser-extension.sh 另起一个**临时配置**的 Chrome / Edge（不碰用户
 * 自己的配置与登录），加载 apps/browser-extension；这里经远程调试口把配对码写进扩展，然后用
 * 编译好的 AgentBrowser（「extension」后端）对一个本机测试页走一遍：打开 → 快照 →
 * 填字提交 → 抽正文 → 截图 → 列标签页，并核对 agent 的标签页是在后台开的。
 *
 * 跑法：scripts/e2e-browser-extension.sh [浏览器可执行文件]
 * 不进 CI：要一个装好的 Chromium 系浏览器与图形会话。发版前手动跑。
 */
const { app } = require("electron");
const http = require("node:http");
const fs = require("node:fs");
const path = require("node:path");

const REPO = path.join(__dirname, "..");
const DIST = path.join(REPO, "apps", "desktop", "dist", "main");
const EXTENSION = path.join(REPO, "apps", "browser-extension");
const TOKEN = "e2e-" + Math.random().toString(36).slice(2);
const DEBUG_PORT = 9339;

const results = [];
function check(name, ok, detail = "") {
  results.push({ name, ok, detail });
  console.log(`${ok ? "  ok " : "  FAIL"} ${name}${detail ? " — " + detail : ""}`);
}

const PAGE = `<!doctype html><html><head><meta charset="utf-8"><title>E2E 表单</title></head><body>
<h1>搜索测试</h1>
<form id="f" action="/result" method="get">
  <input name="q" placeholder="关键词" />
  <button type="submit">搜索</button>
</form>
</body></html>`;

function serve() {
  return new Promise((resolve) => {
    const server = http.createServer((request, response) => {
      const url = new URL(request.url, "http://x");
      response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
      if (url.pathname === "/result") {
        response.end(`<!doctype html><title>结果</title><p id="r">你搜的是：${url.searchParams.get("q")}</p>`);
      } else {
        response.end(PAGE);
      }
    });
    server.listen(0, "127.0.0.1", () => resolve(server));
  });
}

async function json(url) {
  const response = await fetch(url);
  return response.json();
}

/** 经远程调试口，在扩展自己的页面里执行一段脚本（写配对码、读扩展记的状态）。 */
async function inExtensionPage(expression) {
  const target = await (
    await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/new?chrome-extension://doofgbncfflbmekanpfocimbideeiadd/popup.html`, {
      method: "PUT",
    })
  ).json();
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = reject;
  });
  let nextId = 1;
  const pending = new Map();
  ws.onmessage = (event) => {
    const message = JSON.parse(event.data);
    pending.get(message.id)?.(message);
  };
  const evaluate = (code) =>
    new Promise((resolve) => {
      const id = nextId++;
      pending.set(id, resolve);
      ws.send(JSON.stringify({ id, method: "Runtime.evaluate", params: { expression: code, awaitPromise: true, returnByValue: true } }));
    });
  try {
    // 新开的页面要等它加载完：之前执行的话 chrome.storage 还不存在，脚本悄悄失败。
    await until("扩展页面加载", async () => {
      const ready = await evaluate("typeof chrome !== 'undefined' && !!chrome.storage && document.readyState === 'complete'");
      return ready.result?.result?.value === true;
    }, 10_000);
    const message = await evaluate(expression);
    if (message.result?.exceptionDetails) {
      throw new Error("扩展页面里的脚本出错：" + JSON.stringify(message.result.exceptionDetails).slice(0, 300));
    }
    return message.result?.result?.value;
  } finally {
    ws.close();
    await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/close/${target.id}`).catch(() => undefined);
  }
}

async function until(label, condition, ms = 20_000) {
  const deadline = Date.now() + ms;
  for (;;) {
    const value = await condition().catch(() => undefined);
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`等不到：${label}`);
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
}

app.whenReady().then(async () => {
  const { ExtensionBridge } = await import(path.join(DIST, "browser-bridge.js"));
  const { AgentBrowser } = await import(path.join(DIST, "browser.js"));
  const bridge = new ExtensionBridge(() => TOKEN);
  await bridge.start();
  check("桥接开始监听", bridge.status().listening, bridge.status().error || "");
  const agent = new AgentBrowser(() => "extension", bridge);
  const server = await serve();
  const base = `http://127.0.0.1:${server.address().port}`;

  // 浏览器由外面（scripts/e2e-browser-extension.sh）先起好：从 Electron 里派生的
  // Chromium 系浏览器在 macOS 上起不来（实测：没有任何输出就退出，换 `open -na` 也一样）。
  const browser = { kill: () => undefined };
  let exitCode = 1;
  try {
    await until("浏览器的远程调试口", () => json(`http://127.0.0.1:${DEBUG_PORT}/json/version`));
    const workers = await until("扩展的后台脚本", async () => {
      const targets = await json(`http://127.0.0.1:${DEBUG_PORT}/json/list`);
      return targets.some((t) => t.url.includes("doofgbncfflbmekanpfocimbideeiadd")) ? targets : null;
    });
    check("扩展已加载，ID 与 manifest 的 key 对得上", Boolean(workers));

    await inExtensionPage(`chrome.storage.local.set({ pairToken: ${JSON.stringify(TOKEN)} }).then(() => true)`);
    await until("扩展连上 AIClaw", async () => bridge.connected).catch(async (error) => {
      // 连不上时把扩展自己记的状态带出来，不然只知道「没连上」。
      const status = await inExtensionPage("chrome.storage.session.get(null)").catch(() => null);
      throw new Error(`${error.message}；扩展说：${JSON.stringify(status)}`);
    });
    check("扩展用配对码连上", bridge.connected, JSON.stringify(bridge.status().connected));

    const opened = await agent.perform({ action: "navigate", url: `${base}/` });
    check("打开网址并拿到编号列表", /E2E 表单/.test(opened.text) && /\[\d+\]/.test(opened.text), opened.text.split("\n")[0]);
    const inputIndex = Number(/\[(\d+)\][^\n]*(关键词|input|输入)/.exec(opened.text)?.[1] ?? -1);
    check("快照里有输入框", inputIndex >= 0, `index=${inputIndex}`);

    const tabs = await agent.perform({ action: "tabs" });
    const agentLine = tabs.text.split("\n").find((line) => line.includes("E2E 表单")) ?? "";
    check("agent 的标签页在后台（没有抢走用户正在看的页面）", /AIClaw 开的/.test(agentLine) && !/用户正在看/.test(agentLine), agentLine);

    const typed = await agent.perform({ action: "type", index: inputIndex, text: "你好 world", submit: true });
    check("填字并回车提交", /已填入并提交/.test(typed.text), typed.text.split("\n")[0]);
    const extracted = await until("提交后的结果页", async () => {
      const result = await agent.perform({ action: "extract" });
      return /你搜的是：你好 world/.test(result.text) ? result : null;
    }, 10_000).catch(() => agent.perform({ action: "extract" }));
    check("后台标签页里的表单真的提交了", /你搜的是：你好 world/.test(extracted.text), extracted.text.slice(0, 120).replace(/\n/g, " | "));

    const shot = await agent.perform({ action: "screenshot" }).catch((error) => ({ text: String(error) }));
    check("后台标签页也能截图", Boolean(shot.imageBase64) && shot.width > 0, shot.imageBase64 ? `${shot.width}×${shot.height}` : shot.text);

    const back = await agent.perform({ action: "back" });
    check("后退", /E2E 表单/.test(back.text), back.text.split("\n")[0]);

    exitCode = results.every((r) => r.ok) ? 0 : 1;
  } catch (error) {
    check("端到端", false, String(error && error.stack ? error.stack : error).slice(0, 400));
  } finally {
    console.log(`${results.filter((r) => r.ok).length}/${results.length} 通过`);
    browser.kill();
    bridge.stop();
    server.close();
    setTimeout(() => app.exit(exitCode), 300);
  }
});
