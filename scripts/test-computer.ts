import test from "node:test";
import assert from "node:assert/strict";
import { PassThrough } from "node:stream";
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import {
  CuaRuntime,
  CUA_VERSION,
} from "../apps/desktop/src/main/cua-runtime.ts";
function harness() {
  const child = Object.assign(new EventEmitter(), {
    stdin: new PassThrough(),
    stdout: new PassThrough(),
    stderr: new PassThrough(),
    kill() {
      this.emit("exit", 0);
    },
  });
  const requests: any[] = [];
  let buffer = "",
    release = 0;
  child.stdin.on("data", (chunk) => {
    buffer += chunk;
    let end: number;
    while ((end = buffer.indexOf("\n")) >= 0) {
      const frame = JSON.parse(buffer.slice(0, end));
      buffer = buffer.slice(end + 1);
      requests.push(frame);
      if (frame.id) {
        const result =
          frame.method === "initialize"
            ? { protocolVersion: "2025-06-18" }
            : frame.method === "tools/list"
              ? { tools: [] }
              : { content: [{ type: "text", text: "ok" }] };
        child.stdout.write(
          JSON.stringify({ jsonrpc: "2.0", id: frame.id, result }) + "\n",
        );
      }
    }
  });
  const runtime = new CuaRuntime(child as any, async () => {
    release++;
  });
  return { runtime, requests, child, released: () => release };
}
test("Cua uses negotiated MCP, a branded per-turn cursor lease, and host-owned routing", async () => {
  const h = harness();
  await h.runtime.initialize();
  await h.runtime.call("list_apps", { session: "model-spoof" });
  assert.equal(h.requests[0].params.protocolVersion, "2025-06-18");
  const calls = h.requests.filter((frame) => frame.method === "tools/call");
  assert.equal(calls[0].params.name, "start_session");
  assert.match(calls[0].params.arguments.session, /^AIClaw-/);
  assert.equal(
    calls[2].params.arguments.session,
    calls[0].params.arguments.session,
  );
  assert.equal(calls[1].params.name, "set_agent_cursor_enabled");
  assert.equal(calls[1].params.arguments.enabled, true);
  await h.runtime.close();
  await h.runtime.close();
  assert.equal(h.released(), 1);
  assert.equal(h.requests.at(-1).params.name, "end_session");
});
test("closing during pending input cancels the request, rejects it, and never resends", async () => {
  const h = harness();
  await h.runtime.initialize();
  h.child.stdin.removeAllListeners("data");
  const sent: any[] = [];
  h.child.stdin.on("data", (chunk) => sent.push(JSON.parse(String(chunk))));
  const pending = h.runtime.call("run_steps", { steps: [] });
  const rejected = assert.rejects(pending, /closed/);
  await h.runtime.close();
  await rejected;
  assert.equal(
    sent.filter(
      (frame) =>
        frame.method === "tools/call" && frame.params.name === "run_steps",
    ).length,
    1,
  );
  assert.ok(sent.some((frame) => frame.method === "notifications/cancelled"));
});
test("release pin includes hashes for all packaged targets and no floating latest", () => {
  const manifest = JSON.parse(
    readFileSync(
      new URL("../tools/cua-driver/runtime.json", import.meta.url),
      "utf8",
    ),
  );
  assert.equal(manifest.version, CUA_VERSION);
  assert.equal(Object.keys(manifest.assets).length, 5);
  for (const asset of Object.values(manifest.assets) as any[]) {
    assert.match(asset.sha256, /^[a-f0-9]{64}$/);
    assert.ok(asset.name.includes(CUA_VERSION));
  }
});
