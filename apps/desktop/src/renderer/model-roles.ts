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

/** 把一条清单项拆成模型名与能力集。 */
export function parseModelMark(entry: string): { name: string; roles: ModelRole[] } {
  const index = entry.indexOf("#");
  if (index < 0) return { name: entry.trim(), roles: [] };
  const name = entry.slice(0, index).trim();
  const roles: ModelRole[] = [];
  for (const mark of entry.slice(index + 1).split(",")) {
    const role = mark.trim().toLowerCase() as ModelRole;
    // 不认识的标记丢掉而不是报错：清单是手写的。
    if (MODEL_ROLES.includes(role) && !roles.includes(role)) roles.push(role);
  }
  return { name, roles };
}

/** 把模型名与能力集拼回一条清单项。顺序固定，同一份配置每次写出来都一样。 */
export function formatModelMark(name: string, roles: ModelRole[]): string {
  const clean = name.trim();
  const marks = MODEL_ROLES.filter((role) => roles.includes(role));
  return marks.length === 0 ? clean : `${clean}#${marks.join(",")}`;
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
      found.push({ providerId: provider.id, providerName: provider.name, model: parsed.name });
    }
  }
  return found;
}
