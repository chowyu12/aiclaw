import { BrowserWindow } from "electron";

import { NOT_CONNECTED, type ExtensionBridge } from "./browser-bridge.js";
import { cdpKey, pngSize } from "./browser-cdp.js";
import {
  EXTRACT_SCRIPT,
  INDEX_SCRIPT,
  formatExtract,
  formatSnapshot,
  formatTabs,
  type RawSnapshot,
  type TabInfo,
} from "./browser-snapshot.js";

/**
 * 浏览器工具的宿主侧：模型按元素编号操作一个页面。
 *
 * 页面有两种（设置 → 浏览器）：AIClaw 自己的 Chromium 窗口（下面几条说的是它），或者用户
 * 自己的 Chrome / Edge 里的一个后台标签页（经扩展，见 browser-bridge.ts 与 docs/design/browser.md）。
 * 动作只写一份，两种页面各是一个 PageDriver。
 *
 * 为什么用 Electron 自己的窗口而不是接 Playwright / CDP：Electron 里就带着 Chromium，
 * 多装一个浏览器驱动只会让安装包再大一百多兆、还多一层版本对齐。BrowserWindow 能
 * 做到我们要的全部：加载、执行页面脚本、合成输入、截图、后退。
 *
 * 几条约束：
 *   - **窗口可见。** 用户随时能看见模型在干什么、随时能接手（登录、验证码）。
 *     browser-use 默认也是有头的，理由相同。
 *   - **独立分区** `persist:agent-browser`：登录态跨会话留着，但与应用自己的渲染层
 *     完全隔开——网页拿不到 preload、拿不到 Node。
 *   - **页面里跑的脚本只读 DOM、只返回 JSON。** 网页可以改自己的 DOM 来骗这段脚本
 *     （比如把「删除」按钮叫成「下一页」），这与 browser-use 面对的是同一个问题，
 *     防线在两处：返回给模型的内容带不可信边界；打开网址要经用户确认。
 *   - 只放 http / https / data:。file: 能读本机文件，about: 之外的内部页也不该由模型打开。
 */

export type BrowserAction =
  | "navigate"
  | "snapshot"
  | "click"
  | "type"
  | "select"
  | "scroll"
  | "back"
  | "key"
  | "extract"
  | "screenshot"
  | "tabs"
  | "use_tab";

export interface BrowserRequest {
  action: BrowserAction;
  url?: string;
  index?: number;
  text?: string;
  submit?: boolean;
  value?: string;
  dy?: number;
  keys?: string;
  /** use_tab 的标签页编号，来自 tabs。 */
  tabId?: number;
}

export interface BrowserResult {
  text: string;
  imageBase64?: string;
  width?: number;
  height?: number;
}

/** 等一次加载最多等多久。慢站点十几秒是常态，再久多半是挂了。 */
const LOAD_TIMEOUT_MS = 30_000;
/** 点击 / 提交之后等页面稳定的时间：给单页应用一点反应时间，又不至于每步都慢。 */
const SETTLE_MS = 400;

/** 浏览器工具在哪儿干活。 */
export type BrowserBackend = "builtin" | "extension";

/**
 * 一个能被驾驶的页面：AIClaw 自己的浏览器窗口，或者用户浏览器里的一个标签页。
 *
 * 上面的动作（快照、点击、输入、选择、滚动）全是往页面里跑脚本，两边共用一套；
 * 只有加载、按键、截图、后退这几样要各自的能力，放在驱动里。
 */
interface PageDriver {
  /** 有没有已经打开的页面。 */
  hasPage(): Promise<boolean>;
  url(): Promise<string>;
  load(url: string): Promise<void>;
  evaluate<T>(expression: string): Promise<T>;
  pressKey(name: string): Promise<void>;
  /** 后退。没有可以后退的页面返回 false。 */
  back(): Promise<boolean>;
  /** 点击 / 提交后等页面稳定。 */
  settle(): Promise<void>;
  screenshot(): Promise<{ base64: string; width: number; height: number }>;
}

