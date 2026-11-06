import test from "node:test";
import assert from "node:assert/strict";
import { computerAppPolicy } from "../apps/desktop/src/main/computer-policy.ts";

test("curated app strategies match exact bundle identities without selectors or side effects", () => {
  for (const [app, id] of [["com.google.Chrome", "chrome"], ["com.microsoft.edgemac", "edge"], ["com.microsoft.VSCode", "vscode"], ["com.visualstudio.code.oss", "vscode"], ["com.tencent.WeWorkMac", "wecom"]]) {
    const policy = computerAppPolicy(app!, "darwin");
    assert.equal(policy.policy_id, id); assert.equal(policy.identity_match, "exact_bundle_id");
    assert.equal(policy.guidance_only, true); assert.ok(policy.instructions.length > 5);
    assert.ok(policy.sources.every(source => source.startsWith("https://")));
    assert.match(policy.selector_policy, /no hard-coded indexes/);
    assert.equal(computerAppPolicy(app! + ".attacker", "darwin").policy_id, "generic");
  }
  for (const app of ["Chrome", "Visual Studio Code", "企业微信", "123", "com.tencent.weworkmac"]) assert.equal(computerAppPolicy(app, "darwin").policy_id, "generic");
});

test("PID platforms never inherit app policies from a claimed bundle ID or display name", () => {
  for (const platform of ["win32", "linux"] as const) for (const app of ["123", "com.google.Chrome", "com.microsoft.VSCode", "com.tencent.WeWorkMac", "Microsoft Edge"]) {
    const policy = computerAppPolicy(app, platform);
    assert.equal(policy.policy_id, "generic"); assert.equal(policy.identity_match, "none");
    assert.equal(policy.platform, platform);
  }
});

test("backend capability reports preserve unsupported background input and platform constraints", () => {
  const mac = computerAppPolicy("com.apple.finder", "darwin");
  assert.match(mac.background_backend, /AX/); assert.ok(mac.background_actions.includes("single unmodified AXPress click"));
  const windows = computerAppPolicy("123", "win32");
  assert.match(windows.background_constraints[0]!, /standard Edit.*standard Button/);
  const linux = computerAppPolicy("123", "linux");
  assert.match(linux.background_backend, /X11/); assert.ok(linux.background_constraints.some(value => value.includes("Wayland")));
  for (const policy of [mac, windows, linux]) for (const action of ["coordinate click", "paste", "scroll", "drag"]) assert.ok(policy.unsupported_background.includes(action));
  for (const policy of [windows, linux]) for (const action of ["keyboard", "text input"]) assert.ok(policy.unsupported_background.includes(action));
  assert.ok(mac.background_actions.some(action => action.includes("focused-field proof")));
  assert.ok(mac.background_actions.some(action => action.includes("plain field or empty textarea")));
  assert.ok(mac.unsupported_background.includes("Cmd/Ctrl/Alt shortcuts"));
  assert.ok(mac.unsupported_background.includes("nonempty rich web editor text"));
  assert.ok(mac.background_constraints.some(constraint => constraint.includes("screenshot-bound") && constraint.includes("process generation")));
  assert.deepEqual(computerAppPolicy("123", "freebsd").background_actions, []);
});

test("messaging strategy separates drafting from an explicitly authorized send", () => {
  const instructions = computerAppPolicy("com.tencent.WeWorkMac", "darwin").instructions.join("\n");
  assert.match(instructions, /leave it unsent/); assert.match(instructions, /recipient identity/);
  assert.match(instructions, /explicitly requested sending/); assert.match(instructions, /never automatically retried/);
});
