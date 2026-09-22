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
 * 两个地址有默认值，配置页上默认不显示。
 *
 * 公司里这两个地址对所有人都是同一个，让每个人第一次打开应用都先去抄一遍 URL
 * 是在给装机加一道纯手工的步骤，抄错了还只能等第一次对话报 404 才发现。
 * 要改的人在配置页的「高级」里改。
 */
export const DEFAULT_MODEL_BASE_URL = "https://llm.example.internal/v1";
export const DEFAULT_CLAW_URL = "https://claw.example.internal";

export const DEFAULT_CONFIG: AppConfig = {
  modelBaseUrl: DEFAULT_MODEL_BASE_URL,
  model: "",
  reasoningEffort: "medium",
  contextWindow: 0,
  clawUrl: DEFAULT_CLAW_URL,
  profile: "on-write",
  retentionDays: 30,
  sandboxCommands: true,
  codeMode: false,
  enableComputerUse: false,
};

/**
 * 把两个地址的空值折回默认值。
 *
 * 展开默认值（`{ ...DEFAULT_CONFIG, ...raw }`）挡不住这件事：早于这个版本写下的
 * config.json 里这两项存的是空串，而空串会**盖掉**默认值——装机时看起来是默认的，
 * 老用户升级上来却是空的。清空输入框同理。地址不是可选项，空着的结果是每次请求
 * 都打到空地址上，而报错发生在上游、指不回这一格。
 */
export function normalizeConfig(config: AppConfig): AppConfig {
  return {
    ...config,
    modelBaseUrl: config.modelBaseUrl.trim() || DEFAULT_MODEL_BASE_URL,
    clawUrl: config.clawUrl.trim() || DEFAULT_CLAW_URL,
    // 老配置里没有这一项。展开默认值能兜住「key 不存在」，但兜不住
    // 显式写进去的 undefined/null（升级路径上出现过），所以这里再钉一次：
    // **只有明确的 false 才算关**，其余一律当开着。安全开关的缺省必须是开。
    sandboxCommands: config.sandboxCommands !== false,
  };
}

/**
 * 解析「用户对能力的显式选择」文件。
 *
 * 单独成纯函数是为了能在 node 里测：它唯一的难点是**兼容旧格式**，
 * 而兼容写错了不会报错——升级上来的用户会发现自己关掉的能力一次性
 * 全开回来了，还以为是同步把它们打开的。
 *
 * 两种形状：
 *   - v0.1.4：`["data-api:1", ...]`，一个「关掉的键」数组；
 *   - 之后：`{"data-api:1": false, "web-search:12": true}`，双向选择。
 *     记双向是因为联网搜索那类里有些候选默认是关的，手动打开也要记住。
 */
export function parseCapabilityChoices(raw: unknown): Record<string, boolean> {
  if (Array.isArray(raw)) {
    return Object.fromEntries(
      raw.filter((key): key is string => typeof key === "string").map((key) => [key, false]),
    );
  }
  if (raw && typeof raw === "object") {
    const out: Record<string, boolean> = {};
    for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
      if (typeof value === "boolean") out[key] = value;
    }
    return out;
  }
  return {};
}
