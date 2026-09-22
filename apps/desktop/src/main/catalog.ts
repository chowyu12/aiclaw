import type { ClawCapability, ConfigStore } from "./config.js";

/**
 * 从内部平台拉「当前这把 BFF Key 有权限用的」对象，合成本地的能力列表。
 *
 * 用的全是按调用者权限过滤的那组接口，不是 /admin/ 的：
 *   数据 API    POST /api/v1/data-apis/list        （asset_grants + 部门路径过滤）
 *   API 服务    POST /api/v1/api-operations/search  （按 api_source 的 read 授权过滤）
 *   语料库      POST /api/v1/corpora/list           （Scope=visible，服务端写死）
 *   知识库      —— 见下面 knowledgeCatalog 的说明
 *
 * 可见 ≠ 可调用：内部平台的可见性按资产授权算，执行时还会再查一层（数据 API 查
 * 云鉴表权限，API 服务按单个操作查）。所以这里把明确不可用的过滤掉，
 * 把「未知」的留下——列表接口在旧版 Platform 上拿不到这两个判定字段，
 * 一律当成不可用会把整张表清空。
 */

export type CapabilityKind = ClawCapability["type"];

export interface CatalogItem {
  type: CapabilityKind;
  id: string;
  name: string;
  description: string;
  /** 归属：API 服务给 API 源名，数据 API 给标签。 */
  group?: string;
  /**
   * 第一次发现时默认开不开。不给就是开。
   *
   * 只有联网搜索用到它：那一类是按关键词搜出来的，「搜索」这个词在企业 API
   * 目录里能命中一堆业务检索接口，全默认打开就会给模型挂上一排它用不上的
   * search_xxx。所以只有明显是通用联网搜索的那条默认开，其余列出来但关着。
   */
  defaultEnabled?: boolean;
}

const PAGE_SIZE = 200;

/** 只依赖「能 POST」，测试里可以塞一个假的进来。 */
export interface ClawPoster {
  post<T>(path: string, payload: unknown): Promise<T>;
}

/**
 * 各类能力对应的列表接口。全是按调用者权限过滤的那组，没有 /admin/。
 *
 * 注意粒度：数据 API 与 API 服务列的是**容器**（云鉴数据源 / API 源），
 * 不是一个个对象。用户按库、按服务授权，也按库、按服务开关；
 * 库里新增了 API、服务里新增了接口，下次开会话由 claw-mcp 自动带上，
 * 不用回来重新配。展开在 claw-mcp 那边做（internal/provider 的 expand）。
 *
 * 顺带绕开一个坑：跨源搜操作的 /api-operations/search 在 Platform 那层写死了
 * 「keyword is required」，空关键词直接 400，没法用来「打开面板就看到有什么」。
 */
export const CATALOG_ROUTES = {
  "data-api": "/api/v1/data-sources/list",
  "api-operation": "/api/v1/api-sources/list",
  corpus: "/api/v1/corpora/list",
} as const;

/** 一次同步的结果，给界面显示用。 */
export interface SyncResult {
  capabilities: ClawCapability[];
  /** 哪几类没拉到，以及原因。拉不到的那一类保留上一次的结果。 */
  failures: { type: CapabilityKind; reason: string }[];
  syncedAt: string;
}

export function capabilityKey(type: string, id: string): string {
  return `${type}:${id}`;
}

/**
 * 把「内部平台上现在有什么」和「用户开过关过哪些」合成最终的能力列表。
 *
 * **默认开。** 配好内部平台之后，你有权限的东西就该能用，让人再逐个去点一遍
 * 「添加」只是把内部平台那边已经做过的授权又抄一遍。唯一的例外是联网搜索里
 * 那些不太像的候选（见 CatalogItem.defaultEnabled）。
 *
 * 用户的选择记在 choices 里并且跨同步存活——同步一次就自己变回去的话，
 * 这个功能比不做还糟。记的是双向选择：默认关的被手动打开，同样要记住。
 *
 * 某一类拉取失败时，**保留上一次的结果**而不是当成「内部平台上没有了」：
 * 网络抖一下就把用户的能力清空，下一个会话什么都调不动，而原因在别处。
 */
