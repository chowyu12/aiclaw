/**
 * 模型能力标记的解析与筛选。
 *
 * 清单项写成 `名字#vision,image`，没有 `#` 的就是只做对话——旧版写下的清单
 * 原样可读。这份与 Go 侧 protocol/roles_marks.go 是同一套规则，改一边要改另一边。
 *
 * 单独成文件是为了能在 node 里直接测，同 model-choices.ts。
 */

export type ModelRole = "vision" | "stt" | "tts" | "image";

/** 全部角色，界面按它出勾选项与角色格。顺序与 Go 侧 KnownRoles 一致。 */
export const MODEL_ROLES: ModelRole[] = ["vision", "stt", "tts", "image"];

export const ROLE_LABELS: Record<ModelRole, string> = {
  vision: "看图",
  stt: "听写",
  tts: "朗读",
  image: "画图",
};

/** 配置页上每个角色格的说明。 */
export const ROLE_HINTS: Record<ModelRole, string> = {
  vision: "对话模型不认图时，用它把图转成文字再交给对话模型",
  stt: "把音频转成文字（transcribe_audio 工具）",
  tts: "把文字读成语音（speak 工具）",
  image: "按描述生成图片（generate_image 工具）",
};

/** 一条清单项拆开之后的样子。 */
export interface ParsedModel {
  name: string;
  roles: ModelRole[];
  /** 上下文窗口（token）。0 表示不知道。 */
  context: number;
}

/** 把一条清单项拆成模型名、能力集与上下文窗口。 */
export function parseModelMark(entry: string): ParsedModel {
  let rest = entry.trim();
  let context = 0;

  // 窗口先切：`@` 一定在最后一段，而模型名里不会有它。
  const at = rest.indexOf("@");
  if (at >= 0) {
    const value = Number.parseInt(rest.slice(at + 1).trim(), 10);
    if (Number.isFinite(value) && value > 0) context = value;
    rest = rest.slice(0, at);
  }

  const hash = rest.indexOf("#");
  if (hash < 0) return { name: rest.trim(), roles: [], context };
  const roles: ModelRole[] = [];
  for (const mark of rest.slice(hash + 1).split(",")) {
    const role = mark.trim().toLowerCase() as ModelRole;
    // 不认识的标记丢掉而不是报错：清单是手写的。
    if (MODEL_ROLES.includes(role) && !roles.includes(role)) roles.push(role);
  }
  return { name: rest.slice(0, hash).trim(), roles, context };
}

/**
 * 把拆开的部分拼回一条清单项。
 *
 * 顺序固定（能力按 MODEL_ROLES 排、窗口在最后），同一份配置每次写出来都一样——
 * 否则每次同步都会让配置库无谓地变动。与 Go 侧 FormatModelMark 是同一套规则。
 */
export function formatModelMark(parsed: {
  name: string;
  roles: ModelRole[];
  context?: number;
}): string {
  let entry = parsed.name.trim();
  if (!entry) return "";
  const marks = MODEL_ROLES.filter((role) => parsed.roles.includes(role));
  if (marks.length > 0) entry += `#${marks.join(",")}`;
  if (parsed.context && parsed.context > 0) entry += `@${parsed.context}`;
  return entry;
}

/** 一条清单项带不带某个能力。 */
export function modelHasRole(entry: string, role: ModelRole): boolean {
  return parseModelMark(entry).roles.includes(role);
}

/** 一个能担任某角色的模型。 */
export interface RoleCandidate {
  providerId: number;
  providerName: string;
  model: string;
  /** 上下文窗口（token）。0 表示不知道。 */
  context: number;
}

type ProviderLike = {
  readonly id: number;
  readonly name: string;
  readonly enabled: boolean;
  readonly apiKeySet: boolean;
  readonly models: readonly string[];
};

/**
 * 列出能担任某个角色的模型。
 *
 * 只看启用且配了 Key 的服务——与对话模型同一条标准：一个连不上的服务里的
 * 模型出现在下拉里，选了也只会在用的时候失败。
 */
export function roleCandidates(providers: readonly ProviderLike[], role: ModelRole): RoleCandidate[] {
  const found: RoleCandidate[] = [];
  for (const provider of providers) {
    if (!provider.enabled || !provider.apiKeySet) continue;
    for (const entry of provider.models) {
      const parsed = parseModelMark(entry);
      if (!parsed.roles.includes(role)) continue;
      found.push({
        providerId: provider.id,
        providerName: provider.name,
        model: parsed.name,
        context: parsed.context,
      });
    }
  }
  return found;
}

/**
 * 按模型名猜它是干什么的。与内核的 GuessRolesByName 是同一套词表，这边只用来
 * 提醒：用户手勾时把 asr 勾成朗读、tts 勾成听写，两格挨着、名字又长，实际连着
 * 错了三次，而选反的角色一用就是一个莫名其妙的 400。
 */
export function guessRoleByName(model: string): ModelRole | null {
  const lower = model.toLowerCase().split("/").pop() ?? "";
  const has = (...words: string[]) => words.some((word) => lower.includes(word));
  if (has("-tts", "tts-", "speech-0", "text-to-speech")) return "tts";
  if (has("-asr", "asr-", "whisper", "transcribe", "speech-to-text")) return "stt";
  if (has("-image", "image-", "imagen", "dall-e", "flux", "wan2", "stable-diffusion", "z-image")) return "image";
  if (has("-vl-", "-vl", "vision")) return "vision";
  return null;
}

/**
 * 一条清单项的标记与它的名字有没有明显冲突。只管听写 / 朗读这一对：它们互斥，
 * 而且是唯一会被勾反的一对——一个模型既能看图又能画图并不矛盾。
 * 返回一句给人看的话，没冲突返回空串。
 */
export function markConflict(entry: string): string {
  const parsed = parseModelMark(entry);
  const guess = guessRoleByName(parsed.name);
  if (guess === "tts" && parsed.roles.includes("stt") && !parsed.roles.includes("tts")) {
    return "名字像朗读（TTS）模型，却勾了听写";
  }
  if (guess === "stt" && parsed.roles.includes("tts") && !parsed.roles.includes("stt")) {
    return "名字像听写（ASR）模型，却勾了朗读";
  }
  return "";
}
