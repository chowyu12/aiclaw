// The Electron shell. It owns the window, the native dialogs and the Go core's
// lifecycle, and nothing else: every behaviour belongs to the core.
//
// See docs/design/electron-migration.md.

import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve, sep } from "node:path";

import { BrowserWindow, app, dialog, ipcMain, net, protocol, session, shell } from "electron";
import { pathToFileURL } from "node:url";

import { Sidecar, type HostCall, type SidecarState } from "./sidecar";

/** Protocols shell.openExternal may open. Anything else can launch a local
 *  handler, so a link in the interface must not reach it. */
const OPENABLE_PROTOCOLS = new Set(["http:", "https:"]);

/**
 * The renderer is served over a custom scheme rather than from file://.
 *
 * Vite emits the application as an ES module, and a module script cannot be
 * fetched from a file:// page: modules require CORS and file:// is an opaque
 * origin. The page loaded but Vue never mounted, silently — an empty window
 * with a working bridge behind it. A standard scheme also gives the page a
 * real origin, so storage and CSP behave predictably.
 */
const RENDERER_SCHEME = "app";

/**
 * Attachments are served over their own scheme rather than inlined.
 *
 * The core used to base64 an image into its reply. Over a pipe that is
 * untenable: a 20MB attachment becomes roughly 27MB of JSON on the single
 * channel every other message shares. Serving the file instead keeps previews
 * out of the protocol entirely, and lets the page stream and cache them.
 */
const ATTACHMENT_SCHEME = "aiclaw";

protocol.registerSchemesAsPrivileged([
  {
    scheme: RENDERER_SCHEME,
    privileges: { standard: true, secure: true, supportFetchAPI: true },
  },
  {
    scheme: ATTACHMENT_SCHEME,
    privileges: { standard: true, secure: true, supportFetchAPI: true, stream: true },
  },
]);

let window: BrowserWindow | undefined;
let sidecar: Sidecar | undefined;

/** Locates the built renderer: beside the compiled shell when packaged, in
 *  the repository when developing. */
function rendererRoot(): string {
  const packaged = join(__dirname, "..", "renderer");
  if (existsSync(join(packaged, "index.html"))) return packaged;
  return join(__dirname, "..", "..", "renderer", "dist");
}

/** Serves the renderer, refusing any path that escapes its directory. */
function serveRenderer(root: string): void {
  protocol.handle(RENDERER_SCHEME, (request) => {
    const { pathname } = new URL(request.url);
    const relative = decodeURIComponent(pathname === "/" ? "/index.html" : pathname);
    const target = join(root, relative);
    const resolvedRoot = join(root, "/");
    if (!target.startsWith(resolvedRoot) && target !== join(root, "index.html")) {
      return new Response("forbidden", { status: 403 });
    }
    return net.fetch(pathToFileURL(target).toString());
  });
}

/**
 * Serves attachment previews from the data directory.
 *
 * Only files inside that directory are served, and the path is resolved
 * before the check: a preview URL reaches this handler from the page, so a
 * traversal attempt must not be able to read the rest of the disk.
 */
function serveAttachments(dataDir: string): void {
  protocol.handle(ATTACHMENT_SCHEME, async (request) => {
    const { pathname } = new URL(request.url);
    const target = resolve(dataDir, "." + decodeURIComponent(pathname));
    if (!target.startsWith(resolve(dataDir) + sep)) {
      return new Response("forbidden", { status: 403 });
    }
    if (!existsSync(target)) return new Response("not found", { status: 404 });
    return net.fetch(pathToFileURL(target).toString());
  });
}

/** Locates the core executable: alongside the app when packaged, in the
 *  repository when developing. */
function coreExecutable(): string {
  const name = process.platform === "win32" ? "aiclaw-core.exe" : "aiclaw-core";
  const packaged = join(process.resourcesPath ?? "", "core", name);
  if (existsSync(packaged)) return packaged;
  return join(__dirname, "..", "..", "bin", name);
}

/** Answers the calls the core cannot make itself, because they need a window. */
async function handleHostCall(call: HostCall): Promise<unknown> {
  switch (call.host) {
    case "dialog.pickFiles": {
      const request = (call.params ?? {}) as {
        Title?: string;
        Filters?: { DisplayName: string; Pattern: string }[];
      };
      const result = await dialog.showOpenDialog({
        title: request.Title,
        properties: ["openFile", "multiSelections"],
        // The core states patterns as "*.png;*.jpg"; Electron wants bare
        // extensions.
        filters: (request.Filters ?? []).map((filter) => ({
          name: filter.DisplayName,
          extensions: filter.Pattern.split(";")
            .map((pattern) => pattern.replace(/^\*\./, "").trim())
            .filter((extension) => extension && extension !== "*"),
        })),
      });
      return result.canceled ? [] : result.filePaths;
    }
    case "dialog.pickDirectory": {
      const { title } = (call.params ?? {}) as { title?: string };
      const result = await dialog.showOpenDialog({
        title,
        properties: ["openDirectory"],
      });
      return result.canceled ? "" : (result.filePaths[0] ?? "");
    }
    default:
      throw new Error(`the shell cannot handle ${call.host}`);
  }
}

