/**
 * 模型清单的铺平与筛选。
 *
 * 单独成文件是为了能在 node 里直接 import 测：`store.ts` 顶上 `import { reactive }
 * from "vue"` 与一串无后缀的相对 import，在 electron / vite 之外跑不起来。
 * 同样的拆法见 `config-defaults.ts`、`turns.ts` 与 `computer-keys.ts`。
 */

/** 一个能选的模型：服务 + 模型名。 */
export interface ModelChoice {
  providerId: number;
  providerName: string;
  /** 发给服务的名字，不带能力/窗口标记。 */
  model: string;
  /** 上下文窗口（token），来自清单项的 `@` 段。0 表示不知道。 */
  context: number;
}

/**
 * store 里那份是 readonly() 包过的深只读副本，模板上拿到的就是这个形状；
 * 参数按它来声明，免得每个调用方都要 cast。
 */
type ProviderLike = {
  readonly id: number;
  readonly name: string;
  readonly enabled: boolean;
  readonly apiKeySet: boolean;
  readonly models: readonly string[];
};

/**
 * 拆一条清单项。只取名字与窗口——这个文件不该知道能力那一套，
 * 完整的解析在 model-roles.ts。
 */
function parseEntry(entry: string): { name: string; context: number } {
  let rest = entry.trim();
  let context = 0;
  const at = rest.indexOf("@");
  if (at >= 0) {
    const value = Number.parseInt(rest.slice(at + 1).trim(), 10);
    if (Number.isFinite(value) && value > 0) context = value;
    rest = rest.slice(0, at);
  }
  const hash = rest.indexOf("#");
  return { name: (hash < 0 ? rest : rest.slice(0, hash)).trim(), context };
}

/** 能用的模型服务：启用了、配了 Key、清单里至少有一个模型。 */
export function usable(provider: ProviderLike): boolean {
  return provider.enabled && provider.apiKeySet && provider.models.length > 0;
}

/** 把全部能用的模型服务铺成一张可选清单，给对话页顶部与配置页的选择器用。 */
export function modelChoices(providers: readonly ProviderLike[]): ModelChoice[] {
  const choices: ModelChoice[] = [];
  for (const provider of providers) {
    if (!usable(provider)) continue;
    for (const entry of provider.models) {
      // 清单项可能带能力与窗口标记（`名字#vision@131072`）。发给服务的是名字
      // 那一段——把整条送过去，上游只会回一句「没有这个模型」。
      const { name, context } = parseEntry(entry);
      choices.push({ providerId: provider.id, providerName: provider.name, model: name, context });
    }
  }
  return choices;
}

/**
 * 按关键词过滤模型清单。模型名与服务名都匹配，忽略大小写与首尾空格。
 *
 * 为什么必须有：一个 Azure 之类的服务能列出上百个模型，没有搜索时用户要在
 * 一个滚动的下拉里翻找自己每天用的那一个。空关键词返回原样。
 */
export function filterChoices(choices: ModelChoice[], keyword: string): ModelChoice[] {
  const needle = keyword.trim().toLowerCase();
  if (!needle) return choices;
  return choices.filter(
    (choice) =>
      choice.model.toLowerCase().includes(needle) ||
      choice.providerName.toLowerCase().includes(needle),
  );
}
