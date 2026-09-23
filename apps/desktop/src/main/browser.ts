import { BrowserWindow } from "electron";

import {
  EXTRACT_SCRIPT,
  INDEX_SCRIPT,
  formatExtract,
  formatSnapshot,
  type RawSnapshot,
} from "./browser-snapshot.js";

/**
 * 浏览器工具的宿主侧：一个独立的 Chromium 窗口，模型按元素编号操作它。
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
  | "screenshot";

export interface BrowserRequest {
  action: BrowserAction;
  url?: string;
  index?: number;
  text?: string;
  submit?: boolean;
  value?: string;
  dy?: number;
  keys?: string;
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

export class AgentBrowser {
  private window: BrowserWindow | null = null;

  /** 执行一步。窗口按需创建，用户关掉了下次再开。 */
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
      default:
        throw new Error(`不认识的浏览器操作：${String(request.action)}`);
    }
  }

  /** 关掉窗口。应用退出或运行时停止时调。 */
  close(): void {
    if (this.window && !this.window.isDestroyed()) this.window.destroy();
    this.window = null;
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

  /** 必须已经有页面才允许的操作。 */
  private current(): BrowserWindow {
    const window = this.ensure();
    const url = window.webContents.getURL();
    if (!url || url === "about:blank") {
      throw new Error("浏览器里还没有打开任何页面，先用 browser_navigate 打开一个网址。");
    }
    return window;
  }

  private async navigate(raw: string): Promise<BrowserResult> {
    const url = raw.trim();
    if (!/^(https?:\/\/|data:text\/html)/i.test(url)) {
      throw new Error(`只能打开 http/https 网址，给的是：${url.slice(0, 80)}`);
    }
    const window = this.ensure();
    await withTimeout(window.loadURL(url), LOAD_TIMEOUT_MS, "页面加载超时（30 秒）").catch((error: unknown) => {
      // loadURL 在某些跳转 / 中止时会抛 ERR_ABORTED，页面其实已经在了；
      // 真失败的话下面取快照时 URL 还是空的，会在那里报。
      const message = String(error instanceof Error ? error.message : error);
      if (!message.includes("ERR_ABORTED")) throw error;
    });
    await sleep(SETTLE_MS);
    return this.snapshot("已打开。");
  }

  private async snapshot(note = ""): Promise<BrowserResult> {
    const window = this.current();
    const raw = (await window.webContents.executeJavaScript(INDEX_SCRIPT, true)) as RawSnapshot;
    return { text: formatSnapshot(raw, note) };
  }

  private async click(index: number): Promise<BrowserResult> {
    const window = this.current();
    const outcome = (await window.webContents.executeJavaScript(
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
      true,
    )) as string;
    if (outcome === "missing") throw new Error(`没有 ${index} 号元素：编号来自上一次快照，页面可能已经变了，先 browser_snapshot。`);
    if (outcome === "hidden") throw new Error(`${index} 号元素现在不可见，先滚动或展开它所在的区域。`);
    await this.settle(window);
    return this.snapshot(`已点击 ${index} 号。`);
  }

  private async type(index: number, text: string, submit: boolean): Promise<BrowserResult> {
    const window = this.current();
    const outcome = (await window.webContents.executeJavaScript(
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
      true,
    )) as string;
    if (outcome === "missing") throw new Error(`没有 ${index} 号元素：先 browser_snapshot 拿最新编号。`);
    if (outcome === "not-input") throw new Error(`${index} 号不是输入框。`);
    if (submit) {
      await this.pressKey(window, "Enter");
    }
    await this.settle(window);
    return this.snapshot(submit ? `已填入并提交 ${index} 号。` : `已填入 ${index} 号。`);
  }

  private async select(index: number, value: string): Promise<BrowserResult> {
    const window = this.current();
    const outcome = (await window.webContents.executeJavaScript(
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
      true,
    )) as string;
    if (outcome === "missing") throw new Error(`没有 ${index} 号元素：先 browser_snapshot 拿最新编号。`);
    if (outcome === "not-select") throw new Error(`${index} 号不是下拉框。`);
    if (outcome.startsWith("no-option:")) throw new Error(`下拉框里没有「${value}」，可选：${outcome.slice(10)}`);
    await this.settle(window);
    return this.snapshot(`已在 ${index} 号里选了「${value}」。`);
  }

  private async scroll(dy: number, index: number): Promise<BrowserResult> {
    const window = this.current();
    if (index >= 0) {
      const found = (await window.webContents.executeJavaScript(
        `(() => { const el = document.querySelector('[data-aiclaw-i="${index}"]'); if (!el) return false; el.scrollIntoView({ block: "center" }); return true; })()`,
        true,
      )) as boolean;
      if (!found) throw new Error(`没有 ${index} 号元素。`);
    } else {
      await window.webContents.executeJavaScript(`window.scrollBy(0, ${Math.trunc(dy)})`, true);
    }
    await sleep(SETTLE_MS / 2);
    return this.snapshot("已滚动。");
  }

  private async back(): Promise<BrowserResult> {
    const window = this.current();
    if (!window.webContents.navigationHistory.canGoBack()) {
      throw new Error("没有可以后退的页面。");
    }
    const loaded = onceLoaded(window);
    window.webContents.navigationHistory.goBack();
    await withTimeout(loaded, LOAD_TIMEOUT_MS, "后退后页面加载超时");
    await sleep(SETTLE_MS);
    return this.snapshot("已后退。");
  }

  private async key(name: string): Promise<BrowserResult> {
    const window = this.current();
    await this.pressKey(window, name.trim());
    await this.settle(window);
    return this.snapshot(`已按 ${name}。`);
  }

  private async extract(): Promise<BrowserResult> {
    const window = this.current();
    const text = (await window.webContents.executeJavaScript(EXTRACT_SCRIPT, true)) as string;
    return { text: formatExtract(window.webContents.getURL(), text ?? "") };
  }

  private async screenshot(): Promise<BrowserResult> {
    const window = this.current();
    const image = await window.webContents.capturePage();
    const { width, height } = image.getSize();
    return {
      text: `已截图（${width}×${height}）。截图只用来看布局；操作仍按 browser_snapshot 的编号。`,
      imageBase64: image.toPNG().toString("base64"),
      width,
      height,
    };
  }

  /** 合成一次按键：keyDown + char + keyUp。 */
  private async pressKey(window: BrowserWindow, name: string): Promise<void> {
    const keyCode = KEY_NAMES[name.toLowerCase()] ?? name;
    window.webContents.sendInputEvent({ type: "keyDown", keyCode });
    if (keyCode.length === 1) window.webContents.sendInputEvent({ type: "char", keyCode });
    window.webContents.sendInputEvent({ type: "keyUp", keyCode });
    await sleep(50);
  }

  /** 点击 / 提交后等页面稳定：有导航就等加载完，没有就等一小会儿。 */
  private async settle(window: BrowserWindow): Promise<void> {
    await sleep(SETTLE_MS);
    if (window.webContents.isLoading()) {
      await withTimeout(onceLoaded(window), LOAD_TIMEOUT_MS, "页面加载超时（30 秒）").catch(() => undefined);
      await sleep(SETTLE_MS / 2);
    }
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