export function composeCapabilities(
  discovered: Map<CapabilityKind, CatalogItem[]>,
  previous: ClawCapability[],
  choices: Record<string, boolean>,
): ClawCapability[] {
  const out: ClawCapability[] = [];
  const seen = new Set<string>();

  for (const type of CAPABILITY_KINDS) {
    const items = discovered.get(type);
    if (items === undefined) {
      // 这一类没拉到，原样留着上一次的。
      for (const kept of previous.filter((item) => item.type === type)) {
        const key = capabilityKey(kept.type, kept.id);
        if (seen.has(key)) continue;
        seen.add(key);
        // 保留上次的结果时，开关也以上次为准（用户的选择优先）。
        out.push({ ...kept, enabled: choices[key] ?? kept.enabled });
      }
      continue;
    }
    for (const item of items) {
      const key = capabilityKey(item.type, item.id);
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({
        type: item.type,
        id: item.id,
        name: item.name,
        description: item.description,
        // 用户明确选过就听他的；没选过用这一项的默认值（一般是开）。
        enabled: choices[key] ?? item.defaultEnabled ?? true,
      });
    }
  }
  return out;
}

/** 四类能力。顺序决定界面上的分组顺序。 */
export const CAPABILITY_KINDS: CapabilityKind[] = [
  "data-api",
  "api-operation",
  "knowledge",
  "corpus",
  "web-search",
];

export class CapabilityCatalog {
  private readonly store: ConfigStore;
  private readonly claw: ClawPoster;

  constructor(store: ConfigStore, claw: ClawPoster) {
    this.store = store;
    this.claw = claw;
  }

  /**
   * 把内部平台上你有权限的能力全拉一遍，合成新的能力列表并落盘。
   *
   * 不在会话启动时做：那会把一次网络往返加进每次开会话的路径上，
   * 内部平台慢一点开会话就跟着慢，内部平台不通就开不了会话。改成应用启动时后台同步、
   * 结果写进本地文件，会话启动照旧只读文件——快，而且离线能用上次的。
   */
  async syncAll(): Promise<SyncResult> {
    const discovered = new Map<CapabilityKind, CatalogItem[]>();
    const failures: SyncResult["failures"] = [];

    for (const type of CAPABILITY_KINDS) {
      try {
        discovered.set(type, await this.fetch(type));
      } catch (error) {
        // 单类失败不影响其余：一个接口没权限不该让整次同步白跑。
        // 原因里带上接口路径——只说「失败」的话，从截图里分不出是地址错、
        // 没权限，还是这一类对象本身有问题。
        const route = CATALOG_ROUTES[type as keyof typeof CATALOG_ROUTES];
        const reason = error instanceof Error ? error.message : String(error);
        failures.push({ type, reason: route ? `${reason}（${route}）` : reason });
      }
    }

    const capabilities = composeCapabilities(
      discovered,
      this.store.readCapabilities(),
      this.store.readCapabilityChoices(),
    );
    this.store.writeCapabilities(capabilities);
    return { capabilities, failures, syncedAt: new Date().toISOString() };
  }

  /** 开关一项。这个选择会被记住，之后每次同步都保持它。 */
  setEnabled(type: CapabilityKind, id: string, enabled: boolean): ClawCapability[] {
    const key = capabilityKey(type, id);
    const choices = this.store.readCapabilityChoices();
    choices[key] = enabled;
    this.store.writeCapabilityChoices(choices);

    const next = this.store
      .readCapabilities()
      .map((item) =>
        capabilityKey(item.type, item.id) === key ? { ...item, enabled } : item,
      );
    return this.store.writeCapabilities(next);
  }

  private async fetch(type: CapabilityKind): Promise<CatalogItem[]> {
    switch (type) {
      case "data-api":
        return this.dataAPIs();
      case "api-operation":
        return this.apiOperations();
      case "corpus":
        return this.corpora();
      case "knowledge":
        return knowledgeCatalog();
      case "web-search":
        return this.webSearch();
      default:
        return [];
    }
  }

