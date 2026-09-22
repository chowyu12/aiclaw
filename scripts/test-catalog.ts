/**
 * 内部平台能力目录的测试。
 *
 * 粒度是**容器**：数据 API 列库、API 服务列源。用户按库/按源授权，也按库/按源
 * 开关；里面的对象由 claw-mcp 在会话启动时展开，所以库里新增了 API 不用回来重配。
 *
 * 另外两件事：
 *   1. 拉的是**按调用者权限过滤**的接口，不是 /admin/ 的。用 /admin/ 会列出
 *      整个租户的目录，用户配一堆点了就 403 的东西。
 *   2. 「可见」不等于「可调用」。内部平台的可见性按资产授权算，执行时还会再查一层；
 *      明确不可用的要滤掉，**判定不出来的要留下**——一律当成不可用会在旧版
 *      Platform 上把整张表清空。
 *
 * 跑法：make test-renderer
 */
import test from "node:test";
import assert from "node:assert/strict";
import {
  CATALOG_ROUTES,
  CAPABILITY_KINDS,
  CapabilityCatalog,
  composeCapabilities,
  markWebSearchDefaults,
  scoreWebSearch,
  mapCorpora,
  mapOperations,
  mapDatabases,
  mapSources,
  type CatalogItem,
  type CapabilityKind,
  type CorpusRow,
  type DatabaseRow,
  type SourceRow,
} from "../apps/desktop/src/main/catalog.ts";

// ---------- 粒度：容器而不是对象 ----------

test("数据 API 列的是库，不是一个个 API", () => {
  const rows: DatabaseRow[] = [
    { id: 4, name: "db_data_service(CK)", description: "数据组输出表", type: "ClickHouse", data_api_count: 102, table_count: 102 },
  ];
  const items = mapDatabases(rows);
  assert.equal(items[0]?.id, "4");
  assert.equal(items[0]?.name, "db_data_service(CK)");
  // 里面有几个 API 要摆出来：0 个 API 的库加进来一个工具都没有。
  assert.ok(items[0]?.group?.includes("102 个 API"), items[0]?.group);
  assert.ok(items[0]?.group?.includes("ClickHouse"), items[0]?.group);
});

test("空库也列出来，但数字如实显示 0", () => {
  const items = mapDatabases([{ id: 1, name: "空库" }]);
  assert.ok(items[0]?.group?.includes("0 个 API"), items[0]?.group);
});

test("API 服务列的是源，不是一个个接口", () => {
  const rows: SourceRow[] = [
    { id: 9, name: "ai-internal-bff", description: "AI Agent 定制的中间接口层", category: "内部系统", operation_count: 101 },
  ];
  const items = mapSources(rows);
  assert.equal(items[0]?.id, "9");
  assert.ok(items[0]?.group?.includes("101 个接口"), items[0]?.group);
  assert.ok(items[0]?.group?.includes("内部系统"), items[0]?.group);
});

test("语料库带上类型与词条数", () => {
  const rows: CorpusRow[] = [{ id: 5, name: "指标口径", kind: "metric", entry_count: 128 }];
  assert.equal(mapCorpora(rows)[0]?.group, "metric · 128 条");
});

test("没有 id 的行丢掉，不生成一个点不开的条目", () => {
  assert.equal(mapDatabases([{ name: "无 id" }]).length, 0);
  assert.equal(mapSources([{ name: "无 id" }]).length, 0);
  assert.equal(mapCorpora([{ name: "无 id" }]).length, 0);
});

// ---------- 走的是哪个接口 ----------

test("三类都走按权限过滤的接口，没有一个是 /admin/", () => {
  for (const [kind, route] of Object.entries(CATALOG_ROUTES)) {
    assert.ok(
      !route.includes("/admin/"),
      `${kind} 用了 /admin/ 接口（${route}）——那会列出整个租户的目录`,
    );
    assert.ok(route.startsWith("/api/v1/"), `${kind} 的路径不对：${route}`);
  }
  // 固定住具体路径：这三条都是「容器」列表，换成对象列表就改变了整个模型的粒度。
  assert.equal(CATALOG_ROUTES["data-api"], "/api/v1/data-sources/list");
  assert.equal(CATALOG_ROUTES["api-operation"], "/api/v1/api-sources/list");
  assert.equal(CATALOG_ROUTES.corpus, "/api/v1/corpora/list");
});

