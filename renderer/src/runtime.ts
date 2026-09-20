// The Wails runtime helpers, reimplemented over the Electron shell's bridge.
//
// The names and signatures match what the interface already imported, so
// App.vue needed only its import path changed. See
// docs/design/electron-migration.md.

/** Opens a link in the user's browser. The shell restricts this to http and
 *  https; anything else is ignored there rather than here. */
export function BrowserOpenURL(url: string): void {
  void window.aiclaw.openExternal(url);
}

/**
 * Subscribes to one core event, returning an unsubscribe function.
 *
 * Generic over the payload so a typed handler still type-checks at the call
 * site. Wails declared this with `any`, which accepted anything; widening the
 * parameter here instead would have thrown away the interface's own types.
 */
export function EventsOn<T extends unknown[]>(
  name: string,
  handler: (...args: T) => void,
): () => void {
  return window.aiclaw.on(name, handler as (...args: unknown[]) => void);
}

type DropHandler = (x: number, y: number, paths: string[]) => void;

let dropListeners: { over: (event: DragEvent) => void; drop: (event: DragEvent) => void } | undefined;

/**
 * Installs a file-drop handler.
 *
 * Wails delivered dropped paths through its own runtime. Here the drop is an
 * ordinary DOM event, and the path comes from the shell: Electron 32 removed
 * `File.path`, so reading it would silently yield undefined for every file —
 * the interface would see drops that contain nothing. `webUtils` is the only
 * way to resolve a dropped file to a path, and it lives in the preload.
 */
export function OnFileDrop(handler: DropHandler, preventDefault: boolean): void {
  OnFileDropOff();
  const over = (event: DragEvent) => {
    if (preventDefault) event.preventDefault();
  };
  const drop = (event: DragEvent) => {
    if (preventDefault) event.preventDefault();
    const files = Array.from(event.dataTransfer?.files ?? []);
    if (files.length === 0) return;
    const paths = files
      .map((file) => window.aiclaw.pathForFile(file))
      .filter((path): path is string => Boolean(path));
    if (paths.length > 0) handler(event.clientX, event.clientY, paths);
  };
  window.addEventListener("dragover", over);
  window.addEventListener("drop", drop);
  dropListeners = { over, drop };
}

/** Removes the file-drop handler. */
export function OnFileDropOff(): void {
  if (!dropListeners) return;
  window.removeEventListener("dragover", dropListeners.over);
  window.removeEventListener("drop", dropListeners.drop);
  dropListeners = undefined;
}