  /**
   * 联网搜索：在当前员工有权调用的 API 操作里，按关键词找搜索引擎接口。
   *
   * 这个接口本来就在某个 API 源里（公司里是 ai-internal-bff 的阿里云联网搜索），
   * 但那个源有一百多个接口、一挂就落进目录模式，模型要先想到「去目录里搜一个
   * 搜索接口」才用得上。把它单拎成一个能力，挂出来就是一个叫 search 的工具。
   *
   * 用 /api-operations/search 而不是别的：它**强制要 keyword**（Platform 那层
   * 写死的），别处都因此用不了它——而这里正好有关键词。
   */
  private async webSearch(): Promise<CatalogItem[]> {
    const found = new Map<string, CatalogItem>();
    const errors: string[] = [];
    for (const keyword of WEB_SEARCH_KEYWORDS) {
      let rows: OperationRow[] = [];
      try {
        const data = await this.claw.post<{ list?: OperationRow[] }>(WEB_SEARCH_ROUTE, {
          keyword,
          page: 1,
          page_size: 20,
        });
        rows = data.list ?? [];
      } catch (error) {
        // 某个关键词搜失败不该让整类失败（换个词可能就有），但**原因不能吞掉**：
        // 全部失败时这一栏是空的，而空的原因是「接口不通」还是「确实没有
        // 这样的接口」，用户完全看不出来——那正是最难查的那种故障。
        errors.push(`${keyword}：${error instanceof Error ? error.message : String(error)}`);
        continue;
      }
      for (const item of mapOperations(rows)) {
        if (!found.has(item.id)) found.set(item.id, item);
      }
    }
    if (found.size === 0 && errors.length > 0) {
      throw new Error(errors.join("；"));
    }
    // 最像的排前面。分数相同时按 id 稳定排序，免得每次同步顺序都在跳。
    const ranked = [...found.values()].sort(
      (a, b) => scoreWebSearch(b) - scoreWebSearch(a) || Number(a.id) - Number(b.id),
    );
    return markWebSearchDefaults(ranked).slice(0, WEB_SEARCH_MAX);
  }

  /** 数据 API 按**库**列：一个云鉴数据源一条。 */
  private async dataAPIs(): Promise<CatalogItem[]> {
    const data = await this.claw.post<{ list?: DatabaseRow[] }>(CATALOG_ROUTES["data-api"], {
      page: 1,
      page_size: PAGE_SIZE,
    });
    return mapDatabases(data.list ?? []);
  }

  /** API 服务按**源**列：一个 API 源一条。 */
  private async apiOperations(): Promise<CatalogItem[]> {
    const data = await this.claw.post<{ list?: SourceRow[] }>(CATALOG_ROUTES["api-operation"], {
      page: 1,
      page_size: PAGE_SIZE,
    });
    return mapSources(data.list ?? []);
  }

  private async corpora(): Promise<CatalogItem[]> {
    const data = await this.claw.post<{ list?: CorpusRow[] }>(CATALOG_ROUTES.corpus, {
      status: "published",
      page: 1,
      page_size: PAGE_SIZE,
    });
    return mapCorpora(data.list ?? []);
  }
}

/**
 * 云鉴数据源 → 一条可开关的能力。
 *
 * 带上库里有几个 API、几张表：一个 0 个 API 的库加进来没有任何工具，
 * 数字摆在这儿用户自己就能判断。
 */
export function mapDatabases(rows: DatabaseRow[]): CatalogItem[] {
  return rows
    .filter((row) => row.id !== undefined)
    .map((row) => ({
      type: "data-api" as const,
      id: String(row.id),
      name: row.name ?? `数据源 ${row.id}`,
      description: row.description ?? "",
      group: [row.type, row.data_api_count ? `${row.data_api_count} 个 API` : "0 个 API"]
        .filter(Boolean)
        .join(" · "),
    }));
}

/**
 * API 源 → 一条可开关的能力。
 *
 * 源里只有只读接口会挂给模型；写类的（非 GET/HEAD）由 claw-mcp 按运行契约
 * 逐个跳过。所以这里不需要任何「写」的标记——挂上去的一定是只读的。
 */
export function mapSources(rows: SourceRow[]): CatalogItem[] {
  return rows
    .filter((row) => row.id !== undefined)
    .map((row) => ({
      type: "api-operation" as const,
      id: String(row.id),
      name: row.name ?? `API 源 ${row.id}`,
      description: row.description ?? "",
      group: [row.category, row.operation_count ? `${row.operation_count} 个接口` : "0 个接口"]
        .filter(Boolean)
        .join(" · "),
    }));
}