export class AgentBrowser {
  private readonly window = new WindowDriver();
  private readonly tab: ExtensionDriver;
  private readonly backend: () => BrowserBackend;

  /**
   * @param backend 现查用哪个后端：用户在设置页里换了，下一步就生效。
   * @param bridge 与浏览器扩展的连接。
   */
  constructor(backend: () => BrowserBackend = () => "builtin", bridge?: ExtensionBridge) {
    this.backend = backend;
    this.tab = new ExtensionDriver(bridge);
  }

  private driver(): PageDriver {
    return this.backend() === "extension" ? this.tab : this.window;
  }

  /** 执行一步。 */
  async perform(request: BrowserRequest): Promise<BrowserResult> {
    switch (request.action) {
      case "navigate":
        return this.navigate(request.url ?? "");
      case "snapshot":
        return this.snapshot();
      case "click":
        return this.click(request.index ?? -1);
      case "type":
        return this.type(request.index ?? -1, request.text ?? "", request.submit === true);
      case "select":
        return this.select(request.index ?? -1, request.value ?? "");
      case "scroll":
        return this.scroll(request.dy ?? 0, request.index ?? -1);
      case "back":
        return this.back();
      case "key":
        return this.key(request.keys ?? "");
      case "extract":
        return this.extract();
      case "screenshot":
        return this.screenshot();
      case "tabs":
        return this.tabs();
      case "use_tab":
        return this.useTab(request.tabId ?? -1);
      default:
        throw new Error(`不认识的浏览器操作：${String(request.action)}`);
    }
  }

  /** 关掉窗口、断开调试。应用退出或运行时停止时调。 */
  close(): void {
    this.window.close();
    this.tab.release();
  }

  /** 必须已经有页面才允许的操作。 */
  private async current(): Promise<PageDriver> {
    const driver = this.driver();
    if (!(await driver.hasPage())) {
      throw new Error("浏览器里还没有打开任何页面，先用 browser_navigate 打开一个网址。");
    }
    return driver;
  }

  private async navigate(raw: string): Promise<BrowserResult> {
    const url = raw.trim();
    if (!/^(https?:\/\/|data:text\/html)/i.test(url)) {
      throw new Error(`只能打开 http/https 网址，给的是：${url.slice(0, 80)}`);
    }
    if (this.backend() === "extension" && /^data:/i.test(url)) {
      throw new Error("在你的浏览器里只能打开 http/https 网址。");
    }
    const driver = this.driver();
    await driver.load(url);
    await sleep(SETTLE_MS);
    return this.snapshot("已打开。");
  }

  private async snapshot(note = ""): Promise<BrowserResult> {
    const driver = await this.current();
    const raw = await driver.evaluate<RawSnapshot>(INDEX_SCRIPT);
    return { text: formatSnapshot(raw, note) };
  }

  private async click(index: number): Promise<BrowserResult> {
    const driver = await this.current();
    const outcome = await driver.evaluate<string>(
      `(() => {
        const el = document.querySelector('[data-aiclaw-i="${index}"]');
        if (!el) return "missing";
        el.scrollIntoView({ block: "center", inline: "nearest" });
        const rect = el.getBoundingClientRect();
        if (rect.width === 0 || rect.height === 0) return "hidden";
        el.focus && el.focus();
        el.click();
        return "ok";
      })()`,
    );
    if (outcome === "missing") throw new Error(`没有 ${index} 号元素：编号来自上一次快照，页面可能已经变了，先 browser_snapshot。`);
    if (outcome === "hidden") throw new Error(`${index} 号元素现在不可见，先滚动或展开它所在的区域。`);
    await driver.settle();
    return this.snapshot(`已点击 ${index} 号。`);
  }

