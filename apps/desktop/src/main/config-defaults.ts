import { homedir } from "node:os";
import { join } from "node:path";

import type { AppConfig } from "./config.js";

/**
 * 配置的默认值与归一化。
 *
 * 单独成文件是为了能在 node 里直接 import 测：`config.ts` 顶上 `import { app }
 * from "electron"`，在 electron 之外 import 它会直接抛。同样的拆法见
 * `errors.ts` 与 `computer-keys.ts`。
 */

/**
 * 模型端点有默认值，配置页上默认不显示；要改的人在「高级」里改。
 */
export const DEFAULT_MODEL_BASE_URL = "https://api.openai.com/v1";

export const DEFAULT_CONFIG: AppConfig = {
  modelBaseUrl: DEFAULT_MODEL_BASE_URL,
  model: "",
  reasoningEffort: "medium",
  contextWindow: 0,
  profile: "on-write",
  retentionDays: 30,
  sandboxCommands: true,
  codeMode: false,
  enableComputerUse: false,
};

/**
 * 把地址的空值折回默认值。
 *
 * 展开默认值（`{ ...DEFAULT_CONFIG, ...raw }`）挡不住这件事：早于这个版本写下的
 * config.json 里这一项存的是空串，而空串会**盖掉**默认值——装机时看起来是默认的，
 * 老用户升级上来却是空的。清空输入框同理。地址不是可选项，空着的结果是每次请求
 * 都打到空地址上，而报错发生在上游、指不回这一格。
 */
export function normalizeConfig(config: AppConfig): AppConfig {
  return {
    ...config,
    modelBaseUrl: config.modelBaseUrl.trim() || DEFAULT_MODEL_BASE_URL,
    // 老配置里没有这一项。展开默认值能兜住「key 不存在」，但兜不住
    // 显式写进去的 undefined/null（升级路径上出现过），所以这里再钉一次：
    // **只有明确的 false 才算关**，其余一律当开着。安全开关的缺省必须是开。
    sandboxCommands: config.sandboxCommands !== false,
  };
}