/**
 * 一个 API 操作 → 一条可开关的联网搜索能力。
 *
 * **行的形状是 `{ operation: {...}, source_name }`**，字段在里层。
 * 这一条是踩出来的：按顶层字段读的话 id 全是 undefined，整张表被过滤成空，
 * 而界面上只会显示「这一类没有」——看不出是接口不通还是真的没有。
 * 下面那几个字段名都对着真实响应抄的（`/api/v1/api-operations/search`）。
 *
 * summary 是多行的：第一行是名字，后面是详细说明与使用场景。列表里只显示
 * 第一行，其余进描述——否则一条就占掉半屏。
 *
 * **换行可能是字面量的反斜杠 n**（内部平台那边存的就是转义过的文本），所以两种
 * 都要切。只按真换行切的话，名字里会拖着一整段「使用场景：……」。
 */
export function mapOperations(rows: OperationRow[]): CatalogItem[] {
  return rows
    .map((row) => ({ ...row.operation, sourceName: row.source_name }))
    .filter((op) => op.id !== undefined)
    // 明确不可调用的滤掉；**判定不出来的留下**——旧版 Platform 给不出这两个
    // 字段，一律当拒绝会把整张表清空。
    .filter((op) => !(op.access_checked === true && op.accessible === false))
    .map((op) => {
      const [title = "", ...rest] = (op.summary ?? "").split(/\\n|\r?\n/);
      return {
        type: "web-search" as const,
        id: String(op.id),
        // 第一行也可能很长（有些接口把整段说明写成一行），再截一刀：
        // 这是列表里的一行，不是文档。
        name: clampName(title.trim()) || op.path || `操作 ${op.id}`,
        description: (rest.join(" ").trim() || op.description) ?? "",
        group: [op.sourceName, op.method && op.path ? `${op.method.toUpperCase()} ${op.path}` : ""]
          .filter(Boolean)
          .join(" · "),
      };
    });
}

export function mapCorpora(rows: CorpusRow[]): CatalogItem[] {
  return rows
    .filter((row) => row.id !== undefined)
    .map((row) => ({
      type: "corpus" as const,
      id: String(row.id),
      name: row.name ?? `语料库 ${row.id}`,
      description: row.description ?? "",
      group: [row.kind, row.entry_count ? `${row.entry_count} 条` : ""].filter(Boolean).join(" · "),
    }));
}

/** 联网搜索：在已授权的 API 操作里找搜索引擎接口。 */
const WEB_SEARCH_ROUTE = "/api/v1/api-operations/search";

/**
 * 找联网搜索接口用的关键词。
 *
 * 按关键词找而不是写死一个 id：接口 id 在不同环境里不一样，内部平台那边重建一次
 * 操作 id 就变了——写死的表现是「联网搜索开着但模型不会上网」，而且没有任何
 * 报错。多搜几个词是因为那个接口在内部平台上叫什么由录入的人决定，我们只知道
 * 它大概率带「搜索 / search」这几个字。
 *
 * 命中多条时全列出来（按下面的打分排序），由用户在能力页上关掉不要的——
 * 这比我们替他猜一个然后猜错要好。
 */
const WEB_SEARCH_KEYWORDS = ["联网搜索", "联网", "websearch", "web search"];

/**
 * 给命中的操作打分，越像「通用联网搜索」分越高。
 *
 * 为什么要排序而不是原样列出来：「搜索」这个词在一个企业 API 目录里能命中
 * 一大堆东西（搜索订单、搜索物料…），不排的话真正想要的那条可能排在第十位，
 * 用户得一条条看。排序只影响**顺序与默认展示**，不做过滤——过滤会把我们
 * 猜错的代价变成「那条根本不出现」。
 */
/** 列表里一行最多显示多少字。超了截断，全文在描述里。 */
function clampName(name: string): string {
  const limit = 40;
  return name.length > limit ? `${name.slice(0, limit)}…` : name;
}

/** 列表里最多留几条候选。「搜索」能命中一大堆，全铺出来这一页就没法看了。 */
export const WEB_SEARCH_MAX = 8;