  private async type(index: number, text: string, submit: boolean): Promise<BrowserResult> {
    const driver = await this.current();
    const outcome = await driver.evaluate<string>(
      `(() => {
        const el = document.querySelector('[data-aiclaw-i="${index}"]');
        if (!el) return "missing";
        el.scrollIntoView({ block: "center", inline: "nearest" });
        el.focus();
        if (el.isContentEditable) {
          document.execCommand("selectAll", false, null);
          document.execCommand("insertText", false, ${JSON.stringify(text)});
          return "ok";
        }
        if (!("value" in el)) return "not-input";
        // 用 execCommand 走的是真正的输入路径：React / Vue 的受控输入靠 input 事件
        // 更新状态，直接赋 value 它们看不见。
        el.select && el.select();
        const inserted = document.execCommand("insertText", false, ${JSON.stringify(text)});
        if (!inserted || el.value !== ${JSON.stringify(text)}) {
          el.value = ${JSON.stringify(text)};
          el.dispatchEvent(new Event("input", { bubbles: true }));
          el.dispatchEvent(new Event("change", { bubbles: true }));
        }
        return "ok";
      })()`,
    );
    if (outcome === "missing") throw new Error(`没有 ${index} 号元素：先 browser_snapshot 拿最新编号。`);
    if (outcome === "not-input") throw new Error(`${index} 号不是输入框。`);
    if (submit) await driver.pressKey("Enter");
    await driver.settle();
    return this.snapshot(submit ? `已填入并提交 ${index} 号。` : `已填入 ${index} 号。`);
  }

  private async select(index: number, value: string): Promise<BrowserResult> {
    const driver = await this.current();
    const outcome = await driver.evaluate<string>(
      `(() => {
        const el = document.querySelector('[data-aiclaw-i="${index}"]');
        if (!el) return "missing";
        if (el.tagName.toLowerCase() !== "select") return "not-select";
        const want = ${JSON.stringify(value)}.trim().toLowerCase();
        const option = Array.from(el.options).find((o) =>
          o.value.toLowerCase() === want || (o.textContent || "").trim().toLowerCase() === want)
          || Array.from(el.options).find((o) => (o.textContent || "").toLowerCase().includes(want));
        if (!option) return "no-option:" + Array.from(el.options).map((o) => (o.textContent || "").trim()).slice(0, 20).join(" | ");
        el.value = option.value;
        el.dispatchEvent(new Event("input", { bubbles: true }));
        el.dispatchEvent(new Event("change", { bubbles: true }));
        return "ok";
      })()`,
    );
    if (outcome === "missing") throw new Error(`没有 ${index} 号元素：先 browser_snapshot 拿最新编号。`);
    if (outcome === "not-select") throw new Error(`${index} 号不是下拉框。`);
    if (outcome.startsWith("no-option:")) throw new Error(`下拉框里没有「${value}」，可选：${outcome.slice(10)}`);
    await driver.settle();
    return this.snapshot(`已在 ${index} 号里选了「${value}」。`);
  }

  private async scroll(dy: number, index: number): Promise<BrowserResult> {
    const driver = await this.current();
    if (index >= 0) {
      const found = await driver.evaluate<boolean>(
        `(() => { const el = document.querySelector('[data-aiclaw-i="${index}"]'); if (!el) return false; el.scrollIntoView({ block: "center" }); return true; })()`,
      );
      if (!found) throw new Error(`没有 ${index} 号元素。`);
    } else {
      await driver.evaluate(`window.scrollBy(0, ${Math.trunc(dy)})`);
    }
    await sleep(SETTLE_MS / 2);
    return this.snapshot("已滚动。");
  }

  private async back(): Promise<BrowserResult> {
    const driver = await this.current();
    if (!(await driver.back())) throw new Error("没有可以后退的页面。");
    await sleep(SETTLE_MS);
    return this.snapshot("已后退。");
  }

  private async key(name: string): Promise<BrowserResult> {
    const driver = await this.current();
    await driver.pressKey(name.trim());
    await driver.settle();
    return this.snapshot(`已按 ${name}。`);
  }

