/** Verify the actual bundled native MCP service, without screenshots or input. */
const { app } = require("electron");
const { readFileSync } = require("node:fs");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
app.setName("aiclaw-computer-smoke");
app.setPath("userData", path.join(app.getPath("temp"), "aiclaw-computer-smoke"));
const watchdog = setTimeout(() => { console.error("Computer Use smoke timed out"); app.exit(1); }, 45_000);
app.whenReady().then(async () => {
  const { ComputerMcpRuntime } = await import(pathToFileURL(path.join(__dirname, "..", "apps/desktop/dist/main/computer-runtime.js")));
  const runtime = await ComputerMcpRuntime.create();
  try {
    const specs = JSON.parse(readFileSync(path.join(__dirname, "../tools/claw-agent/internal/agent/computer-tools.json"), "utf8"));
    for (const spec of specs) {
      const upstream = runtime.tools.find(tool => tool.name === spec.action);
      if (!upstream) throw Error(`Service is missing ${spec.action}`);
      for (const field of Object.keys(spec.schema.properties)) {
        if (field !== "observation_id" && !upstream.inputSchema.properties[field]) throw Error(`Service is missing ${spec.action}.${field}`);
      }
    }
    console.log(`Open Computer Use 1.1.3: real native MCP handshake and ${specs.length} tool contracts verified (no desktop actions).`);
  } finally { await runtime.close(); }
}).then(() => { clearTimeout(watchdog); app.quit(); }).catch(error => { console.error(error); clearTimeout(watchdog); app.exit(1); });