/** 打到这个分就认为「明显是通用联网搜索」，默认开。 */
const WEB_SEARCH_CONFIDENT = 80;

/**
 * 从命中里挑出真正的联网搜索接口。
 *
 * **只留明显是联网搜索的那些**（名字里有「联网搜索」/「web search」这类）。
 * 关键词再窄也会带出个别不相干的接口（比如名字里恰好有「联网」的），
 * 把它们摆进这一栏，用户要么困惑，要么顺手开了一个用不上的工具——
 * 而每个挂上去的工具都在开局就占掉一块上下文。
 *
 * **一条都不够像时，留排第一的那条**。这个接口在内部平台上叫什么由录入的人决定，
 * 可能是「全网搜索」这种没猜到的名字；一条都不留的话，用户看到的是
 * 「联网搜索这一栏是空的」，分不清是没有这个接口还是我们没认出来。
 * 留一条并打开，猜错了他关掉就行，代价不对称。
 */
export function markWebSearchDefaults(ranked: CatalogItem[]): CatalogItem[] {
  const confident = ranked.filter((item) => scoreWebSearch(item) >= WEB_SEARCH_CONFIDENT);
  const kept = confident.length > 0 ? confident : ranked.slice(0, 1);
  return kept.map((item) => ({ ...item, defaultEnabled: true }));
}

export function scoreWebSearch(item: { name: string; description: string; group?: string }): number {
  const haystack = `${item.name} ${item.description} ${item.group ?? ""}`.toLowerCase();
  let score = 0;
  if (haystack.includes("联网搜索")) score += 100;
  if (haystack.includes("web search") || haystack.includes("websearch")) score += 80;
  if (haystack.includes("阿里云") || haystack.includes("aliyun") || haystack.includes("iqs")) score += 40;
  if (haystack.includes("ai-internal-bff")) score += 30;
  if (haystack.includes("互联网") || haystack.includes("全网")) score += 20;
  // 「搜索一下订单」这类业务检索接口不是我们要的，但也不排除——只压低。
  if (haystack.includes("搜索") || haystack.includes("search")) score += 5;
  return score;
}

/**
 * 知识库只有一条。
 *
 * 内部平台不按「多个知识库」建模：检索接口只收 query / categories / top_k，
 * 没有知识库 id，也没有 /knowledge-base/list。能检索到什么由内部平台按员工的
 * 授权范围决定。所以这里给一个整体开关，而不是假装能逐库挑——那样做出来的
 * 开关背后没有对应的东西可关。
 *
 * 按分类拆成多个开关也不行：/knowledge-base/categories/list 返回的是
 * **调用者自己上传的文档**的分类，比他实际能检索到的范围窄，拿它当
 * 「可检索的知识库」会误导。
 */
function knowledgeCatalog(): CatalogItem[] {
  return [
    {
      type: "knowledge",
      id: "default",
      name: "知识库检索",
      description: "在你在内部平台有权访问的知识库里做语义检索。内部平台不分库，范围由授权决定。",
    },
  ];
}

// ---------- 内部平台返回的行。只声明用得上的字段。 ----------

/** /api/v1/data-sources/list 的一行：一个云鉴数据源。 */
export interface DatabaseRow {
  id?: number;
  name?: string;
  description?: string;
  type?: string;
  table_count?: number;
  data_api_count?: number;
}

/** /api/v1/api-sources/list 的一行：一个 API 源。 */
export interface SourceRow {
  id?: number;
  name?: string;
  description?: string;
  category?: string;
  operation_count?: number;
}

/**
 * /api/v1/api-operations/search 的一行。
 *
 * 操作本身在 `operation` 里，源名在外层——这是实测的形状，别按直觉改平。
 */
export interface OperationRow {
  operation?: {
    id?: number;
    /** 多行：第一行是名字，后面是说明与使用场景。 */
    summary?: string;
    description?: string;
    method?: string;
    path?: string;
    access_checked?: boolean;
    accessible?: boolean;
  };
  source_name?: string;
}

export interface CorpusRow {
  id?: number;
  name?: string;
  description?: string;
  kind?: string;
  entry_count?: number;
}
