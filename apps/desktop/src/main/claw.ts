import type { ConfigStore } from "./config.js";

/**
 * 宿主这边打内部平台的最小客户端。
 *
 * 只用于「浏览我有权限的东西」与「下载技能包」这类配置期操作。
 * 真正的能力调用不走这里——那是 claw-mcp 在会话里做的事。
 *
 * 所有列表都必须走**按调用者权限过滤**的那一组接口，不能用 /admin/ 的。
 * 后者返回整个租户的目录，用户会在界面上看到一堆点了就报 403 的东西。
 */

/** 网关统一信封：{code, message, data}。code 非 0 是业务失败，HTTP 仍是 200。 */
interface Envelope {
  code?: number;
  message?: string;
  data?: unknown;
}

export class ClawClient {
  private readonly store: ConfigStore;

  constructor(store: ConfigStore) {
    this.store = store;
  }

  async post<T>(path: string, payload: unknown, timeoutMs = 20_000): Promise<T> {
    const { clawUrl } = this.store.readConfig();
    const { clawToken } = this.store.readCredentials();
    if (!clawUrl.trim()) throw new Error("内部平台地址未配置");
    if (!clawToken.trim()) throw new Error("内部平台 BFF Key 未配置");

    let response: Response;
    try {
      response = await fetch(trimSlash(clawUrl) + path, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${clawToken}` },
        body: JSON.stringify(payload ?? {}),
        signal: AbortSignal.timeout(timeoutMs),
      });
    } catch (error) {
      throw new Error(`连接内部平台失败：${describe(error)}`);
    }

    const text = await response.text();
    if (!response.ok) {
      throw new Error(`内部平台返回 ${response.status}：${extract(text) || response.statusText}`);
    }
    let envelope: Envelope;
    try {
      envelope = JSON.parse(text) as Envelope;
    } catch {
      throw new Error("内部平台返回的不是合法 JSON");
    }
    if (envelope.code !== undefined && envelope.code !== 0) {
      throw new Error(envelope.message || "内部平台返回失败");
    }
    return (envelope.data ?? {}) as T;
  }

  async download(path: string, timeoutMs = 60_000): Promise<Buffer> {
    const { clawUrl } = this.store.readConfig();
    const { clawToken } = this.store.readCredentials();
    if (!clawUrl.trim()) throw new Error("内部平台地址未配置");

    const response = await fetch(trimSlash(clawUrl) + path, {
      headers: clawToken ? { Authorization: `Bearer ${clawToken}` } : {},
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (!response.ok) {
      // **把响应体带出来。** 内部平台的失败原因全在 body 里（「permission denied」
      // 「Skill 不存在或无可用版本」这类），只报一个 HTTP 400 等于把唯一的线索
      // 扔掉——那个状态码对着「为什么装不上」什么都说明不了。
      const detail = extract(await response.text().catch(() => ""));
      throw new Error(`下载失败：HTTP ${response.status}${detail ? `——${humanize(detail)}` : ""}`);
    }
    return Buffer.from(await response.arrayBuffer());
  }
}

function trimSlash(url: string): string {
  let end = url.length;
  while (end > 0 && url[end - 1] === "/") end -= 1;
  return url.slice(0, end);
}

function extract(body: string): string {
  try {
    const parsed = JSON.parse(body) as { message?: string; error?: { message?: string } };
    return parsed.message ?? parsed.error?.message ?? "";
  } catch {
    return body.slice(0, 200);
  }
}

/**
 * 把内部平台那几句英文错误翻成用户能照着做的话。
 *
 * 认不出来的原样带出去——原样总比换成一句笼统的「失败」强。
 */
function humanize(message: string): string {
  const text = message.trim();
  if (/permission denied/i.test(text)) {
    return "你没有这个技能包的下载授权。内部平台上技能包能不能看见和能不能下载是两回事：" +
      "列表里显示的是已发布的包，下载还要包的所有者给你授权。找包的所有者要一下。";
  }
  if (/skillhub_asset_missing/i.test(text)) {
    return "内部平台上这个包没有存档文件，下载不了。";
  }
  if (/不存在或无可用版本/.test(text)) {
    return "这个技能包没有已发布的版本。";
  }
  return text;
}

function describe(error: unknown): string {
  if (error instanceof Error) {
    return error.name === "TimeoutError" ? "超时" : error.message;
  }
  return String(error);
}