test("不走 /api-operations/search：它在 Platform 那层要求非空 keyword", () => {
  // 空关键词直接 400（search_online_a_p_i_operations_logic.go:32），
  // 没法用来「打开面板就看到有什么」。
  for (const route of Object.values(CATALOG_ROUTES)) {
    assert.notEqual(route, "/api/v1/api-operations/search");
  }
});

// ---------- 同步：默认启用 + 记住关掉的 ----------

function item(type: CapabilityKind, id: string, name = id): CatalogItem {
  return { type, id, name, description: "" };
}

function discovered(...items: CatalogItem[]): Map<CapabilityKind, CatalogItem[]> {
  const map = new Map<CapabilityKind, CatalogItem[]>();
  for (const kind of CAPABILITY_KINDS) map.set(kind, []);
  for (const one of items) map.get(one.type)!.push(one);
  return map;
}

test("拉下来的默认是开的", () => {
  const out = composeCapabilities(discovered(item("data-api", "1"), item("corpus", "7")), [], {});
  assert.deepEqual(
    out.map((c) => [c.type, c.id, c.enabled]),
    [
      ["data-api", "1", true],
      ["corpus", "7", true],
    ],
  );
});

test("用户关掉的，下次同步还是关的", () => {
  const out = composeCapabilities(
    discovered(item("data-api", "1"), item("data-api", "2")),
    [
      { type: "data-api", id: "1", name: "甲", enabled: false },
      { type: "data-api", id: "2", name: "乙", enabled: true },
    ],
    { "data-api:1": false },
  );
  assert.deepEqual(
    out.map((c) => [c.id, c.enabled]),
    [
      ["1", false],
      ["2", true],
    ],
  );
});

test("关掉的记录按类型分开：不同类型的同名 id 不互相影响", () => {
  const out = composeCapabilities(
    discovered(item("data-api", "1"), item("corpus", "1")),
    [],
    { "corpus:1": false },
  );
  assert.deepEqual(
    out.map((c) => [c.type, c.enabled]),
    [
      ["data-api", true],
      ["corpus", false],
    ],
  );
});

test("内部平台上没有了的，本地也就没有了", () => {
  const out = composeCapabilities(
    discovered(item("data-api", "1")),
    [
      { type: "data-api", id: "1", name: "甲", enabled: true },
      { type: "data-api", id: "9", name: "已下线", enabled: true },
    ],
    {},
  );
  assert.deepEqual(out.map((c) => c.id), ["1"]);
});

test("某一类没拉到时保留上次的结果，而不是当成被删了", () => {
  // 网络抖一下就把用户的能力清空，下一个会话什么都调不动，而原因在别处。
  const partial = new Map<CapabilityKind, CatalogItem[]>([["corpus", [item("corpus", "7")]]]);
  const out = composeCapabilities(partial, [
    { type: "data-api", id: "1", name: "甲", enabled: true },
  ], {});
  assert.deepEqual(
    out.map((c) => [c.type, c.id]),
    [
      ["data-api", "1"],
      ["corpus", "7"],
    ],
  );
});

test("保留上次结果时也认关掉的记录：不能借着一次失败把它开回来", () => {
  const out = composeCapabilities(new Map(), [
    { type: "data-api", id: "1", name: "甲", enabled: false },
  ], { "data-api:1": false });
  assert.equal(out[0]?.enabled, false);
});

// ---------- syncAll ----------

function fakeStore(
  previous: { type: string; id: string; enabled: boolean }[],
  choices: Record<string, boolean> = {},
) {
  const state = {
    capabilities: previous.map((c) => ({ ...c, name: c.id })),
    choices,
  };
  return {
    state,
    store: {
      readCapabilities: () => state.capabilities,
      writeCapabilities: (next: unknown) => {
        state.capabilities = next as never;
        return next;
      },
      readCapabilityChoices: () => state.choices,
      writeCapabilityChoices: (next: Record<string, boolean>) => {
        state.choices = next;
      },
    } as never,
  };
}

