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
  model: string;
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

/** 能用的模型服务：启用了、配了 Key、清单里至少有一个模型。 */
export function usable(provider: ProviderLike): boolean {
  return provider.enabled && provider.apiKeySet && provider.models.length > 0;
}

/** 把全部能用的模型服务铺成一张可选清单，给对话页顶部与配置页的选择器用。 */
export function modelChoices(providers: readonly ProviderLike[]): ModelChoice[] {
  const choices: ModelChoice[] = [];
  for (const provider of providers) {
    if (!usable(provider)) continue;
    for (const model of provider.models) {
      choices.push({ providerId: provider.id, providerName: provider.name, model });
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
