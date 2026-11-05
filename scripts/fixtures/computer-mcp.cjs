const readline = require("node:readline");
const mode = process.argv[2];
readline.createInterface({ input: process.stdin }).on("line", line => {
  const frame = JSON.parse(line); if (frame.id === undefined) return;
  if (mode === "malformed") return process.stdout.write("not JSON\n");
  if (mode === "exit") return process.exit(1);
  if (mode === "timeout" && frame.method === "tools/call") return;
  if (mode === "slow-observation" && frame.method === "tools/call") {
    if (frame.params.name !== "get_app_state") return;
    return setTimeout(() => process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: frame.id, result: { content: [{ type: "text", text: "slow observation" }] } }) + "\n"), 1400);
  }
  const result = frame.method === "initialize" ? { protocolVersion: "2025-03-26", serverInfo: { name: "open-computer-use", version: mode === "version" ? "0.0.0" : "1.1.3" } }
    : frame.method === "tools/list" ? { tools: [] } : { content: [{ type: "text", text: "fixture" }] };
  process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: frame.id, result }) + "\n");
});
