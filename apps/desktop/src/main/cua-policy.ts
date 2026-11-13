import { readFileSync } from "node:fs";
export interface CuaToolSpec {
  name: string;
  action: string;
  readOnly: boolean;
  backgroundDelivery?: boolean;
  schema: { properties: Record<string, any>; required?: string[] };
}
export const cuaToolSpecs: CuaToolSpec[] = JSON.parse(
  readFileSync(
    new URL(
      import.meta.url.endsWith(".ts")
        ? "../../../../tools/claw-agent/internal/agent/computer-tools.json"
        : "../../cua-driver/tools.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
const actions = new Set([
  "click",
  "double_click",
  "right_click",
  "set_value",
  "type_text",
  "press_key",
  "hotkey",
  "scroll",
  "drag",
]);
const checks = new Set([
  "role",
  "name",
  "text",
  "gone",
  "value",
  "value_contains",
  "enabled",
  "selected",
  "timeout_ms",
]);
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw Error("Cua arguments must be an object");
  return value as Record<string, unknown>;
}
function fields(
  args: Record<string, unknown>,
  allowed: Record<string, any>,
  required: string[] = [],
): void {
  for (const key of Object.keys(args)) {
    if (!Object.hasOwn(allowed, key))
      throw Error(`Unsupported Cua argument: ${key}`);
    const schema = allowed[key],
      value = args[key];
    if (
      (schema.type === "string" && typeof value !== "string") ||
      (schema.type === "boolean" && typeof value !== "boolean") ||
      (schema.type === "integer" && !Number.isSafeInteger(value)) ||
      (schema.type === "number" &&
        (typeof value !== "number" || !Number.isFinite(value)))
    )
      throw Error(`Invalid Cua argument: ${key}`);
    if (schema.enum && !schema.enum.includes(value))
      throw Error(`Invalid Cua argument: ${key}`);
  }
  for (const key of required)
    if (args[key] === undefined || args[key] === null)
      throw Error(`Missing Cua argument: ${key}`);
}
/** The only dispatch path: exact window targets; no desktop, scripts, clipboard,
 * permission UI, foreground escalation, model sessions, file output or launch argv. */
export function backgroundCuaArguments(
  action: string,
  input: unknown,
): Record<string, unknown> {
  const spec = cuaToolSpecs.find((spec) => spec.action === action);
  if (!spec) throw Error(`Unsupported Cua operation: ${action}`);
  const args = { ...object(input) };
  fields(args, spec.schema.properties, spec.schema.required);
  if (
    !["list_apps", "list_windows", "wait_new_window", "check_permissions", "launch_app"].includes(
      action,
    )
  ) {
    if (
      !Number.isSafeInteger(args.pid) ||
      Number(args.pid) <= 0 ||
      args.pid === process.pid ||
      !Number.isSafeInteger(args.window_id) ||
      Number(args.window_id) <= 0
    )
      throw Error(
        "An explicit non-host pid and window_id from computer_list_windows are required",
      );
  }
  if (action === "wait_new_window") {
    if (!Number.isSafeInteger(args.pid) || Number(args.pid) <= 0 || args.pid === process.pid ||
        !Array.isArray(args.exclude_window_ids) || args.exclude_window_ids.some(id => !Number.isSafeInteger(id) || id <= 0) ||
        (args.timeout_ms !== undefined && (!Number.isSafeInteger(args.timeout_ms) || Number(args.timeout_ms) < 100 || Number(args.timeout_ms) > 10000)))
      throw Error("Invalid window wait arguments");
  }
  if (action === "invoke_menu" && (!Array.isArray(args.path) || args.path.length < 1 || args.path.length > 16 || args.path.some(part => typeof part !== "string" || !part.trim() || part.length > 200)))
    throw Error("Menu path requires 1–16 exact labels");
  if (action === "launch_app") {
    if (
      typeof args.bundle_id !== "string" ||
      !/^[a-z0-9-]+(?:\.[a-z0-9-]+)+$/i.test(args.bundle_id) ||
      /aiclaw|electron|trycua/i.test(args.bundle_id)
    )
      throw Error("A non-host bundle_id is required");
  }
  if (action === "check_permissions") args.prompt = false;
  if (action === "run_steps") {
    if (
      !Array.isArray(args.steps) ||
      !args.steps.length ||
      args.steps.length > 64
    )
      throw Error("Cua batch requires 1–64 steps");
    args.steps = args.steps.map((raw) => {
      const step = object(raw);
      // Normalize to one unambiguous spelling; shorthand cannot hide extra actions.
      for (const key of Object.keys(step))
        if (!["tool", "args", "wait_for", "expect", "timeout_ms"].includes(key))
          throw Error(`Unsupported Cua step field: ${key}`);
      const normalized: Record<string, unknown> = {};
      if (step.tool !== undefined) {
        if (typeof step.tool !== "string" || !actions.has(step.tool))
          throw Error("Unsupported Cua batch tool");
        const tool = cuaToolSpecs.find((spec) => spec.action === step.tool)!;
        const nested = { ...object(step.args ?? {}) };
        fields(nested, {
          ...tool.schema.properties,
          role: { type: "string" },
          name: { type: "string" },
          nth: { type: "integer" },
        });
        for (const key of ["pid", "window_id", "app", "window"])
          if (key in nested)
            throw Error("Cua batch steps must use the batch's exact window");
        normalized.tool = step.tool;
        // Leave delivery mode unset inside a batch. Cua defaults to background
        // and can retry only a step it explicitly refused as background-only.
        normalized.args = nested;
      } else if (step.args !== undefined)
        throw Error("Cua step args require a tool");
      for (const key of ["wait_for", "expect"])
        if (step[key] !== undefined) {
          const value = step[key];
          const values =
            key === "expect"
              ? Array.isArray(value)
                ? value
                : [value]
              : [value];
          if (!values.length || values.length > 4)
            throw Error("Cua batch requires 1–4 checks");
          for (const value of values)
            for (const key of Object.keys(object(value)))
              if (!checks.has(key))
                throw Error(`Unsupported Cua check field: ${key}`);
          normalized[key] = key === "expect" ? values : values[0];
        }
      if (step.timeout_ms !== undefined) {
        if (
          !Number.isSafeInteger(step.timeout_ms) ||
          Number(step.timeout_ms) < 0 ||
          Number(step.timeout_ms) > 10_000
        )
          throw Error("Invalid Cua step timeout");
        normalized.timeout_ms = step.timeout_ms;
      }
      if (!normalized.tool && !normalized.wait_for && !normalized.expect)
        throw Error("Empty Cua step");
      return normalized;
    });
    // Intermediate plain/shift arrow keys in a batch with a final observation
    // do not need an individual window-change wait. Preserve explicit waits,
    // predicates and modified shortcuts that may open menus or dialogs.
    if (args.observe !== undefined) {
      const steps = args.steps as Record<string, unknown>[];
      for (let i = 0; i + 1 < steps.length; i++) {
        const step = steps[i]!;
        const next = steps[i + 1]!;
        const input = step.args as Record<string, unknown> | undefined;
        if (step.tool !== "press_key" || next.tool !== "press_key" || !input ||
            step.expect !== undefined || step.wait_for !== undefined || input.post_action_wait !== undefined ||
            !["left", "right", "up", "down"].includes(String(input.key).toLowerCase())) continue;
        const modifiers = input.modifiers;
        if (modifiers !== undefined && (!Array.isArray(modifiers) || modifiers.some(value => String(value).toLowerCase() !== "shift"))) continue;
        input.post_action_wait = "none";
      }
    }
    if (args.observe !== undefined) {
      const observe = object(args.observe);
      const schema = cuaToolSpecs.find(
        (spec) => spec.action === "get_window_state",
      )!.schema;
      fields(observe, schema.properties);
      if ("pid" in observe || "window_id" in observe)
        throw Error("Cua observation must use the batch's window");
      args.observe = { ...observe, screenshot_format: "png" };
    }
    args.foreground_fallback = true;
  }
  if (spec.backgroundDelivery && action !== "run_steps") args.delivery_mode = "background";
  if (action === "get_window_state") args.screenshot_format = "png";
  return args;
}
