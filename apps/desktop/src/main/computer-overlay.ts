import type { BrowserWindow, IpcMainInvokeEvent, Rectangle } from "electron";
import { computerActionIsObservation, type ComputerControlAction, type ComputerControlActivity, type ComputerControlState } from "../shared/computer-control.js";

type DisplayArea = { bounds: Rectangle; workArea: Rectangle; scaleFactor: number };
type Frame = Rectangle;
const WIDTH = 370, HEIGHT = 58, GAP = 8;
const finiteFrame = (frame: Frame) => [frame.x, frame.y, frame.width, frame.height, frame.x + frame.width, frame.y + frame.height].every(Number.isFinite) && frame.width > 0 && frame.height > 0;
const rounded = (frame: Frame): Frame => ({ x: Math.round(frame.x), y: Math.round(frame.y), width: Math.round(frame.width), height: Math.round(frame.height) });
const intersection = (a: Frame, b: Frame) => Math.max(0, Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x)) * Math.max(0, Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y));

/** Native macOS AX bounds are points; Windows bounds need Electron's physical-to-DIP conversion.
 * X11 physical pixels are scaled only for a verified origin-zero single display;
 * mixed scales or an unverified scaled multi-display origin remain hidden. */
export function overlayTargetFrame(platform: string, frame: Frame, displays: DisplayArea[], toDip?: (frame: Frame) => Frame): Frame | null {
  if (!finiteFrame(frame) || !displays.length) return null;
  if (platform === "linux") {
    const scale = displays[0]!.scaleFactor;
    if (!Number.isFinite(scale) || scale <= 0 || displays.some(display => display.scaleFactor !== scale)) return null;
    if (scale !== 1 && (displays.length !== 1 || displays[0]!.bounds.x !== 0 || displays[0]!.bounds.y !== 0)) return null;
    const result = { x: frame.x / scale, y: frame.y / scale, width: frame.width / scale, height: frame.height / scale };
    return finiteFrame(result) ? rounded(result) : null;
  }
  if (!["darwin", "win32"].includes(platform) || (platform === "win32" && !toDip)) return null;
  const result = platform === "win32" ? toDip!(rounded(frame)) : frame;
  return finiteFrame(result) ? rounded(result) : null;
}
export function overlayPlacement(frame: Frame, displays: DisplayArea[]): Rectangle | null {
  if (!finiteFrame(frame)) return null;
  const display = displays.filter(display => finiteFrame(display.bounds) && finiteFrame(display.workArea)).sort((a, b) => intersection(frame, b.bounds) - intersection(frame, a.bounds))[0];
  if (!display || intersection(frame, display.bounds) === 0) return null;
  const area = display.workArea, width = Math.min(WIDTH, area.width - GAP * 2);
  if (width < 180 || area.height < HEIGHT + GAP * 2) return null;
  const x = Math.min(area.x + area.width - width - GAP, Math.max(area.x + GAP, frame.x + frame.width - width));
  const above = frame.y - HEIGHT - GAP;
  const y = Math.max(area.y + GAP, Math.min(area.y + area.height - HEIGHT - GAP, above >= area.y + GAP ? above : frame.y + GAP));
  return rounded({ x, y, width, height: HEIGHT });
}

export interface ComputerOverlayView {
  mode: ComputerControlState["mode"];
  target: string;
  status: string;
  pause: string;
  resume: string;
  stop: string;
}
export interface ComputerOverlayOptions {
  platform: string;
  displays: () => DisplayArea[];
  toDip?: (frame: Frame) => Frame;
  createWindow: () => BrowserWindow;
  loadWindow: (window: BrowserWindow) => Promise<unknown>;
  sendStatus: (window: BrowserWindow, view: ComputerOverlayView) => void;
  readBounds: (activity: ComputerControlActivity) => Promise<Frame | null>;
  control: (action: ComputerControlAction) => Promise<unknown>;
  translate?: (source: string) => string;
  pollMs?: number;
}

/** Owns one small, branded control surface. It never captures or sends input to the target. */
export class ComputerOverlay {
  private state: ComputerControlState = { mode: "ready", active: null, watching: 0 };
  private window?: BrowserWindow;
  private timer?: ReturnType<typeof setTimeout>;
  private generation = 0;
  private reading = false;
  private loaded = false;
  private visible = false;
  private disposed = false;
  private observing = false;
  private target = "";
  private readonly options: ComputerOverlayOptions;
  constructor(options: ComputerOverlayOptions) { this.options = options; }

