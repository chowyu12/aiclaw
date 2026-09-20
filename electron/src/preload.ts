// The only bridge between the renderer and this process.
//
// Everything reaches the renderer through this allowlist: it can invoke core
// commands, subscribe to events, and ask the shell to open a path. It cannot
// touch node, the filesystem, or the core's pipe directly.

import { contextBridge, ipcRenderer } from "electron";

/** Events the core emits that the interface subscribes to. Listening is
 *  restricted to these names so a page cannot attach to arbitrary IPC. */
const SUBSCRIBABLE = new Set([
  "chat:delta",
  "chat:progress",
  "chat:finished",
  "core:state",
]);

contextBridge.exposeInMainWorld("aiclaw", {
  /** Invokes a core command with positional arguments. */
  invoke: (command: string, ...params: unknown[]): Promise<unknown> =>
    ipcRenderer.invoke("core:invoke", command, params),

  /** The commands this core reported, for a startup contract check. */
  commands: (): Promise<string[]> => ipcRenderer.invoke("core:commands"),

  /** Subscribes to one core event. Returns an unsubscribe function. */
  on: (name: string, handler: (...args: unknown[]) => void): (() => void) => {
    if (!SUBSCRIBABLE.has(name)) {
      throw new Error(`${name} is not a subscribable event`);
    }
    const listener = (_event: unknown, ...args: unknown[]) => handler(...args);
    ipcRenderer.on(name, listener);
    return () => ipcRenderer.removeListener(name, listener);
  },

  openExternal: (url: string): Promise<void> =>
    ipcRenderer.invoke("shell:openExternal", url),
  openPath: (path: string): Promise<string> =>
    ipcRenderer.invoke("shell:openPath", path),
  showItemInFolder: (path: string): Promise<void> =>
    ipcRenderer.invoke("shell:showItemInFolder", path),
});