  private async extract(): Promise<BrowserResult> {
    const driver = await this.current();
    const text = await driver.evaluate<string>(EXTRACT_SCRIPT);
    return { text: formatExtract(await driver.url(), text ?? "") };
  }

  private async screenshot(): Promise<BrowserResult> {
    const driver = await this.current();
    const { base64, width, height } = await driver.screenshot();
    return {
      text: `已截图（${width}×${height}）。截图只用来看布局；操作仍按 browser_snapshot 的编号。`,
      imageBase64: base64,
      width,
      height,
    };
  }

  private async tabs(): Promise<BrowserResult> {
    if (this.backend() !== "extension") {
      const url = this.window.hasOpenPage() ? await this.window.url() : "";
      return {
        text: url
          ? `现在用的是 AIClaw 自带的浏览器窗口，只有一个页面：${url}。要操作你自己浏览器里的标签页，在设置 → 浏览器里改成「用我的浏览器」。`
          : "现在用的是 AIClaw 自带的浏览器窗口，还没有打开页面。",
      };
    }
    return { text: formatTabs(await this.tab.list(), this.tab.currentTab()) };
  }

  private async useTab(tabId: number): Promise<BrowserResult> {
    if (this.backend() !== "extension") {
      throw new Error("只有在「用我的浏览器」模式下才能接管标签页（设置 → 浏览器）。");
    }
    await this.tab.use(tabId);
    return this.snapshot(`已切到标签页 ${tabId}。它是用户自己打开的页面，操作前想清楚用户是否要你改动它。`);
  }
}

/** AIClaw 自己的浏览器窗口：独立分区、可见。 */
class WindowDriver implements PageDriver {
  private window: BrowserWindow | null = null;

  close(): void {
    if (this.window && !this.window.isDestroyed()) this.window.destroy();
    this.window = null;
  }

  hasOpenPage(): boolean {
    const url = this.window && !this.window.isDestroyed() ? this.window.webContents.getURL() : "";
    return Boolean(url) && url !== "about:blank";
  }

  async hasPage(): Promise<boolean> {
    return this.hasOpenPage();
  }

  async url(): Promise<string> {
    return this.ensure().webContents.getURL();
  }

  private ensure(): BrowserWindow {
    if (this.window && !this.window.isDestroyed()) return this.window;
    const window = new BrowserWindow({
      width: 1200,
      height: 860,
      title: "AIClaw 浏览器",
      show: true,
      webPreferences: {
        partition: "persist:agent-browser",
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        // 网页不该弹窗；要开新页就在本窗口里开。
        javascript: true,
      },
    });
    window.webContents.setWindowOpenHandler(({ url }) => {
      // 新窗口一律在本窗口里打开：模型只操作这一个窗口，弹出去的它看不见。
      if (/^https?:/i.test(url)) void window.loadURL(url);
      return { action: "deny" };
    });
    window.on("closed", () => {
      this.window = null;
    });
    this.window = window;
    return window;
  }

  async load(url: string): Promise<void> {
    const window = this.ensure();
    await withTimeout(window.loadURL(url), LOAD_TIMEOUT_MS, "页面加载超时（30 秒）").catch((error: unknown) => {
      // loadURL 在某些跳转 / 中止时会抛 ERR_ABORTED，页面其实已经在了；
      // 真失败的话下面取快照时 URL 还是空的，会在那里报。
      const message = String(error instanceof Error ? error.message : error);
      if (!message.includes("ERR_ABORTED")) throw error;
    });
  }

  async evaluate<T>(expression: string): Promise<T> {
    return (await this.ensure().webContents.executeJavaScript(expression, true)) as T;
  }

  /** 合成一次按键：keyDown + char + keyUp。 */
  async pressKey(name: string): Promise<void> {
    const window = this.ensure();
    const keyCode = KEY_NAMES[name.toLowerCase()] ?? name;
    window.webContents.sendInputEvent({ type: "keyDown", keyCode });
    if (keyCode.length === 1) window.webContents.sendInputEvent({ type: "char", keyCode });
    window.webContents.sendInputEvent({ type: "keyUp", keyCode });
    await sleep(50);
  }

