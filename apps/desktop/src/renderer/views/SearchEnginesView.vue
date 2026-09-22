<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { actions, store } from "../store";
import { describeError } from "../errors";
import type { SearchHitView } from "../../shared/types";

/**
 * 联网搜索引擎：Tavily / SerpAPI / 阿里云 IQS，各带自己的 Key。
 *
 * 有一个启用且配了 Key 的引擎，开会话时就会挂上内核自带的 `web_search` 工具；
 * 一次只用一个（列表里第一个启用的）——两个同时挂给模型它无从选择。
 * Key 只进不出，存完只回「已配置」。
 */

type EngineRow = (typeof store.searchEngines)[number];

/** 各类型的显示名与默认端点。默认端点与内核 websearch 包一致，留空就用它。 */
const PROVIDERS: { id: string; label: string; baseUrl: string; keyHint: string }[] = [
  { id: "tavily", label: "Tavily", baseUrl: "https://api.tavily.com/search", keyHint: "tvly-…" },
  { id: "serpapi", label: "SerpAPI", baseUrl: "https://serpapi.com/search.json", keyHint: "SerpAPI 的 api_key" },
  {
    id: "aliyun-iqs",
    label: "阿里云 IQS",
    baseUrl: "https://cloud-iqs.aliyuncs.com/search/unified",
    keyHint: "阿里云 IQS 的 API Key",
  },
];

const expanded = ref(0);
const saving = ref(false);
const keyDrafts = reactive<Record<number, string>>({});
const testQuery = ref("AIClaw");
const tests = reactive<Record<number, { loading: boolean; error?: string; results?: SearchHitView[] }>>({});

onMounted(() => {
  if (store.searchEngines.length === 0) void actions.loadSearchEngines();
});

function providerOf(engine: EngineRow) {
  return PROVIDERS.find((p) => p.id === engine.provider);
}

async function run(task: () => Promise<unknown>): Promise<void> {
  saving.value = true;
  try {
    await task();
  } catch (error) {
    actions.showError(describeError(error));
  } finally {
    saving.value = false;
  }
}

async function add(): Promise<void> {
  await run(async () => {
    const created = await actions.createSearchEngine({ provider: "tavily" });
    expanded.value = created.id;
  });
}

async function patch(
  id: number,
  change: { provider?: string; name?: string; baseUrl?: string; enabled?: boolean },
): Promise<void> {
  await run(() => actions.updateSearchEngine({ id, ...change }));
}

async function saveKey(id: number): Promise<void> {
  const key = keyDrafts[id]?.trim();
  if (!key) return;
  await run(() => actions.updateSearchEngine({ id, apiKey: key }));
  keyDrafts[id] = "";
}

async function remove(engine: EngineRow): Promise<void> {
  if (!confirm(`删除搜索引擎「${engine.name}」？`)) return;
  await run(() => actions.deleteSearchEngine(engine.id));
}

async function test(engine: EngineRow): Promise<void> {
  tests[engine.id] = { loading: true };
  try {
    const result = await actions.testSearchEngine(engine.id, testQuery.value.trim() || "AIClaw");
    tests[engine.id] = { loading: false, results: result.results };
  } catch (error) {
    tests[engine.id] = { loading: false, error: describeError(error) };
  }
}

/** 收起时那一行右边的状态字。 */
function summary(engine: EngineRow): string {
  if (!engine.apiKeySet) return "没配 Key";
  const first = store.searchEngines.find((e) => e.enabled && e.apiKeySet);
  if (engine.enabled && first?.id === engine.id) return "生效中";
  if (engine.enabled) return "已启用（排在后面，不生效）";
  return "已停用";
}
</script>

