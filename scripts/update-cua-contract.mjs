/** Run after pinning and staging a new upstream Driver. This reads its own
 * describe schemas without starting a daemon, permission prompt or desktop input. */
import { readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
const repo = fileURLToPath(new URL("../", import.meta.url));
const binary = join(
  repo,
  "apps/desktop/cua-driver",
  process.platform === "win32" ? "cua-driver.exe" : "cua-driver",
);
const manifest = JSON.parse(
  readFileSync(join(repo, "tools/cua-driver/runtime.json"), "utf8"),
);
if (
  !execFileSync(binary, ["--version"], { encoding: "utf8" }).includes(
    manifest.version,
  )
)
  throw Error("Stage the pinned Driver before updating its contract");
const names = [
  "list_apps",
  "list_windows",
  "get_window_state",
  "invoke_menu",
  "click",
  "double_click",
  "right_click",
  "type_text",
  "press_key",
  "hotkey",
  "set_value",
  "scroll",
  "drag",
  "run_steps",
  "verify_state",
  "zoom",
  "check_permissions",
  "launch_app",
];
const reads = new Set([
  "list_apps",
  "list_windows",
  "get_window_state",
  "verify_state",
  "zoom",
  "check_permissions",
]);
const specs = names.map((action) => {
  const output = execFileSync(binary, ["describe", action], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
  });
  const [description, raw] = output
    .split("description:\n")[1]
    .split("\ninput_schema:\n");
  const schema = JSON.parse(raw),
    backgroundDelivery = Object.hasOwn(schema.properties, "delivery_mode");
  for (const key of [
    "session",
    "delivery_mode",
    "foreground_fallback",
    "debug_image_out",
    "screenshot_out_file",
    "prompt",
    "probe_direct_capture",
    "additional_arguments",
    "webkit_inspector_port",
    "scope",
    "target",
    "capture_mode",
  ])
    delete schema.properties[key];
  if (
    !["list_apps", "list_windows", "launch_app", "check_permissions"].includes(
      action,
    )
  ) {
    for (const key of ["app", "window"]) delete schema.properties[key];
    schema.required = [
      ...new Set([...(schema.required ?? []), "pid", "window_id"]),
    ].sort();
  }
  for (const key of ["pid", "window_id"])
    if (schema.properties[key]) schema.properties[key].minimum = 1;
  if (action === "get_window_state")
    schema.properties.screenshot_format = { type: "string", enum: ["png"] };
  if (action === "run_steps") {
    const steps = schema.properties.steps;
    steps.description =
      "Ordered {tool,args,wait_for?,expect?} actions on this exact window. Stop at the first failure. Never set per-step routing, delivery mode or session.";
    steps.items.additionalProperties = false;
    steps.items.properties.tool.enum = steps.items.properties.tool.enum.filter(
      (name) => name !== "move_cursor",
    );
  }
  return {
    name: `computer_${action}`,
    action,
    readOnly: reads.has(action),
    description:
      description.trim().split("\n\n")[0].slice(0, 1600) +
      " Background preferred; the host can temporarily use foreground after an explicit background refusal. Use exact pid and window_id from computer_list_windows. Prefer computer_run_steps for known sequential actions with one final observe; verify semantic effects, never replay uncertain input.",
    schema,
    platforms: ["darwin", "windows", "linux"],
    backgroundDelivery,
  };
});
specs.push({
  name:"computer_wait_new_window", action:"wait_new_window", readOnly:true,
  description:"Wait up to timeout_ms for one new ordinary window of the given pid, excluding the window IDs already returned by computer_list_windows. Return candidates; if multiple appear, inspect them before acting. Never assume an off-screen or helper window is the document.",
  schema:{type:"object",additionalProperties:false,required:["pid","exclude_window_ids"],properties:{pid:{type:"integer",minimum:1},exclude_window_ids:{type:"array",items:{type:"integer",minimum:1}},timeout_ms:{type:"integer",minimum:100,maximum:10000}}},
  platforms:["darwin","windows","linux"],backgroundDelivery:false,
});
writeFileSync(
  join(repo, "tools/claw-agent/internal/agent/computer-tools.json"),
  JSON.stringify(specs, null, 2) + "\n",
);
