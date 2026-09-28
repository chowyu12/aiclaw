/**
 * AIClaw 浏览器助手的后台脚本。
 *
 * 它只是一层薄代理：连上本机的 AIClaw（ws://127.0.0.1:17891），替它管标签页、把 CDP
 * 命令转给 chrome.debugger。「打开、快照、点击、输入」这些动作的逻辑都在 AIClaw 的
 * 主进程里（apps/desktop/src/main/browser.ts），与它自带的浏览器窗口共用一套。
 *
 * 安全上守三条：
 *   - **只连 127.0.0.1，而且双方都要证明自己知道配对码**（挑战-应答，HMAC-SHA256）：
 *     不核对的话，本机任何一个程序只要先占住这个端口，就能借这个扩展驱动你的浏览器，
 *     用你所有的登录态。配对码本身从不在连接上传——直接对暗号的话，冒充扩展连上来
 *     一次就能把它套走。
 *   - **默认只在自己开的后台标签页里干活**（归进「AIClaw」标签组），不碰你正在看的页面；
 *     要接管你已经打开的标签页，得由你在对话里指明。
 *   - **你点了浏览器顶部「取消」调试，就尊重它**：一段时间内不再自己接上去。
 */

const PORT = 17891;
const PROTOCOL_VERSION = 1;
/** 多久没有命令就断开调试：断开后浏览器顶部的「正在调试」提示条会消失。 */
const IDLE_DETACH_MS = 60_000;
/** 用户在提示条上点了取消之后，多久之内不再自己接上去。 */
const USER_CANCEL_COOLDOWN_MS = 5 * 60_000;
const LOAD_TIMEOUT_MS = 30_000;

let socket = null;
let authed = false;
/** 这次连接里我们出的随机数，等对面拿它算证明。 */
let clientNonce = "";
let reconnectTimer = null;
/** 正在等用户在配对页上点「允许 / 拒绝」的那条连接与代码。 */
let pairing = null;
/** 配对页配出来的配对码是扩展自己存的：存的那一下不该当成「用户换了码」去断开重连。 */
let storingOwnToken = false;
/** tabId -> 最后一次使用的时间。 */
const attached = new Map();
/** 用户手动取消调试的时间：tabId -> 时间戳。 */
const canceled = new Map();
let groupId = null;
/** 用户在配对页上点了「拒绝」之后，多久之内不再自己弹配对页（弹窗里点「重新配对」可以提前）。 */
const PAIR_DECLINE_COOLDOWN_MS = 10 * 60_000;

// ---------- 连接 ----------

async function token() {
  const { pairToken } = await chrome.storage.local.get("pairToken");
  return typeof pairToken === "string" ? pairToken.trim() : "";
}

async function setStatus(status, detail = "") {
  await chrome.storage.session.set({ status, detail, at: Date.now() });
  chrome.action.setBadgeText({ text: status === "connected" ? "" : "!" });
  chrome.action.setBadgeBackgroundColor({ color: status === "connected" ? "#2f7d5b" : "#b35c00" });
}

/** 用户最近拒绝过配对，还在冷却期内。 */
async function pairingDeclined() {
  const { pairDeclinedAt } = await chrome.storage.local.get("pairDeclinedAt");
  return typeof pairDeclinedAt === "number" && Date.now() - pairDeclinedAt < PAIR_DECLINE_COOLDOWN_MS;
}

/**
 * 连 AIClaw。有配对码就走挑战-应答；**没有就进配对模式**：AIClaw 回一个四位代码，
 * 扩展打开配对页让用户核对后点「允许」，AIClaw 再把配对码交过来。用户不用在
 * 两边之间复制粘贴。
 */
async function connect() {
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) return;
  const pairToken = await token();
  if (!pairToken && (await pairingDeclined())) {
    await setStatus("unpaired", "你拒绝过配对。要连接 AIClaw，在这个弹窗里点「重新配对」。");
    return;
  }
  let ws;
  try {
    ws = new WebSocket(`ws://127.0.0.1:${PORT}/extension`);
  } catch (error) {
    await setStatus("offline", String(error));
    scheduleReconnect();
    return;
  }
  socket = ws;
  authed = false;
  clientNonce = "";
  ws.onopen = () => {
    void setStatus("connecting", pairToken ? "已连上，正在核对配对码…" : "已连上，等 AIClaw 发起配对…");
  };
  ws.onmessage = (event) => {
    void onMessage(ws, event.data, pairToken);
  };
  ws.onclose = (event) => {
    if (socket === ws) socket = null;
    authed = false;
    if (pairing && pairing.ws === ws) void endPairing("配对中途连接断开了，稍后会自动重试。");
    void onClosed(event.code, Boolean(pairToken));
  };
  ws.onerror = () => {
    // onclose 会紧跟着来，状态在那里写。
  };
}