<template>
  <div class="page">
    <section>
      <header>
        <h2>搜索引擎</h2>
        <p class="sub">
          配一个引擎、填上 Key、启用，模型就多一个 <code>web_search</code> 工具。
          一次只用一个：列表里第一个启用且配了 Key 的。改动下一个会话生效。
        </p>
      </header>

      <p class="note">搜索是只读的，调用时不会请你确认。Key 只存进本机的配置库，不回显。</p>

      <div class="add">
        <button class="ghost" @click="add()">+ 添加搜索引擎</button>
        <button class="ghost" @click="actions.loadSearchEngines()">刷新</button>
      </div>

      <p v-if="store.searchError" class="note warn">{{ store.searchError }}</p>
      <p v-else-if="store.searchEngines.length === 0 && !store.searchLoading" class="note">
        还没有搜索引擎。没有它模型也能用，只是不能联网搜。
      </p>

      <article
        v-for="engine in store.searchEngines"
        :key="engine.id"
        class="card"
        :class="{ off: !engine.enabled }"
      >
        <div class="card-head">
          <button class="disclose" :aria-expanded="expanded === engine.id" @click="expanded = expanded === engine.id ? 0 : engine.id">
            <span class="chevron">{{ expanded === engine.id ? "▾" : "▸" }}</span>
            <span class="name">{{ engine.name || "未命名" }}</span>
            <span class="badge">{{ providerOf(engine)?.label ?? engine.provider }}</span>
            <span class="state" :class="{ bad: !engine.apiKeySet }">{{ summary(engine) }}</span>
          </button>
          <label class="toggle">
            <input
              type="checkbox"
              :checked="engine.enabled"
              @change="patch(engine.id, { enabled: ($event.target as HTMLInputElement).checked })"
            />
            启用
          </label>
          <button class="icon" title="删除" @click="remove(engine)">×</button>
        </div>

        <template v-if="expanded === engine.id">
          <div class="pair">
            <label>
              <span>类型</span>
              <select
                :value="engine.provider"
                @change="patch(engine.id, { provider: ($event.target as HTMLSelectElement).value })"
              >
                <option v-for="p in PROVIDERS" :key="p.id" :value="p.id">{{ p.label }}</option>
              </select>
            </label>
            <label>
              <span>名字</span>
              <input
                :value="engine.name"
                @change="patch(engine.id, { name: ($event.target as HTMLInputElement).value })"
              />
            </label>
          </div>

          <label>
            <span>端点</span>
            <input
              :value="engine.baseUrl"
              :placeholder="providerOf(engine)?.baseUrl ?? ''"
              @change="patch(engine.id, { baseUrl: ($event.target as HTMLInputElement).value })"
            />
            <span class="hint">留空用默认端点。自建代理或私有化部署才需要改。</span>
          </label>

          <label>
            <span>Key<em v-if="engine.apiKeySet">已配置</em></span>
            <div class="key-row">
              <input
                v-model="keyDrafts[engine.id]"
                type="password"
                :placeholder="engine.apiKeySet ? '留空表示不修改' : (providerOf(engine)?.keyHint ?? '')"
                @keydown.enter="saveKey(engine.id)"
              />
              <button class="ghost" :disabled="!keyDrafts[engine.id]" @click="saveKey(engine.id)">保存 Key</button>
            </div>
          </label>

          <div class="test">
            <div class="test-row">
              <input v-model="testQuery" placeholder="搜点什么试试" @keydown.enter="test(engine)" />
              <button class="ghost small" :disabled="!engine.apiKeySet || tests[engine.id]?.loading" @click="test(engine)">
                {{ tests[engine.id]?.loading ? "搜索中…" : "试一下" }}
              </button>
            </div>
            <p v-if="!engine.apiKeySet" class="hint">先保存 Key 才能试。</p>
            <p v-else-if="tests[engine.id]?.error" class="hint bad">{{ tests[engine.id]?.error }}</p>
            <div v-else-if="tests[engine.id]?.results" class="hits">
              <p v-if="tests[engine.id]?.results?.length === 0" class="hint">连上了，但没有结果。</p>
              <div v-for="hit in tests[engine.id]?.results" :key="hit.url" class="hit">
                <a :href="hit.url" target="_blank" rel="noreferrer">{{ hit.title || hit.url }}</a>
                <p class="hint">{{ hit.snippet }}</p>
              </div>
            </div>
          </div>
        </template>
      </article>

      <p v-if="saving" class="note">保存中…</p>
    </section>
  </div>
</template>

<style scoped>
.page {
  height: 100%;
  overflow-y: auto;
  padding: 28px 28px 64px;
}

section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 660px;
  margin: 0 auto;
}

header {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.sub {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}

.add {
  display: flex;
  gap: 8px;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 11px;
  padding: 14px 16px;
  border: 1px solid var(--rule);
  border-radius: var(--r-lg);
  background: var(--surface);
  box-shadow: var(--shadow-1);
}

.card.off {
  opacity: 0.6;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.disclose {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 2px 0;
  border: none;
  background: none;
  text-align: left;
  cursor: pointer;
  color: inherit;
}

.chevron {
  flex: 0 0 auto;
  color: var(--muted);
  font-size: 10px;
}

.name {
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
}

.state {
  flex: 1;
  min-width: 0;
  color: var(--muted);
  font-size: 11.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.state.bad,
.hint.bad {
  color: var(--danger);
}

.badge {
  flex: 0 0 auto;
  padding: 2px 8px;
  border-radius: var(--r-full);
  background: var(--surface-2);
  color: var(--muted);
  font-size: 10.5px;
  white-space: nowrap;
}

.toggle {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--ink-2);
  cursor: pointer;
  white-space: nowrap;
}

.toggle input {
  width: auto;
}

.icon {
  flex: 0 0 auto;
  width: 24px;
  height: 24px;
  border: none;
  border-radius: var(--r-sm);
  background: none;
  color: var(--muted);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
}

.icon:hover {
  background: var(--danger-soft);
  color: var(--danger);
}

label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 13px;
}

label > span {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--ink-2);
  font-size: 12px;
  font-weight: 500;
}

label > span em {
  padding: 1px 7px;
  border-radius: var(--r-full);
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 10.5px;
  font-style: normal;
}

.pair {
  display: grid;
  grid-template-columns: 1fr 2fr;
  gap: 10px;
}

.key-row,
.test-row {
  display: flex;
  gap: 8px;
}

.key-row input,
.test-row input {
  flex: 1;
}

.key-row button,
.test-row button {
  flex: 0 0 auto;
  white-space: nowrap;
}

button.small {
  padding: 3px 10px;
  font-size: 11.5px;
}

.test {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface-2);
}

.hits {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.hit a {
  color: var(--accent);
  font-size: 12.5px;
  text-decoration: none;
}

.hit a:hover {
  text-decoration: underline;
}

.note {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}

.note.warn {
  color: var(--danger);
  background: var(--danger-soft);
  padding: 10px 13px;
  border-radius: var(--r-md);
}

.hint {
  margin: 0;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.6;
}
</style>
