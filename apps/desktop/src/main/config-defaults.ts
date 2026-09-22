import type { AppConfig } from "./config.js";

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
  reasoningEffort: "medium",
  contextWindow: 0,
  profile: "on-write",
  retentionDays: 30,
  sandboxCommands: true,
  codeMode: false,
};

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
    // 没选模型服务就是 0；非数字（旧配置里根本没有这一项）同样当 0。
    providerId: Number.isFinite(providerId) && providerId > 0 ? Math.trunc(providerId) : 0,
    // **只有明确的 false 才算关**，其余一律当开着。安全开关的缺省必须是开。
    sandboxCommands: config.sandboxCommands !== false,
  };
}