async function onClosed(code, hadToken) {
  if (code === 4003) {
    // 配对完成：配对码已存好，马上用它重连。
    scheduleReconnect(200);
    return;
  }
  if (code === 4001 && hadToken) {
    // 配对码对不上（多半是 AIClaw 那边重新生成过）：丢掉旧的，下次连接走配对，
    // 用户在配对页上点一下就好，不用再去复制粘贴。
    await chrome.storage.local.remove("pairToken");
    await setStatus("rejected", "配对码失效了（AIClaw 可能重新生成过），马上重新配对。");
    scheduleReconnect(1000);
    return;
  }
  if (code === 4006) {
    // 配对超时（3 分钟没人点）或被新的配对请求顶掉：稍后重新发起即可。
    await setStatus("unpaired", "配对请求过期了，稍后会重新弹出配对页。");
    scheduleReconnect(30_000);
    return;
  }
  if (code === 4005) {
    await setStatus("unpaired", "AIClaw 现在不接受配对：在它的「设置 → 配置 → 浏览器」里选「我的浏览器」。");
  } else {
    await setStatus("offline", "AIClaw 没在运行，或者没开「用我的浏览器」。");
  }
  scheduleReconnect();
}

function scheduleReconnect(delay = 5_000) {
  if (reconnectTimer) clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    void connect();
  }, delay);
}