function reportState(state: SidecarState, detail?: string): void {
  // The interface must be told the truth rather than appear healthy while the
  // core is gone.
  window?.webContents.send("core:state", { state, detail });
}

async function createWindow(): Promise<void> {
  window = new BrowserWindow({
    width: 1280,
    height: 820,
    minWidth: 680,
    minHeight: 540,
    backgroundColor: "#1b2636",
    show: false,
    webPreferences: {
      preload: join(__dirname, "preload.js"),
      // The plugin system already lets this application run local commands;
      // handing node to the renderer on top of that is not acceptable.
      nodeIntegration: false,
      contextIsolation: true,
      sandbox: true,
    },
  });

  window.once("ready-to-show", () => window?.show());
  window.on("closed", () => {
    window = undefined;
  });

  // Links open in the user's browser, never as a new app window.
  window.webContents.setWindowOpenHandler(({ url }) => {
    void openExternal(url);
    return { action: "deny" };
  });

  await window.loadURL(`${RENDERER_SCHEME}://local/index.html`);
}

async function openExternal(url: string): Promise<void> {
  try {
    if (!OPENABLE_PROTOCOLS.has(new URL(url).protocol)) return;
  } catch {
    return;
  }
  await shell.openExternal(url);
}

/**
 * Restricts what the page may load.
 *
 * Without this the renderer runs with no policy at all, which Electron warns
 * about: a compromised dependency could reach any origin. The policy allows
 * the renderer's own scheme and attachment previews, and nothing remote.
 */
function applyContentSecurityPolicy(): void {
  session.defaultSession.webRequest.onHeadersReceived((details, callback) => {
    callback({
      responseHeaders: {
        ...details.responseHeaders,
        "Content-Security-Policy": [
          [
            "default-src 'self'",
            // Vite inlines a small runtime style block.
            "style-src 'self' 'unsafe-inline'",
            `img-src 'self' data: ${ATTACHMENT_SCHEME}:`,
            "font-src 'self' data:",
            "connect-src 'self'",
            "object-src 'none'",
            "base-uri 'none'",
            "frame-ancestors 'none'",
          ].join("; "),
        ],
      },
    });
  });
}

function registerIpc(): void {
  ipcMain.handle("core:invoke", async (_event, command: string, params: unknown[]) => {
    if (!sidecar) throw new Error("the core is not running");
    return sidecar.invoke(command, params ?? []);
  });
  ipcMain.handle("core:commands", () => sidecar?.availableCommands ?? []);
  ipcMain.handle("shell:openExternal", (_event, url: string) => openExternal(url));
  ipcMain.handle("shell:openPath", (_event, path: string) => shell.openPath(path));
  ipcMain.handle("shell:showItemInFolder", (_event, path: string) => {
    shell.showItemInFolder(path);
  });
}

app.whenReady().then(async () => {
  const dataDir = join(homedir(), ".aiclaw");
  serveRenderer(rendererRoot());
  serveAttachments(dataDir);
  registerIpc();
  applyContentSecurityPolicy();
  sidecar = new Sidecar({
    executable: coreExecutable(),
    hooks: {
      onEvent: (event) => window?.webContents.send(event.event, ...(event.data ?? [])),
      onHostCall: handleHostCall,
      onState: reportState,
      onLog: (line) => console.error("[core]", line),
    },
  });

  try {
    await sidecar.start();
  } catch (error) {
    // A window that explains itself beats a silent blank one.
    dialog.showErrorBox(
      "AIClaw 无法启动",
      `本地核心未能启动：\n\n${(error as Error).message}`,
    );
    app.quit();
    return;
  }
  await createWindow();
});

// macOS keeps the application running with no windows, matching what the
// Wails shell did with HideWindowOnClose.
app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

app.on("activate", () => {
  if (!window) void createWindow();
});

app.on("before-quit", async (event) => {
  if (!sidecar || sidecar.currentState === "stopped") return;
  event.preventDefault();
  await sidecar.stop();
  sidecar = undefined;
  app.quit();
});
