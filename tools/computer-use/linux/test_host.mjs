/** Full Linux host/controller/stdio/native test in a private Xvfb desktop. */
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as pause } from "node:timers/promises";
const { ComputerController } = await import(new URL("../../dist/main/computer.js", import.meta.url));
const here = dirname(fileURLToPath(import.meta.url));
const temporary = await mkdtemp(join(tmpdir(), "aiclaw-linux-host-"));
const children = [];
const controller = new ComputerController();
const call = (action, args = {}) => controller.perform({ sessionId: "linux-host-acceptance", turnId: "t1", action, arguments: args });
const launch = async name => {
  const child = spawn("/usr/bin/python3", [join(here, "fixture.py"), join(temporary, `${name}.json`), `AIClaw ${name} Fixture`], { stdio: ["ignore", "pipe", "inherit"] });
  children.push(child);
  await Promise.race([once(child.stdout, "data"), pause(10_000).then(() => { throw Error("GTK fixture startup timed out"); })]);
  return String(child.pid);
};
try {
  const target = await launch("Target"), sentinel = await launch("Sentinel");
  let inventory;
  for (let attempt = 0; attempt < 30; attempt++) {
    inventory = JSON.parse((await call("find_apps", { text: "AIClaw Target Fixture" })).text);
    if (inventory.apps.some(item => item.id === target)) break;
    await pause(100);
  }
  assert.ok(inventory.apps.some(item => item.id === target));
  await call("modify_app", { app: sentinel, modification: "activate", verify_after: false });
  const foreground = JSON.parse((await call("frontmost_window")).text).app;
  assert.equal(foreground, sentinel);
  const query = async name => {
    const state = await call("query", { app: target, text: name, exact: true });
    const data = JSON.parse(state.text.split("\n")[0]);
    assert.equal(data.truncated, false); assert.equal(data.matches.length, 1);
    return { app: target, element_index: String(data.matches[0].index), observation_id: state.text.match(/observation_id: ([\w-]+)/)[1], interaction_mode: "background", verify_after: false };
  };
  await call("set_value", { ...await query("Fixture input"), value: "Linux 宿主 🌦️" });
  await call("click", await query("Submit fixture"));
  assert.equal(JSON.parse(await readFile(join(temporary, "Target.json"), "utf8")).value, "Linux 宿主 🌦️");
  const editable = await query("Fixture input");
  const started = await call("watch_start", { app: target, interval_ms: 500 });
  const baseline = JSON.parse(started.text);
  assert.ok(started.imageBase64); assert.equal(baseline.reset, true);
  const quiet = JSON.parse((await call("watch_next", { watch_id: baseline.watch_id, after_sequence: baseline.sequence, wait_ms: 900 })).text);
  assert.equal(quiet.changed, false);
  await call("set_value", { ...editable, value: "Linux 订阅变化 🌦️" });
  const update = await call("watch_next", { watch_id: baseline.watch_id, after_sequence: baseline.sequence, wait_ms: 3000 });
  const delta = JSON.parse(update.text);
  assert.equal(delta.changed, true); assert.equal(delta.reset, false);
  const lines = [...baseline.ax.lines]; lines.splice(delta.ax.start, delta.ax.delete_count, ...delta.ax.lines);
  assert.ok(lines.some(line => line.includes("Linux 订阅变化 🌦️")));
  assert.ok(update.imageBase64);
  const pending = assert.rejects(call("watch_next", { watch_id: baseline.watch_id, after_sequence: delta.sequence, wait_ms: 10_000 }), /stopped/);
  await pause(20); await controller.finishTurn("linux-host-acceptance", "t1"); await pending;
  console.log(JSON.stringify({ passed: true, backend: "x11-atspi", host_controller: true, private_stdio: true, source_identity_handshake: true, background_receipt: true, window_png: true, static_window_no_update: true, ax_delta_reconstructed: true, sampling_preserved_action_observation: true, finish_turn_cancelled_wait: true }));
} finally {
  await controller.close();
  for (const child of children) child.kill("SIGTERM");
  await rm(temporary, { recursive: true, force: true });
}
