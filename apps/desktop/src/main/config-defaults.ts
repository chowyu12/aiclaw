import type { AppConfig, RoleConfig, RoleModelConfig } from "./config.js";

/** 一个角色都没配的样子。 */
const NO_ROLE: RoleModelConfig = { providerId: 0, model: "" };
const NO_ROLES: RoleConfig = {
  vision: { ...NO_ROLE },
  stt: { ...NO_ROLE },
  tts: { ...NO_ROLE },
  image: { ...NO_ROLE },
};

/**
 * 配置的默认值与归一化。
 *
 * 单独成文件是为了能在 node 里直接 import 测：`config.ts` 顶上 `import { app }
 * from "electron"`，在 electron 之外 import 它会直接抛。同样的拆法见
 * `errors.ts` 与 `computer-keys.ts`。
 */

export const DEFAULT_CONFIG: AppConfig = {
  providerId: 0,
  model: "",
  roles: NO_ROLES,
  reasoningEffort: "medium",
  contextWindow: 0,
  profile: "on-write",
  retentionDays: 30,
  sandboxCommands: true,
  codeMode: false,
};

/** 补全四个角色，并把每个角色里不合法的 providerId 钉成 0。 */
function normalizeRoles(roles: RoleConfig | undefined): RoleConfig {
  const one = (role: RoleModelConfig | undefined): RoleModelConfig => {
    const id = Number(role?.providerId);
    const valid = Number.isFinite(id) && id > 0;
    return {
      providerId: valid ? Math.trunc(id) : 0,
      // 没有服务就没有模型：留着一个孤零零的模型名，界面会显示一个选不中的值。
      model: valid ? String(role?.model ?? "") : "",
    };
  };
  return {
    vision: one(roles?.vision),
    stt: one(roles?.stt),
    tts: one(roles?.tts),
    image: one(roles?.image),
  };
}

/**
 * 把旧配置文件里没有、或者被手改坏的字段钉回合法值。
 *
 * 展开默认值（`{ ...DEFAULT_CONFIG, ...raw }`）能兜住「key 不存在」，
 * 但兜不住显式写进去的 undefined/null 或类型不对的值（升级路径上出现过）。
 */
export function normalizeConfig(config: AppConfig): AppConfig {
  const providerId = Number(config.providerId);
  return {
    ...config,
    // 角色是后加的：旧 config.json 里根本没有这一项，缺了要补全四个角色，
    // 否则界面读 roles.vision.model 会在第一行就抛。
    roles: normalizeRoles(config.roles),
    // 没选模型服务就是 0；非数字（旧配置里根本没有这一项）同样当 0。
    providerId: Number.isFinite(providerId) && providerId > 0 ? Math.trunc(providerId) : 0,
    // **只有明确的 false 才算关**，其余一律当开着。安全开关的缺省必须是开。
    sandboxCommands: config.sandboxCommands !== false,
  };
}
