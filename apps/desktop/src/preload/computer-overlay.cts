import { contextBridge, ipcRenderer } from "electron";
import { IPC } from "../shared/ipc.cjs";
contextBridge.exposeInMainWorld("computerControl", {
  status: () => ipcRenderer.invoke(IPC.computerOverlayStatus),
  control: (action: "pause" | "resume" | "stop") => {
    if (action === "resume" && !navigator.userActivation?.isActive) return Promise.reject(new Error("A direct user action is required to resume desktop control"));
    return ipcRenderer.invoke(IPC.computerOverlayControl, action);
  },
  onStatus: (handler: (state: unknown) => void) => {
    const listener = (_event: unknown, state: unknown) => handler(state);
    ipcRenderer.on(IPC.onComputerOverlayStatus, listener);
    return () => ipcRenderer.off(IPC.onComputerOverlayStatus, listener);
  },
});
