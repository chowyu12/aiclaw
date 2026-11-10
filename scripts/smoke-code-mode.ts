/** Production binary → isolated Goja worker → fixture tools → incremental wait. No real model or desktop input. */
import { strict as assert } from "node:assert";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { ClawAgentClient } from "../packages/agent-client/dist/index.js";

const root = mkdtempSync(join(tmpdir(), "aiclaw-code-mode-smoke-"));
const client = new ClawAgentClient({
  command: resolve("tools/claw-agent", process.platform === "win32" ? "claw-agent.exe" : "claw-agent"),
  args: ["serve", `--data-home=${join(root, "agent")}`, `--app-db=${join(root, "app.db")}`],
  env: { ...process.env },
});
let stage = 0;
let failure: unknown;
const server = createServer(async (req, res) => {
  try {
    let body = "";
    for await (const chunk of req) body += chunk;
    const request = JSON.parse(body) as { messages: { role: string; content?: unknown }[] };
    const result = request.messages.filter(message => message.role === "tool").at(-1);
    const output = String(result?.content ?? "");
    let tool: { name: string; arguments: string } | undefined;
    if (stage === 0) {
      tool = { name: "exec", arguments: JSON.stringify({ title: "Verify code mode", code: `
        store('runs', (load('runs') ?? 0) + 1);
        store('retained', {count:7});
        const spec=await tools.tool_describe({name:'write_file'});
        if (!spec.tools[0].inputSchema.properties.path) throw new Error('missing schema');
        await tools.write_file({path:'worker.txt',content:'exactly once'});
        await tools.read_file({path:'worker.txt'});
        notify('phase-one');
        await new Promise(resolve=>setTimeout(resolve,500));
        return 'phase-two';
      ` }) };
      stage = 1;
    } else if (stage === 1) {
      const cell = /Script running with cell ID (cell_\d+)/.exec(output)?.[1];
      if (cell) tool = { name: "wait", arguments: JSON.stringify({ cell_id: cell, yield_time_ms: 1000 }) };
      else {
        assert.equal(output, "phase-two", "wait must return new output without replaying phase one");
        tool = { name: "exec", arguments: JSON.stringify({ title: "Check retained state", code: "return {runs:load('runs'),retained:load('retained')};" }) };
        stage = 2;
      }
    } else if (stage === 4) {
      tool = { name: "exec", arguments: JSON.stringify({ title: "Check state after remount", code: "return {runs:load('runs'),retained:load('retained')};" }) };
      stage = 5;
    } else if (stage === 5) {
      assert.deepEqual(JSON.parse(output), { runs: 1, retained: { count: 7 } });
      stage = 6;
    } else if (stage === 2) {
      assert.deepEqual(JSON.parse(output), { runs: 1, retained: { count: 7 } });
      stage = 3;
    }
    const delta = tool
      ? { tool_calls: [{ index: 0, id: `call-${request.messages.length}`, type: "function", function: tool }] }
      : { content: "code-mode verified" };
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    res.end(`data: ${JSON.stringify({ choices: [{ delta }] })}\n\ndata: [DONE]\n\n`);
  } catch (error) {
    failure = error;
    res.writeHead(400, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ error: { message: String(error) } }));
  }
});
await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
try {
  await client.start();
  const provider = await client.providerCreate({ name: "code-mode fixture", type: "openai-compatible", baseUrl: `http://127.0.0.1:${(server.address() as { port: number }).port}/v1`, apiKey: "fixture-key", models: ["fixture"] });
  const session = await client.sessionStart({ model: { providerId: provider.id, baseUrl: "", model: "fixture" }, workdir: root, codeMode: true, approvalPolicy: "never" });
  assert.ok(session.tools.includes("exec") && session.tools.includes("wait"));
  const failures: string[] = [];
  const finished = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("code-mode smoke exceeded 15 seconds")), 15000);
    client.on("notification", note => {
      const params = note.params as { sessionId?: string; error?: string; item?: { toolFailed?: boolean; toolResult?: string } };
      if (params.sessionId !== session.sessionId) return;
      if (params.item?.toolFailed) failures.push(params.item.toolResult ?? "failed child tool");
      if (note.method === "turn/completed") {
        clearTimeout(timeout);
        if (params.error) reject(new Error(params.error));
        else resolve();
      }
    });
  });
  await client.turnStart(session.sessionId, "Verify isolated code-mode execution.");
  await finished;
  if (failure) throw failure;
  assert.equal(stage, 3);
  assert.deepEqual(failures, []);
  assert.equal(readFileSync(join(root, "worker.txt"), "utf8"), "exactly once");
  const history = await client.sessionHistory(session.sessionId);
  assert.equal(history.filter(item => item.parentId).length, 3, "nested evidence must survive history readback");
  await client.sessionResume(session.sessionId, { mcpServers: {}, skillDirs: [], memoryFile: "", enableComputerUse: false, disableSandbox: false, codeMode: true, approvalPolicy: "never", locale: "en" });
  stage = 4;
  const remounted = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("remounted code-mode turn timed out")), 15000);
    client.on("notification", note => {
      const params = note.params as { sessionId?: string; error?: string };
      if (params.sessionId === session.sessionId && note.method === "turn/completed") { clearTimeout(timeout); if (params.error) reject(new Error(params.error)); else resolve(); }
    });
  });
  await client.turnStart(session.sessionId, "Check the retained state after remount.");
  await remounted;
  if (failure) throw failure;
  assert.equal(stage, 6);
  console.log("PASS: production code-mode worker, complete discovery, continuous tools, notify/wait deltas, no replay, worker replacement/store, idle settings remount and nested history verified (fixture model only).");
} finally {
  await client.stop().catch(() => undefined);
  server.closeAllConnections();
  await new Promise<void>(resolve => server.close(() => resolve()));
  rmSync(root, { recursive: true, force: true });
}