  async back(): Promise<boolean> {
    const window = this.ensure();
    if (!window.webContents.navigationHistory.canGoBack()) return false;
    const loaded = onceLoaded(window);
    window.webContents.navigationHistory.goBack();
    await withTimeout(loaded, LOAD_TIMEOUT_MS, "后退后页面加载超时");
    return true;
  }

  /** 有导航就等加载完，没有就等一小会儿。 */
  async settle(): Promise<void> {
    const window = this.ensure();
    await sleep(SETTLE_MS);
    if (window.webContents.isLoading()) {
      await withTimeout(onceLoaded(window), LOAD_TIMEOUT_MS, "页面加载超时（30 秒）").catch(() => undefined);
      await sleep(SETTLE_MS / 2);
    }
  }

  async screenshot(): Promise<{ base64: string; width: number; height: number }> {
    // 页面刚加载完时合成器可能还没出第一帧，capturePage 拿到的是空图（CI 的虚拟显示上
    // 常见，实际使用中也会碰到）。空的就等一下再截，最多三次。
    let image = await this.ensure().webContents.capturePage();
    for (let attempt = 0; attempt < 3 && image.isEmpty(); attempt++) {
      await sleep(300);
      image = await this.ensure().webContents.capturePage();
    }
    const { width, height } = image.getSize();
    return { base64: image.toPNG().toString("base64"), width, height };
  }
}

/**
 * 用户自己浏览器里的一个标签页，经「AIClaw 浏览器助手」扩展驱动（见 browser-bridge.ts）。
 *
 * 默认只操作 AIClaw 自己开的后台标签页：第一次 navigate 时在用户最近用的窗口里开一个
 * **不切过去**的标签页，之后都在它上面。用户说「就在我这个页面上」时，模型先
 * browser_tabs 再 browser_use_tab 接管那一个。
 */
class ExtensionDriver implements PageDriver {
  private tabId: number | null = null;
  private readonly bridge: ExtensionBridge | undefined;

  constructor(bridge?: ExtensionBridge) {
    this.bridge = bridge;
  }

  private connection(): ExtensionBridge {
    if (!this.bridge) throw new Error(NOT_CONNECTED);
    return this.bridge;
  }

  currentTab(): number | null {
    return this.tabId;
  }

  /** 断开调试（让浏览器顶部的「正在调试」提示条消失），但不关标签页。 */
  release(): void {
    const tabId = this.tabId;
    if (tabId !== null && this.bridge?.connected) void this.bridge.request("detach", { tabId }).catch(() => undefined);
  }

  async list(): Promise<TabInfo[]> {
    return this.connection().request<TabInfo[]>("tabs.list");
  }

  async use(tabId: number): Promise<void> {
    const tabs = await this.list();
    if (!tabs.some((tab) => tab.tabId === tabId)) {
      throw new Error(`没有编号为 ${tabId} 的网页标签页，先 browser_tabs 看一眼。`);
    }
    this.tabId = tabId;
  }

  async hasPage(): Promise<boolean> {
    if (this.tabId === null) return false;
    try {
      await this.connection().request("tabs.get", { tabId: this.tabId });
      return true;
    } catch (error) {
      if (String(error).includes("No tab")) {
        // 用户把那个标签页关了：下次 navigate 重新开一个。
        this.tabId = null;
        return false;
      }
      throw error;
    }
  }

  async url(): Promise<string> {
    return this.evaluate<string>("location.href");
  }

  async load(url: string): Promise<void> {
    const bridge = this.connection();
    if (this.tabId !== null) {
      try {
        await bridge.request("tabs.navigate", { tabId: this.tabId, url });
        return;
      } catch (error) {
        if (!String(error).includes("No tab")) throw error;
        this.tabId = null;
      }
    }
    const tab = await bridge.request<TabInfo>("tabs.create", { url });
    this.tabId = tab.tabId;
  }

