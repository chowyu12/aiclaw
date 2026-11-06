/** Permission-free contract test; may show setup guidance, never requests access. */
const { app } = require("electron");
const { readFileSync } = require("node:fs");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
app.setName("aiclaw-computer-smoke");
app.setPath("userData", path.join(app.getPath("temp"), "aiclaw-computer-smoke"));
const watchdog = setTimeout(() => { console.error("Native Computer Use smoke timed out"); app.exit(1); }, 45_000);
app.whenReady().then(async () => {
  if (process.platform !== "darwin") { console.log("Native Computer Use is unavailable on this OS; no third-party fallback."); return; }
  const { ComputerNativeRuntime } = await import(pathToFileURL(path.join(__dirname, "../apps/desktop/dist/main/computer-runtime.js")));
  const runtime = await ComputerNativeRuntime.create();
  try {
    const specs = JSON.parse(readFileSync(path.join(__dirname, "../tools/claw-agent/internal/agent/computer-tools.json"), "utf8"));
    for (const spec of specs) {
      const native = runtime.tools.find(tool => tool.name === spec.action);
      if (!native) throw Error(`Native service is missing ${spec.action}`);
      for (const field of Object.keys(spec.schema.properties)) {
        if (!["observation_id", "verify_after"].includes(field) && !native.inputSchema.properties[field]) throw Error(`Native service is missing ${spec.action}.${field}`);
      }
    }
    const status = await runtime.call("permission_status", {});
    const permissions = JSON.parse(status.content[0].text);
    if (typeof permissions.accessibilityTrusted !== "boolean" || typeof permissions.screenCaptureGranted !== "boolean") throw Error("Invalid native permission response");
    if (permissions.setupWindowShown !== false) throw Error("Reading permission status must not open the setup window");
    const setup = await runtime.setupPermissions();
    const state = JSON.parse(setup.content[0].text);
    if (state.setupWindowShown !== (!state.accessibilityTrusted || !state.screenCaptureGranted)) throw Error("Startup setup must open only when a required permission is missing");
    console.log(`AIClaw native IPC handshake and ${specs.length} tool contracts verified (no screenshots or input). Permissions: AX=${permissions.accessibilityTrusted}, capture=${permissions.screenCaptureGranted}.`);
  } finally { await runtime.close(); }
}).then(() => { clearTimeout(watchdog); app.quit(); }).catch(error => { console.error(error); clearTimeout(watchdog); app.exit(1); });
