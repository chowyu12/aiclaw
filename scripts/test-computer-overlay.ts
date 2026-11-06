import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import type { BrowserWindow, IpcMainInvokeEvent } from "electron";
import { ComputerOverlay, overlayPlacement, overlayTargetFrame } from "../apps/desktop/src/main/computer-overlay.ts";
import type { ComputerControlActivity, ComputerControlState } from "../apps/desktop/src/shared/computer-control.ts";
const primary = { bounds: { x: 0, y: 0, width: 1920, height: 1080 }, workArea: { x: 0, y: 24, width: 1920, height: 1032 }, scaleFactor: 2 };
const left = { bounds: { x: -1920, y: 0, width: 1920, height: 1080 }, workArea: { x: -1920, y: 24, width: 1920, height: 1032 }, scaleFactor: 1 };
const frame = { x: 100, y: 100, width: 800, height: 600 };
const active = (app = "com.example.one"): ComputerControlActivity => ({ sessionId: "chat", turnId: "turn", action: "query", app, pid: 123, windowId: app.endsWith("two") ? 2 : 1 });
const state = (activity = active()): ComputerControlState => ({ mode: "ready", active: activity, watching: 0 });
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes; }); return { promise, resolve }; }
const tick = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };
class FakeWindow extends EventEmitter {
  destroyed = false; shown = false; positions: unknown[] = []; messages: unknown[] = [];
  webContents = Object.assign(new EventEmitter(), { mainFrame: {}, send: (_channel: string, payload: unknown) => this.messages.push(payload), setWindowOpenHandler: () => {} });
  isDestroyed() { return this.destroyed; }
  setContentProtection() {} setAlwaysOnTop() {} setVisibleOnAllWorkspaces() {}
  setBounds(bounds: unknown) { this.positions.push(bounds); }
  showInactive() { this.shown = true; } hide() { this.shown = false; }
  destroy() { this.destroyed = true; this.emit("closed"); }
}
function fixture(readBounds: (activity: ComputerControlActivity) => Promise<typeof frame | null> = async () => frame) {
  const windows: FakeWindow[] = [], controls: string[] = [];
  const overlay = new ComputerOverlay({ platform: "darwin", displays: () => [primary, left], readBounds,
    createWindow: () => { const window = new FakeWindow(); windows.push(window); return window as unknown as BrowserWindow; },
    sendStatus: (window, view) => (window as unknown as FakeWindow).messages.push(view),
    loadWindow: async window => { window.webContents.emit("did-finish-load"); },
    control: async action => { controls.push(action); }, pollMs: 10_000,
  });
  return { overlay, windows, controls };
}

