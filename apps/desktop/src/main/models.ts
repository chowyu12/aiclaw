import type { ConfigStore } from "./config.js";

/**
 * 从 airouter 拉可用模型。
 *
 * 两个接口拼起来用（都是 `Authorization: Bearer sk-...`，同一把 LLM Key）：
 *   GET /v1/models          —— 能调的模型名，也就是请求里 `model` 字段该填什么
 *   GET /v1/model-profiles  —— 上下文窗口、最大输出
 * 两边按 `model_key` 关联。
 *
 * 分两次取是 airouter 的刻意设计：/v1/models 只带轻量的 profile 链接，完整档案
 * 单独取。profiles 拿不到不算失败——退化成「只有模型名、窗口未知」，
 * 比整个列表空着强。
 */

export interface ModelChoice {
  /** 调用时填进 `model` 的名字。 */
  id: string;
  /** 渠道名，例如 OpenAI / Claude / DeepSeek。 */
  ownedBy: string;
  /** 上下文窗口（token）。0 表示这个模型没关联档案，窗口未知。 */
  contextWindow: number;
  maxOutputTokens: number;
  /** 合规等级名，例如「境内合规」。展示用，帮用户判断能不能喂内部数据。 */
  securityLevel: string;
}

interface ModelsResponse {
  data?: {
    id?: string;
    owned_by?: string;
    model_key?: string;
    security_level_name?: string;
  }[];
}

interface ProfilesResponse {
  data?: {
    model_key?: string;
    context_window?: number;
    max_output_tokens?: number;
  }[];
}

/** 列表变化不频繁，缓存一会儿，免得每次开下拉都打一次网络。 */
const CACHE_TTL_MS = 5 * 60 * 1000;
let cache: { key: string; at: number; models: ModelChoice[] } | null = null;

export class ModelCatalog {
  private readonly store: ConfigStore;

  constructor(store: ConfigStore) {
    this.store = store;
  }

  async list(force = false): Promise<ModelChoice[]> {
    const { modelBaseUrl } = this.store.readConfig();
    const { llmKey } = this.store.readCredentials();
    if (!modelBaseUrl.trim()) throw new Error("模型端点未配置");
    if (!llmKey.trim()) throw new Error("模型 Key 未配置");

    // 缓存按「端点 + Key」分桶：换了 Key 能看到的模型就不一样了，
    // 共用一份缓存会让用户看到上一把 Key 的列表。
    const key = `${modelBaseUrl} ${llmKey}`;
    if (!force && cache && cache.key === key && Date.now() - cache.at < CACHE_TTL_MS) {
      return cache.models;
    }

    const base = trimTrailingSlash(modelBaseUrl);
    const models = await fetchJson<ModelsResponse>(`${base}/models`, llmKey);

    // profiles 只是锦上添花，失败就当没有——窗口显示为未知，模型照样能选。
    let profiles: ProfilesResponse = {};
    try {
      profiles = await fetchJson<ProfilesResponse>(`${base}/model-profiles`, llmKey);
    } catch {
      profiles = {};
    }
    const byModelKey = new Map<string, { context: number; maxOutput: number }>();
    for (const profile of profiles.data ?? []) {
      if (!profile.model_key) continue;
      byModelKey.set(profile.model_key, {
        context: profile.context_window ?? 0,
        maxOutput: profile.max_output_tokens ?? 0,
      });
    }

    const list: ModelChoice[] = [];
    for (const entry of models.data ?? []) {
      if (!entry.id) continue;
      const profile = entry.model_key ? byModelKey.get(entry.model_key) : undefined;
      list.push({
        id: entry.id,
        ownedBy: entry.owned_by ?? "",
        contextWindow: profile?.context ?? 0,
        maxOutputTokens: profile?.maxOutput ?? 0,
        securityLevel: entry.security_level_name ?? "",
      });
    }
    list.sort((a, b) => a.id.localeCompare(b.id));

    cache = { key, at: Date.now(), models: list };
    return list;
  }

  /** 换 Key 或换端点之后调用，别让用户看着上一把 Key 的列表。 */
  static invalidate(): void {
    cache = null;
  }
}

function trimTrailingSlash(url: string): string {
  let end = url.length;
  while (end > 0 && url[end - 1] === "/") end -= 1;
  return url.slice(0, end);
}

async function fetchJson<T>(url: string, apiKey: string): Promise<T> {
  // 超时必须自己设：fetch 默认不超时，airouter 不可达时下拉会一直转。
  let response: Response;
  try {
    response = await fetch(url, {
      headers: { Authorization: `Bearer ${apiKey}`, Accept: "application/json" },
      signal: AbortSignal.timeout(15_000),
    });
  } catch (error) {
    throw new Error(`连接模型服务失败：${describe(error)}`);
  }
  if (!response.ok) {
    // 把上游的话带出来。401/403 在这里最常见，都是 Key 的问题，
    // 直接说出来比让用户猜「为什么列表是空的」强。
    const body = await response.text().catch(() => "");
    const detail = extractMessage(body) || response.statusText;
    throw new Error(`模型服务返回 ${response.status}：${detail}`);
  }
  return (await response.json()) as T;
}

function extractMessage(body: string): string {
  try {
    const parsed = JSON.parse(body) as { error?: { message?: string }; message?: string };
    return parsed.error?.message ?? parsed.message ?? "";
  } catch {
    return body.slice(0, 200);
  }
}

function describe(error: unknown): string {
  if (error instanceof Error) {
    return error.name === "TimeoutError" ? "超时（15 秒）" : error.message;
  }
  return String(error);
}
