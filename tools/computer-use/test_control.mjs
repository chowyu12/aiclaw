/** Shared real-fixture acceptance for portable desktop backends. No simulated runtime. */
import assert from "node:assert/strict";
import { setTimeout as pause } from "node:timers/promises";

export async function verifyControlTransitions({ controller, app, platform, fixtureAlive }) {
  const sessionId = `${platform}-control-acceptance`;
  console.log(JSON.stringify({ acceptance: "desktop-control", platform, architecture: process.arch, phase: "started", fixture: app }));
  let turnId = "pause";
  const call = (action, args = {}) => controller.perform({ sessionId, turnId, action, arguments: args });
  const observationId = result => {
    const id = result.text.match(/observation_id: ([\w-]+)/)?.[1];
    assert.ok(id, "real observation must issue an identifier"); return id;
  };
  async function query() {
    const observed = await call("query", { app, text: "Fixture input", exact: true });
    const receipt = JSON.parse(observed.text.split("\n")[0]);
    const frame = receipt.window_frame;
    assert.ok(frame && ["x", "y", "width", "height"].every(key => typeof frame[key] === "number" && Number.isFinite(frame[key])) && frame.width > 0 && frame.height > 0,
      `${platform} query must return usable native window_frame metadata for indicator tracking`);
    const matches = receipt.matches;
    assert.equal(matches.length, 1, "fixture input must be uniquely observed");
    return { app, element_index: String(matches[0].index), observation_id: observationId(observed), interaction_mode: "background", verify_after: false, value: matches[0].value };
  }
  async function write(value) {
    const observed = await query(); delete observed.value;
    await call("set_value", { ...observed, value });
    assert.equal((await query()).value, value, "newly observed semantic input must really change the fixture");
  }
  async function interrupt(action) {
    const seed = `${platform} before ${action}`;
    await write(seed);
    const watching = await call("watch_start", { app, interval_ms: 500 });
    const baseline = JSON.parse(watching.text);
    const waiting = Promise.allSettled([call("watch_next", { watch_id: baseline.watch_id, after_sequence: baseline.sequence, wait_ms: 10_000 })]);
    const observed = await call("get_app_state", { app, include_ax: false });
    const stale = observationId(observed);
    let entered;
    const started = new Promise(resolve => { entered = resolve; });
    const unsubscribe = controller.subscribe(status => { if (status.active?.sessionId === sessionId && status.active.action === "press_key") entered(); });
    // End only moves the caret in the owned fixture. Its hold duration keeps the
    // real backend busy while a separate lifecycle action is queued behind it.
    const holding = call("press_key", { app, key: "End", duration: 1, observation_id: stale, verify_after: false });
    const heldOutcome = Promise.allSettled([holding]);
    try { await Promise.race([started, pause(5000).then(() => { throw Error("Native hold never entered the controller"); })]); }
    finally { unsubscribe(); }
    const closing = controller.perform({ sessionId: `${sessionId}-other-chat`, turnId: `queued-${action}`, action: "stop_app", arguments: { app, verify_after: false } });
    const closedOutcome = Promise.allSettled([closing]);
    await controller.setControl(action);
    assert.equal(controller.getStatus().mode, action === "pause" ? "paused" : "stopped");
    const activeResult = (await heldOutcome)[0], queuedResult = (await closedOutcome)[0], waitingResult = (await waiting)[0];
    assert.equal(activeResult.status, "rejected", "active native request must be cut off at the host connection");
    assert.equal(queuedResult.status, "rejected", "queued fixture-close input must never be dispatched");
    assert.match(String(queuedResult.reason), /paused|stopped|ended/i);
    assert.equal(waitingResult.status, "rejected", "pause/stop must cancel observation waits");
    assert.equal(controller.getStatus().watching, 0);
    await assert.rejects(call("modify_app", { app, modification: "activate", verify_after: false }), /paused|stopped/i);
    await pause(1200); // Let a dispatched native key-up finish; input is never replayed.
    assert.equal(await fixtureAlive(), true, "queued stop_app must not close the real target");
    await controller.setControl("resume");
    await assert.rejects(call("press_key", { app, key: "End", observation_id: stale, verify_after: false }), /Observe|ended|observation/i);
    if (action === "stop") turnId = "after-stop";
    assert.equal((await query()).value, seed, "paused/stopped execution must not alter the fixture value");
    await write(`${platform} resumed after ${action}`);
    return { active_request_cancelled: true, queued_input_rejected: true, queued_other_chat_input_rejected: true, queued_close_not_executed: true, observation_wait_cancelled: true, old_observation_refused: true, resumed_fresh_input_verified: true };
  }
  const paused = await interrupt("pause");
  console.log(JSON.stringify({ acceptance: "desktop-control", platform, architecture: process.arch, phase: "pause-passed" }));
  turnId = "stop";
  const stopped = await interrupt("stop");
  console.log(JSON.stringify({ acceptance: "desktop-control", platform, architecture: process.arch, phase: "stop-passed" }));
  const ended = await query(); delete ended.value;
  await controller.finishTurn(sessionId, turnId);
  await assert.rejects(call("set_value", { ...ended, value: "must not execute after finish" }), /ended|Observe|observation/i);
  assert.equal(controller.getStatus().watching, 0);
  assert.equal(controller.getStatus().active?.sessionId === sessionId, false);
  return { platform, architecture: process.arch, actual_fixture: true, query_window_frame_verified: true, pause: paused, stop: stopped, finish_turn_invalidates_observation: true, end_clears_target_and_watches: true, resumes_without_replay: true };
}