async function hmac(secret, text) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const signature = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(text));
  return [...new Uint8Array(signature)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

function randomHex(bytes = 16) {
  return [...crypto.getRandomValues(new Uint8Array(bytes))].map((b) => b.toString(16).padStart(2, "0")).join("");
}

/**
 * 握手（两边各证明一次，配对码不上线）：
 *   AIClaw → 扩展  { type: "challenge", nonce: A }
 *   扩展 → AIClaw  { type: "hello", nonce: B, proof: HMAC(码, "ext:" + A), ... }
 *   AIClaw → 扩展  { type: "welcome", proof: HMAC(码, "app:" + B) }
 * 扩展核对 welcome 里的证明之前，不执行任何命令。
 *
 * 配对（还没有配对码时）：
 *   扩展 → AIClaw  { type: "pair", ... }
 *   AIClaw → 扩展  { type: "pairOffer", code: "4821" }     两边都显示这个代码
 *   扩展 → AIClaw  { type: "pairAccept" | "pairReject" }    用户在配对页上点的
 *   AIClaw → 扩展  { type: "paired", token }，然后以 4003 关掉；扩展存好码、马上重连
 *
 * **确认在浏览器这边做**：没配对时扩展分不清对面是不是真的 AIClaw（AIClaw 没开时
 * 别的程序可以占住端口）。让用户对着两边的代码在浏览器里点「允许」，冒充者就骗不到。
 */
async function onMessage(ws, raw, pairToken) {
  let message;
  try {
    message = JSON.parse(raw);
  } catch {
    return;
  }
  if (!authed) {
    if (message.type === "challenge" && typeof message.nonce === "string" && !clientNonce) {
      clientNonce = randomHex();
      const identity = {
        nonce: clientNonce,
        version: PROTOCOL_VERSION,
        extension: chrome.runtime.getManifest().version,
        userAgent: navigator.userAgent,
      };
      if (!pairToken) {
        ws.send(JSON.stringify({ type: "pair", ...identity }));
        return;
      }
      ws.send(JSON.stringify({ type: "hello", ...identity, proof: await hmac(pairToken, "ext:" + message.nonce) }));
      return;
    }
    if (message.type === "welcome" && pairToken && clientNonce && message.proof === (await hmac(pairToken, "app:" + clientNonce))) {
      authed = true;
      await setStatus("connected", "已连上 AIClaw。");
      return;
    }
    if (message.type === "pairOffer" && !pairToken && /^\d{4}$/.test(String(message.code))) {
      await startPairing(ws, String(message.code));
      return;
    }
    if (message.type === "paired" && !pairToken && pairing?.ws === ws && pairing.accepted && typeof message.token === "string") {
      storingOwnToken = true;
      await chrome.storage.local.set({ pairToken: message.token.trim() });
      await chrome.storage.local.remove("pairDeclinedAt");
      await endPairing("配对好了，正在连接 AIClaw…");
      return;
    }
    if (message.type === "ping") {
      // 配对期间也有心跳：等用户点按钮可能要一两分钟，不能让后台脚本睡过去。
      ws.send(JSON.stringify({ type: "pong" }));
      return;
    }
    // 其它任何东西（包括证明对不上）：断开，不回话。
    ws.close(4001, "bad proof");
    return;
  }
  if (message.type === "ping") {
    // 应用层心跳。它同时让 MV3 的后台脚本保持醒着（有消息往来就不会被回收）。
    ws.send(JSON.stringify({ type: "pong" }));
    return;
  }
  if (typeof message.id !== "number" || typeof message.op !== "string") return;
  try {
    const result = await handle(message.op, message.args ?? {});
    ws.send(JSON.stringify({ id: message.id, ok: true, result }));
  } catch (error) {
    ws.send(JSON.stringify({ id: message.id, ok: false, error: error instanceof Error ? error.message : String(error) }));
  }
}

// ---------- 配对页 ----------

async function startPairing(ws, code) {
  pairing = { ws, code, accepted: false, tabId: null };
  await chrome.storage.session.set({ pairCode: code });
  await setStatus("pairing", `AIClaw 请求连接，代码 ${code}：在打开的配对页上核对后点「允许」。`);
  const url = chrome.runtime.getURL("pair.html");
  // 已经开着配对页就切过去，不重复开。
  const existing = (await chrome.tabs.query({ url })).at(0);
  if (existing?.id !== undefined) {
    await chrome.tabs.update(existing.id, { active: true });
    await chrome.tabs.reload(existing.id);
    pairing.tabId = existing.id;
  } else {
    const tab = await chrome.tabs.create({ url, active: true });
    pairing.tabId = tab.id ?? null;
  }
}

async function endPairing(detail) {
  const current = pairing;
  pairing = null;
  await chrome.storage.session.remove("pairCode");
  if (detail) await setStatus("connecting", detail);
  if (current?.tabId !== null && current?.tabId !== undefined) {
    // 配对页自己会显示结果再关掉；这里只在它没关时兜底。
    setTimeout(() => chrome.tabs.remove(current.tabId).catch(() => {}), 2500);
  }
}

// 配对页上的「允许 / 拒绝」，以及弹窗里的「重新配对」。
chrome.runtime.onMessage.addListener((message, _sender, reply) => {
  if (message?.type === "pairDecision") {
    const current = pairing;
    if (!current || current.ws.readyState !== WebSocket.OPEN) {
      reply({ ok: false, error: "配对请求已经失效，AIClaw 会重新发起。" });
      return false;
    }
    if (message.allow) {
      current.accepted = true;
      current.ws.send(JSON.stringify({ type: "pairAccept" }));
      reply({ ok: true });
    } else {
      current.ws.send(JSON.stringify({ type: "pairReject" }));
      void chrome.storage.local.set({ pairDeclinedAt: Date.now() });
      void endPairing("");
      void setStatus("unpaired", "你拒绝了配对。要连接 AIClaw，在这个弹窗里点「重新配对」。");
      current.ws.close(1000, "declined");
      reply({ ok: true });
    }
    return false;
  }
  if (message?.type === "repair") {
    void chrome.storage.local.remove(["pairDeclinedAt", "pairToken"]).then(() => {
      socket?.close();
      socket = null;
      scheduleReconnect(100);
    });
    reply({ ok: true });
    return false;
  }
  return false;
});

// ---------- 操作 ----------

async function handle(op, args) {
  switch (op) {
    case "tabs.list":
      return listTabs();
    case "tabs.create":
      return createTab(args.url);
    case "tabs.navigate":
      return navigateTab(args.tabId, args.url);
    case "tabs.back":
      return backTab(args.tabId);
    case "tabs.get":
      return getTab(args.tabId);
    case "cdp":
      return cdp(args.tabId, args.method, args.params ?? {});
    case "detach":
      await detach(args.tabId);
      return {};
    default:
      throw new Error(`扩展不认识的操作：${op}`);
  }
}

function describe(tab) {
  return {
    tabId: tab.id,
    windowId: tab.windowId,
    title: tab.title ?? "",
    url: tab.url ?? "",
    active: tab.active === true,
    agent: groupId !== null && tab.groupId === groupId,
  };
}

async function listTabs() {
  const tabs = await chrome.tabs.query({});
  return tabs.filter((tab) => /^https?:/i.test(tab.url ?? "")).map(describe);
}

async function getTab(tabId) {
  const tab = await chrome.tabs.get(tabId);
  return { ...describe(tab), status: tab.status };
}

function assertWebURL(url) {
  if (typeof url !== "string" || !/^https?:\/\//i.test(url.trim())) {
    throw new Error(`只能打开 http/https 网址，给的是：${String(url).slice(0, 80)}`);
  }
  return url.trim();
}

/** 在你最近用的那个窗口里开一个**不切过去**的标签页，归进「AIClaw」标签组。 */
async function createTab(url) {
  const target = assertWebURL(url);
  const windows = await chrome.windows.getAll({ windowTypes: ["normal"] });
  const focused = windows.find((w) => w.focused) ?? windows[0];
  const loaded = waitLoaded(null);
  const tab = await chrome.tabs.create({ url: target, active: false, windowId: focused?.id });
  loaded.bind(tab.id);
  try {
    if (groupId !== null) {
      try {
        await chrome.tabs.group({ groupId, tabIds: [tab.id] });
      } catch {
        groupId = null;
      }
    }
    if (groupId === null) {
      groupId = await chrome.tabs.group({ tabIds: [tab.id], createProperties: { windowId: tab.windowId } });
      await chrome.tabGroups.update(groupId, { title: "AIClaw", color: "green", collapsed: false });
    }
  } catch {
    // 分组失败不影响干活（比如浏览器不支持标签组）。
  }
  await loaded.done;
  return getTab(tab.id);
}

async function navigateTab(tabId, url) {
  const target = assertWebURL(url);
  const loaded = waitLoaded(tabId);
  await chrome.tabs.update(tabId, { url: target });
  await loaded.done;
  return getTab(tabId);
}

async function backTab(tabId) {
  const loaded = waitLoaded(tabId);
  await chrome.tabs.goBack(tabId);
  await loaded.done;
  return getTab(tabId);
}

/**
 * 等一个标签页加载完。监听要在触发导航**之前**挂上，不然快的页面会在挂上之前就完了。
 * tabId 可以后补（新建标签页时 id 要等创建完才知道）。
 */
function waitLoaded(tabId) {
  let target = tabId;
  let resolve;
  const done = new Promise((r) => (resolve = r));
  let sawLoading = false;
  const listener = (id, change) => {
    if (target === null || id !== target) return;
    if (change.status === "loading") sawLoading = true;
    if (change.status === "complete" && sawLoading) finish();
  };
  const timer = setTimeout(() => finish(), LOAD_TIMEOUT_MS);
  function finish() {
    clearTimeout(timer);
    chrome.tabs.onUpdated.removeListener(listener);
    resolve();
  }
  chrome.tabs.onUpdated.addListener(listener);
  return {
    done,
    bind(id) {
      target = id;
      // 创建时就已经在加载了：不再要求先看到 loading。
      sawLoading = true;
    },
  };
}

async function ensureAttached(tabId) {
  if (attached.has(tabId)) {
    attached.set(tabId, Date.now());
    return;
  }
  const cancelAt = canceled.get(tabId);
  if (cancelAt && Date.now() - cancelAt < USER_CANCEL_COOLDOWN_MS) {
    throw new Error("你刚在浏览器里取消了对这个标签页的调试，AIClaw 暂时不再接管它。");
  }
  await chrome.debugger.attach({ tabId }, "1.3");
  attached.set(tabId, Date.now());
  // 让后台标签页也当自己有焦点：不然输入框的 focus、execCommand 在后台标签页里会失效。
  await chrome.debugger.sendCommand({ tabId }, "Emulation.setFocusEmulationEnabled", { enabled: true }).catch(() => {});
}

async function cdp(tabId, method, params) {
  if (typeof tabId !== "number" || typeof method !== "string") throw new Error("cdp 需要 tabId 与 method");
  await ensureAttached(tabId);
  return chrome.debugger.sendCommand({ tabId }, method, params);
}

async function detach(tabId) {
  if (!attached.has(tabId)) return;
  attached.delete(tabId);
  await chrome.debugger.detach({ tabId }).catch(() => {});
}

chrome.debugger.onDetach.addListener((source, reason) => {
  if (typeof source.tabId !== "number") return;
  attached.delete(source.tabId);
  if (reason === "canceled_by_user") canceled.set(source.tabId, Date.now());
});

chrome.tabs.onRemoved.addListener((tabId) => {
  attached.delete(tabId);
  canceled.delete(tabId);
});

// 空闲一段时间就断开调试，让「正在调试」提示条消失；下次用到时再接上。
setInterval(() => {
  const now = Date.now();
  for (const [tabId, at] of attached) {
    if (now - at > IDLE_DETACH_MS) void detach(tabId);
  }
}, 15_000);

// ---------- 生命周期 ----------

// MV3 的后台脚本会被回收：闹钟每 30 秒把它叫醒一次，没连着就重连。
chrome.alarms.create("reconnect", { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "reconnect") void connect();
});
chrome.runtime.onStartup.addListener(() => void connect());
chrome.runtime.onInstalled.addListener(() => void connect());
chrome.storage.onChanged.addListener((changes, area) => {
  // 用户在弹窗里手动粘了新的配对码：断开重连。配对页配出来的那次不用这里管（4003 会重连）。
  if (area === "local" && changes.pairToken && storingOwnToken) {
    storingOwnToken = false;
    return;
  }
  if (area === "local" && changes.pairToken?.newValue) {
    socket?.close();
    socket = null;
    scheduleReconnect(100);
  }
});

void connect();
