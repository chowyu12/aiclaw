/** Full Linux host/controller/stdio/native test in a private Xvfb desktop. */
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, readlink, rm, mkdir, writeFile } from "node:fs/promises";
import { tmpdir, homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as pause } from "node:timers/promises";
const { ComputerController } = await import(new URL("../../dist/main/computer.js", import.meta.url));
const here = dirname(fileURLToPath(import.meta.url));
const temporary = await mkdtemp(join(tmpdir(), "aiclaw-linux-host-"));
process.env.XDG_DATA_HOME=join(temporary,"data");
await mkdir(join(process.env.XDG_DATA_HOME,"applications"),{recursive:true});
await writeFile(join(process.env.XDG_DATA_HOME,"applications/aiclaw-native-fixture.desktop"),`[Desktop Entry]\nType=Application\nName=AIClaw Launch Fixture\nExec=/usr/bin/python3 ${join(here,"fixture.py")} ${join(temporary,"launched.json")} "AIClaw Launch Fixture"\nTerminal=false\n`);
const children = [];
const controller = new ComputerController();
const call = (action, args = {}) => controller.perform({ sessionId: "linux-host-acceptance", turnId: "t1", action, arguments: args });
const launch = async name => {
  const child = spawn("/usr/bin/python3", [join(here, "fixture.py"), join(temporary, `${name}.json`), `AIClaw ${name} Fixture`], { stdio: ["ignore", "pipe", "inherit"], env: {...process.env, AICLAW_FIXTURE_CLIPBOARD_SEED: name === "Target" ? "1" : "0"} });
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
  const servicePID = JSON.parse((await call("permission_status")).text).service_pid;
  assert.ok((await readlink(`/proc/${servicePID}/cwd`)).startsWith(join(homedir(),".aiclaw/computer-use/linux")));
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
  const clipboardSnapshot = () => JSON.parse(execFileSync("/usr/bin/python3", ["-c", `import sys,json; sys.path.insert(0,${JSON.stringify(here)}); from clipboard import Clipboard; print(json.dumps({k:list(v[2]) for k,v in Clipboard().snapshot()[0].items()}))`], {encoding:"utf8"}));
  const originalClipboard = clipboardSnapshot();
  const screenshot = await call("get_app_state", {app:target});
  await call("paste", {app:target, text:"Linux 粘贴 🌦️", format:"text", observation_id:screenshot.text.match(/observation_id: ([\w-]+)/)[1], verify_after:false});
  await call("click", await query("Submit fixture"));
  assert.equal(JSON.parse(await readFile(join(temporary,"Target.json"),"utf8")).value,"Linux 粘贴 🌦️Linux 宿主 🌦️");
  assert.deepEqual(clipboardSnapshot(), originalClipboard);
  await call("modify_app", {app:sentinel, modification:"activate", verify_after:false});
  await pause(300); // Let GTK repaint the focus-out style before the baseline.
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
  await pause(3500); // Beyond the normal child shutdown deadline.
  assert.deepEqual(clipboardSnapshot(), originalClipboard, "Restored formats must survive action runtime EOF");
  assert.doesNotThrow(()=>process.kill(servicePID,0), "Restored X11 selection needs a live byte provider");
  execFileSync("/usr/bin/python3",["-c",'import gi; gi.require_version("Gtk","3.0"); from gi.repository import Gtk,Gdk; Gtk.Clipboard.get(Gdk.SELECTION_CLIPBOARD).set_text("new user copy",-1); Gdk.flush()']);
  for(let attempt=0;attempt<30;attempt++) {
    try {process.kill(servicePID,0);} catch {break;}
    await pause(100);
  }
  assert.throws(()=>process.kill(servicePID,0), /ESRCH/, "Clipboard-only provider must exit after the next copy");
  const launchCall=(action,args)=>controller.perform({sessionId:"linux-launch-acceptance",turnId:"launch",action,arguments:args});
  const installed=JSON.parse((await launchCall("find_apps",{text:"AIClaw Launch Fixture"})).text);
  const launcher=installed.apps.find(item=>item.launch_id);
  assert.equal(launcher.launch_id,"launch:aiclaw-native-fixture.desktop");
  const launched=await launchCall("start_app",{app:launcher.launch_id,verify_after:false});
  const launchReceipt=JSON.parse(launched.text.split("\n")[0]);
  assert.equal(launchReceipt.verified,true); assert.match(launchReceipt.app,/^[1-9][0-9]*$/);
  await launchCall("get_app_state",{app:launchReceipt.app});
  const stopped=await launchCall("stop_app",{app:launchReceipt.app,verify_after:false});
  assert.equal(JSON.parse(stopped.text.split("\n")[0]).verified,true);
  console.log(JSON.stringify({ passed: true, backend: "x11-atspi", installed_runtime: true, host_controller: true, private_stdio: true, source_identity_handshake: true, background_receipt: true, window_png: true, unicode_paste_read_back:true, clipboard_all_formats_restored:true, clipboard_survived_runtime_eof:true, clipboard_provider_exited_after_new_copy:true, static_window_no_update: true, ax_delta_reconstructed: true, sampling_preserved_action_observation: true, finish_turn_cancelled_wait: true, discovered_desktop_launch:true, launched_pid_verified:true, launched_window_observed:true, normal_close_requested:true }));
} finally {
  await controller.close();
  for (const child of children) child.kill("SIGTERM");
  await rm(temporary, { recursive: true, force: true });
}
