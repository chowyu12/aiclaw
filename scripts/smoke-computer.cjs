/** Load the real native SDK inside Electron, without observing or operating user apps. */
const { app } = require("electron");
const { readFileSync } = require("node:fs");
const path = require("node:path");
app.setName("aiclaw-computer-smoke");
app.setPath("userData", path.join(app.getPath("temp"), "aiclaw-computer-smoke"));
const watchdog = setTimeout(() => { console.error("Cua Driver smoke timed out"); app.exit(1); }, 30_000);
app.whenReady().then(async () => {
  let driver;
  try {
    const sdk = await import("@trycua/cua-driver");
    const { CuaDriver } = sdk;
    if (process.env.AICLAW_COMPUTER_PERMISSIONS === "1" && process.platform === "darwin") {
      console.log("Host permission status:", sdk.currentMacOsPermissionStatus());
    }
    driver = CuaDriver.create({ embedded: true, claudeCodeCompatibility: false });
    const manifest = JSON.parse(await driver.listToolsJson());
    const specs = JSON.parse(readFileSync(path.join(__dirname, "..", "tools", "claw-agent", "internal", "agent", "computer-tools.json"), "utf8"));
    for (const spec of specs) {
      const upstream = manifest.tools.find(tool => tool.name === spec.action);
      if (!upstream) throw Error(`Native runtime is missing ${spec.action}`);
      for (const field of Object.keys(spec.schema.properties)) {
        if (!upstream.inputSchema.properties[field]) throw Error(`Native runtime is missing ${spec.action}.${field}`);
      }
    }
    console.log(`Cua Driver 0.38.0: Electron loaded the real native library; ${specs.length} tool contracts verified (no desktop actions).`);
  } finally {
    if (driver) { await driver.shutdown(); driver.uniffiDestroy?.(); }
  }
}).then(() => { clearTimeout(watchdog); app.quit(); }).catch(error => {
  console.error(error); clearTimeout(watchdog); app.exit(1);
});