  update(state: ComputerControlState): void {
    const previous = this.state.active, next = state.active;
    if (previous && (!next || [previous.sessionId, previous.turnId, previous.app, previous.pid, previous.windowId].join("\0") !== [next.sessionId, next.turnId, next.app, next.pid, next.windowId].join("\0")) && state.mode !== "paused") this.hide();
    this.generation++;
    clearTimeout(this.timer);
    this.state = state;
    if (state.mode === "paused") {
      if (this.visible) this.send();
      return;
    }
    if (state.active && state.active.action !== "waiting") this.observing = computerActionIsObservation(state.active.action);
    if (state.mode === "stopped" || !state.active?.app || !state.active.windowId) { this.hide(); return; }
    this.target = state.active.displayName || state.active.app;
    void this.refresh();
  }

  private async refresh(): Promise<void> {
    if (this.disposed || this.reading || this.state.mode !== "ready" || !this.state.active?.windowId) return;
    this.reading = true;
    const generation = this.generation, activity = { ...this.state.active };
    try {
      const bounds = await this.options.readBounds(activity);
      if (this.disposed || generation !== this.generation) return;
      const displays = this.options.displays();
      const frame = bounds && overlayTargetFrame(this.options.platform, bounds, displays, this.options.toDip);
      const placement = frame && overlayPlacement(frame, displays);
      if (!placement) { this.hide(); return; }
      this.ensureWindow();
      if (!this.window || this.window.isDestroyed()) return;
      this.window.setBounds(placement, false);
      this.visible = true;
      this.send();
      if (this.loaded) this.window.showInactive();
    } catch { if (generation === this.generation) this.hide(); }
    finally {
      this.reading = false;
      if (!this.disposed && this.state.mode === "ready" && this.state.active?.windowId) {
        if (generation !== this.generation) void this.refresh();
        else {
          this.timer = setTimeout(() => { void this.refresh(); }, this.options.pollMs ?? 250);
          this.timer.unref?.();
        }
      }
    }
  }

  private ensureWindow(): void {
    if (this.window && !this.window.isDestroyed()) return;
    const window = this.options.createWindow();
    this.window = window; this.loaded = false;
    window.setContentProtection(true);
    window.setAlwaysOnTop(true, "floating");
    window.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true, skipTransformProcessType: true });
    window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    window.webContents.on("will-navigate", event => event.preventDefault());
    window.webContents.on("did-finish-load", () => {
      if (this.disposed || this.window !== window || window.isDestroyed()) return;
      this.loaded = true; this.send();
      if (this.visible && this.state.mode !== "stopped") window.showInactive();
    });
    window.on("closed", () => {
      if (this.window !== window) return;
      this.window = undefined; this.loaded = false;
      const wasVisible = this.visible; this.visible = false;
      this.generation++; clearTimeout(this.timer);
      this.state = { mode: "stopped", active: null, watching: 0 };
      if (!this.disposed && wasVisible) void this.options.control("stop").catch(() => {});
    });
    void this.options.loadWindow(window).catch(() => { if (this.window === window) this.hide(); });
  }

  private view(): ComputerOverlayView {
    const translate = this.options.translate ?? ((source: string) => source);
    return {
      mode: this.state.mode,
      target: this.target,
      status: translate(this.state.mode === "paused" ? "AIClaw 已暂停" : this.observing ? "AIClaw 正在观察" : "AIClaw 正在控制"),
      pause: translate("暂停"), resume: translate("恢复"), stop: translate("停止"),
    };
  }
  private send(): void { if (this.loaded && this.window && !this.window.isDestroyed()) this.options.sendStatus(this.window, this.view()); }
  private hide(): void { this.visible = false; this.window?.hide(); }
  accepts(event: Pick<IpcMainInvokeEvent, "sender" | "senderFrame">): boolean {
    return !!this.window && !this.window.isDestroyed() && event.sender === this.window.webContents && event.senderFrame === this.window.webContents.mainFrame;
  }
  statusFor(event: Pick<IpcMainInvokeEvent, "sender" | "senderFrame">) {
    if (!this.accepts(event)) throw Error("Untrusted desktop control window");
    return this.view();
  }
  controlFrom(event: Pick<IpcMainInvokeEvent, "sender" | "senderFrame">, action: unknown) {
    if (!this.accepts(event)) throw Error("Untrusted desktop control window");
    if (action !== "pause" && action !== "resume" && action !== "stop") throw Error("Invalid desktop control action");
    return this.options.control(action);
  }
  dispose(): void {
    this.disposed = true; this.generation++; clearTimeout(this.timer);
    this.visible = false;
    this.window?.destroy(); this.window = undefined;
  }
}