test("macOS frames stay in DIP across different display scales; Windows uses Electron conversion", () => {
  assert.deepEqual(overlayTargetFrame("darwin", frame, [primary]), frame);
  let converted = false;
  assert.deepEqual(overlayTargetFrame("win32", frame, [primary], value => { converted = true; assert.deepEqual(value, frame); return { ...value, x: 50, y: 50, width: 400, height: 300 }; }), { x: 50, y: 50, width: 400, height: 300 });
  assert.equal(converted, true);
  assert.equal(overlayTargetFrame("win32", frame, [primary]), null);
  assert.deepEqual(overlayTargetFrame("linux", frame, [primary]), { x: 50, y: 50, width: 400, height: 300 });
  assert.deepEqual(overlayTargetFrame("linux", frame, [{ ...primary, scaleFactor: 1.5 }]), { x: 67, y: 67, width: 533, height: 400 });
  assert.equal(overlayTargetFrame("linux", frame, [primary, left]), null);
  assert.equal(overlayTargetFrame("linux", frame, [{ ...primary, bounds: { ...primary.bounds, x: 200 } }]), null);
  assert.equal(overlayTargetFrame("linux", frame, [{ ...primary, scaleFactor: NaN }]), null);
  assert.equal(overlayTargetFrame("linux", frame, [{ ...primary, scaleFactor: 0 }]), null);
  assert.deepEqual(overlayTargetFrame("linux", frame, [left]), frame);
});
test("placement selects the target display including negative origins and stays in its work area", () => {
  const bounds = overlayPlacement({ x: -1800, y: 28, width: 800, height: 700 }, [primary, left])!;
  assert.ok(bounds.x >= left.workArea.x && bounds.x + bounds.width <= 0);
  assert.ok(bounds.y >= left.workArea.y && bounds.y + bounds.height <= 1056);
  assert.equal(overlayPlacement({ ...frame, x: 9000 }, [primary, left]), null);
  assert.equal(overlayPlacement({ ...frame, width: NaN }, [primary]), null);
  assert.equal(overlayPlacement(frame, [{ ...primary, workArea: { x: 0, y: 0, width: 100, height: 100 } }]), null);
});
test("an old asynchronous target lookup cannot show the old target after switching windows", async () => {
  const pending = deferred<typeof frame | null>(); let reads = 0;
  const { overlay, windows } = fixture(async activity => { reads++; return activity.app.endsWith("one") ? pending.promise : { ...frame, x: -1000 }; });
  overlay.update(state()); overlay.update(state(active("com.example.two"))); pending.resolve(frame); await tick();
  assert.equal(reads, 2); assert.equal(windows.length, 1); assert.equal(windows[0]!.positions.length, 1);
  assert.ok((windows[0]!.positions[0] as { x: number }).x < 0); assert.equal(windows[0]!.shown, true);
  overlay.dispose();
});
test("pause retains the visible control surface without new target reads; stop hides it", async () => {
  let reads = 0; const { overlay, windows } = fixture(async () => { reads++; return frame; });
  overlay.update(state()); await tick();
  overlay.update({ mode: "paused", active: null, watching: 0 }); await tick();
  assert.equal(reads, 1); assert.equal(windows[0]!.shown, true);
  assert.equal((windows[0]!.messages.at(-1) as { mode: string }).mode, "paused");
  overlay.update({ mode: "stopped", active: null, watching: 0 }); assert.equal(windows[0]!.shown, false);
  overlay.dispose();
});
test("pause or disposal during first lookup cannot resurrect an indicator", async () => {
  for (const action of ["pause", "dispose"]) {
    const pending = deferred<typeof frame | null>(); const { overlay, windows } = fixture(() => pending.promise);
    overlay.update(state());
    if (action === "pause") overlay.update({ mode: "paused", active: null, watching: 0 }); else overlay.dispose();
    pending.resolve(frame); await tick(); assert.equal(windows.length, 0); overlay.dispose();
  }
});
test("overlay IPC is limited to its own main frame and three control actions", async () => {
  const { overlay, windows, controls } = fixture(); overlay.update(state()); await tick();
  const event = { sender: windows[0]!.webContents, senderFrame: windows[0]!.webContents.mainFrame } as unknown as IpcMainInvokeEvent;
  assert.equal(overlay.statusFor(event).target, "com.example.one");
  assert.throws(() => overlay.statusFor({ ...event, sender: {} } as IpcMainInvokeEvent), /Untrusted/);
  assert.throws(() => overlay.controlFrom({ ...event, senderFrame: {} } as IpcMainInvokeEvent, "stop"), /Untrusted/);
  assert.throws(() => overlay.controlFrom(event, "click"), /Invalid/);
  await overlay.controlFrom(event, "pause"); assert.deepEqual(controls, ["pause"]); overlay.dispose();
});
test("closing a visible indicator stops control and never recreates the target window", async () => {
  const { overlay, windows, controls } = fixture(); overlay.update(state()); await tick();
  windows[0]!.destroy(); await tick(); assert.deepEqual(controls, ["stop"]); assert.equal(windows.length, 1); overlay.dispose();
});


test("an initial observation keeps its observing label while waiting for the next action", async () => {
  const { overlay, windows } = fixture();
  overlay.update(state({ ...active(), action: "get_app_state", windowId: undefined }));
  overlay.update(state({ ...active(), action: "waiting" })); await tick();
  assert.equal((windows[0]!.messages.at(-1) as { status: string }).status, "AIClaw 正在观察");
  overlay.dispose();
});