test("syncAll 每一类各拉一次，结果落盘", async () => {
  const calls: string[] = [];
  const poster = {
    async post<T>(path: string): Promise<T> {
      calls.push(path);
      // 操作搜索那个接口的行是嵌套的，别的都是平的——形状不一样，
      // 假数据也得跟着不一样，否则这条用例会在错误的形状上「通过」。
      if (path === "/api/v1/api-operations/search") {
        return { list: [{ operation: { id: 348, summary: "阿里云联网搜索" } }] } as T;
      }
      return { list: [{ id: 1, name: "甲" }] } as T;
    },
  };
  const fake = fakeStore([]);
  const catalog = new CapabilityCatalog(fake.store, poster);

  const result = await catalog.syncAll();

  // 知识库不打网络：内部平台没有按库枚举的接口，检索接口也不收知识库 id。
  // 联网搜索按几个关键词各搜一次（接口强制要 keyword）。
  assert.deepEqual(calls.slice(0, 3), [
    "/api/v1/data-sources/list",
    "/api/v1/api-sources/list",
    "/api/v1/corpora/list",
  ]);
  assert.ok(
    calls.slice(3).every((path) => path === "/api/v1/api-operations/search"),
    calls.join("、"),
  );
  assert.deepEqual(result.failures, []);
  // 数据源 1 + API 源 1 + 知识库 1 + 语料库 1 + 联网搜索 1（几个关键词命中同一条，去重）
  assert.equal(result.capabilities.length, 5, JSON.stringify(result.capabilities));
  assert.ok(result.capabilities.every((c) => c.enabled));
  // 落盘之后会话启动只读文件，不再打内部平台。
  assert.equal(fake.state.capabilities.length, 5);
});

test("联网搜索：几个关键词命中同一个接口时只留一条", async () => {
  // 接口 id 不写死而按关键词找——id 在不同环境不一样，写死的表现是
  // 「联网搜索开着但模型不会上网」，而且没有任何报错。
  const poster = {
    async post<T>(path: string): Promise<T> {
      if (path !== "/api/v1/api-operations/search") return { list: [] } as T;
      // 形状照抄真实响应：操作在 operation 里，源名在外层，summary 是多行的。
      return {
        list: [
          {
            operation: {
              id: 348,
              method: "POST",
              path: "/ai/aliyun-web-search",
              summary: "阿里云联网搜索\n调用阿里云信息查询服务（IQS）进行开放域联网搜索。",
            },
            source_name: "ai-internal-bff",
          },
        ],
      } as T;
    },
  };
  const fake = fakeStore([]);
  const result = await new CapabilityCatalog(fake.store, poster).syncAll();
  const web = result.capabilities.filter((c) => c.type === "web-search");
  assert.equal(web.length, 1, JSON.stringify(web));
  assert.equal(web[0]?.id, "348");
  // 名字只取 summary 的第一行，剩下的进描述——否则一条占掉半屏。
  assert.equal(web[0]?.name, "阿里云联网搜索");
  assert.match(web[0]?.description ?? "", /IQS/);
});

test("明确不可调用的滤掉，判定不出来的留下", () => {
  // 「可见 ≠ 可调用」。旧版 Platform 给不出这两个字段，一律当拒绝会把
  // 整张表清空——所以只滤掉**明确说了不可调用**的那些。
  // 行的形状照抄真实响应：操作在 operation 里，源名在外层。
  const items = mapOperations([
    { operation: { id: 1, summary: "能调的", access_checked: true, accessible: true } },
    { operation: { id: 2, summary: "明确不能调的", access_checked: true, accessible: false } },
    { operation: { id: 3, summary: "判定不出来的" } },
    { operation: { summary: "没有 id 的" } },
  ]);
  assert.deepEqual(items.map((item) => item.id), ["1", "3"]);
});

test("名字只取 summary 第一行，内部平台那边的换行是字面量的反斜杠 n", () => {
  // 实测就是这样存的。只按真换行切的话，名字会拖着整段「使用场景：……」，
  // 列表里一条占掉半屏。
  const [item] = mapOperations([
    {
      operation: {
        id: 348,
        method: "POST",
        path: "/ai/aliyun-web-search",
        summary: "阿里云联网搜索\\n调用阿里云信息查询服务（IQS）进行开放域联网搜索。\\n使用场景：……",
      },
      source_name: "ai-internal-bff",
    },
  ]);
  assert.equal(item?.name, "阿里云联网搜索");
  assert.match(item?.description ?? "", /IQS/);
  assert.match(item?.group ?? "", /ai-internal-bff/);
  assert.match(item?.group ?? "", /POST \/ai\/aliyun-web-search/);
});