  private async cdp<T>(method: string, params: Record<string, unknown> = {}, timeoutMs?: number): Promise<T> {
    if (this.tabId === null) throw new Error("浏览器里还没有打开任何页面，先用 browser_navigate 打开一个网址。");
    return this.connection().request<T>("cdp", { tabId: this.tabId, method, params }, timeoutMs);
  }

  async evaluate<T>(expression: string): Promise<T> {
    const response = await this.cdp<{
      result?: { value?: unknown };
      exceptionDetails?: { text?: string; exception?: { description?: string } };
    }>("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true, userGesture: true });
    if (response.exceptionDetails) {
      const detail = response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? "脚本出错";
      throw new Error(`页面脚本出错：${detail.split("\n")[0]}`);
    }
    return response.result?.value as T;
  }

  async pressKey(name: string): Promise<void> {
    const key = cdpKey(name);
    if (!key) throw new Error(`不认识的按键：${name}`);
    const base = { key: key.key, code: key.code, windowsVirtualKeyCode: key.windowsVirtualKeyCode };
    await this.cdp("Input.dispatchKeyEvent", { type: key.text ? "keyDown" : "rawKeyDown", ...base, text: key.text });
    await this.cdp("Input.dispatchKeyEvent", { type: "keyUp", ...base });
    await sleep(50);
  }

  async back(): Promise<boolean> {
    const canGoBack = await this.evaluate<boolean>("history.length > 1");
    if (!canGoBack) return false;
    await this.connection().request("tabs.back", { tabId: this.tabId });
    return true;
  }

  async settle(): Promise<void> {
    await sleep(SETTLE_MS);
    const deadline = Date.now() + LOAD_TIMEOUT_MS;
    while (Date.now() < deadline) {
      const tab = await this.connection().request<{ status?: string }>("tabs.get", { tabId: this.tabId });
      if (tab.status !== "loading") break;
      await sleep(200);
    }
  }

  async screenshot(): Promise<{ base64: string; width: number; height: number }> {
    // 后台标签页可能截不出来（浏览器不给不可见的页面合成画面）：限时，并把话说明白。
    const response = await this.cdp<{ data?: string }>("Page.captureScreenshot", { format: "png" }, 15_000).catch(
      (error: unknown) => {
        throw new Error(
          `截图失败：${error instanceof Error ? error.message : String(error)}。` +
            "后台标签页有时截不了图，操作仍可按 browser_snapshot 的编号进行。",
        );
      },
    );
    const base64 = response.data ?? "";
    const { width, height } = pngSize(Buffer.from(base64, "base64"));
    return { base64, width, height };
  }
}

/** 常见按键名到 Electron keyCode 的映射；其余原样传。 */
const KEY_NAMES: Record<string, string> = {
  enter: "Return",
  return: "Return",
  esc: "Escape",
  escape: "Escape",
  tab: "Tab",
  space: "Space",
  backspace: "Backspace",
  delete: "Delete",
  up: "Up",
  arrowup: "Up",
  down: "Down",
  arrowdown: "Down",
  left: "Left",
  arrowleft: "Left",
  right: "Right",
  arrowright: "Right",
  pageup: "PageUp",
  pagedown: "PageDown",
  home: "Home",
  end: "End",
};

function onceLoaded(window: BrowserWindow): Promise<void> {
  return new Promise((resolve) => {
    const done = (): void => {
      window.webContents.off("did-finish-load", done);
      window.webContents.off("did-fail-load", done);
      resolve();
    };
    window.webContents.once("did-finish-load", done);
    window.webContents.once("did-fail-load", done);
  });
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function withTimeout<T>(promise: Promise<T>, ms: number, message: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(message)), ms);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error) => {
        clearTimeout(timer);
        reject(error);
      },
    );
  });
}