test("单类失败不影响其余，原因里带上接口路径", async () => {
  const poster = {
    async post<T>(path: string): Promise<T> {
      if (path === CATALOG_ROUTES["data-api"]) throw new Error("403 forbidden");
      return { list: [{ id: 1, name: "甲" }] } as T;
    },
  };
  const fake = fakeStore([{ type: "data-api", id: "8", enabled: true }]);
  const catalog = new CapabilityCatalog(fake.store, poster);

  const result = await catalog.syncAll();

  assert.deepEqual(
    result.failures.map((f) => f.type),
    ["data-api"],
  );
  assert.ok(result.failures[0]?.reason.includes("/api/v1/data-sources/list"), result.failures[0]?.reason);
  // 失败那一类保留上次的，其余照常更新。
  assert.ok(result.capabilities.some((c) => c.type === "data-api" && c.id === "8"));
  assert.ok(result.capabilities.some((c) => c.type === "corpus"));
});

test("开关都会被记住，跨同步存活", () => {
  const fake = fakeStore([{ type: "data-api", id: "1", enabled: true }]);
  const catalog = new CapabilityCatalog(fake.store, {
    async post<T>(): Promise<T> {
      throw new Error("不该打网络");
    },
  });

  const off = catalog.setEnabled("data-api", "1", false);
  assert.equal(off[0]?.enabled, false);
  assert.deepEqual(fake.state.choices, { "data-api:1": false });

  const on = catalog.setEnabled("data-api", "1", true);
  assert.equal(on[0]?.enabled, true);
  // 记的是双向选择：打开也要记下来，否则默认关的那些（联网搜索的候选）
  // 一同步就又关回去了。
  assert.deepEqual(fake.state.choices, { "data-api:1": true });
});

// ---------- 联网搜索：默认开哪一条 ----------
//
// 这一类是**按关键词搜出来的**，而「搜索」在企业 API 目录里能命中一堆业务
// 检索接口。全默认打开就是给模型挂一排用不上的 search_xxx，白占上下文地板；
// 一条都不开则是「开着但模型不会上网」，而那是最难查的故障。

test("只留明显是联网搜索的那条，业务检索接口不进这一栏", () => {
  // 实测：关键词放宽到「搜索」时这一栏会冒出 8 条，只有 1 条是对的——
  // 搜商品、搜企业、搜替代料都不是联网搜索，摆在这里只是噪音，
  // 而每个挂上去的工具都在开局占掉一块上下文。
  const marked = markWebSearchDefaults([
    { type: "web-search", id: "348", name: "阿里云联网搜索", description: "", group: "ai-internal-bff" },
    { type: "web-search", id: "319", name: "企业搜索 - 按关键词分页搜索企业", description: "" },
    { type: "web-search", id: "45", name: "AI驱动的采购选商品推荐", description: "" },
  ]);
  assert.deepEqual(
    marked.map((item) => [item.id, item.defaultEnabled]),
    [["348", true]],
  );
});

test("一条都不够像时留最像的那条，而不是留一栏空的", () => {
  // 这个接口在内部平台上叫什么由录入的人决定，可能是「全网搜索」这种没猜到的
  // 名字。空着的话，用户分不清是没有这个接口还是我们没认出来。
  const marked = markWebSearchDefaults([
    { type: "web-search", id: "5", name: "全网搜索", description: "" },
    { type: "web-search", id: "6", name: "搜索物料", description: "" },
  ]);
  assert.deepEqual(
    marked.map((item) => [item.id, item.defaultEnabled]),
    [["5", true]],
  );
});

test("用户手动开过的，同步不会把它关回去", () => {
  const out = composeCapabilities(
    new Map([
      [
        "web-search",
        [
          { type: "web-search" as const, id: "12", name: "搜索订单", description: "", defaultEnabled: false },
        ],
      ],
    ]),
    [],
    { "web-search:12": true },
  );
  assert.equal(out[0]?.enabled, true);
});

test("打分：联网搜索 > 阿里云/ai-internal-bff > 只带「搜索」二字的业务接口", () => {
  const score = (name: string): number =>
    scoreWebSearch({ name, description: "", group: "" });
  assert.ok(score("阿里云联网搜索") > score("互联网信息查询"));
  assert.ok(score("互联网信息查询") > score("搜索订单"));
  assert.ok(score("web search") > score("search order"));
});
