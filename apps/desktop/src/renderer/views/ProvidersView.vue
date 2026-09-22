<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { actions, store } from "../store";
import { describeError } from "../errors";
import {
  MODEL_ROLES,
  ROLE_LABELS,
  formatModelMark,
  parseModelMark,
  type ModelRole,
} from "../model-roles";

/**
 * 模型服务的管理：一个 OpenAI 兼容端点 + 它的 Key + 模型清单。
 *
 * 数据在内核那边的库里（AIClaw 沿用至今的 aiclaw.db），这一页每次改动都直接
 * 写过去；Key 只往那边送，回来的只有「配了没有」。
 *
 * 与 MCP 页同一套形状：配好的默认收起，点开才是表单。清单里的模型名就是
 * 对话页顶部能选的东西——所以「从端点拉取」是这一页最有用的按钮。
 */

type ProviderRow = (typeof store.providers)[number];

/** 各类型的显示名与默认端点。默认端点与内核 providers 包里那张表一致，改一处要改另一处。 */
const TYPES: { id: string; label: string; baseUrl: string }[] = [
  { id: "openai-compatible", label: "OpenAI 兼容", baseUrl: "" },
  { id: "openai", label: "OpenAI", baseUrl: "https://api.openai.com/v1" },
  { id: "qwen", label: "通义千问", baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1" },
  { id: "kimi", label: "Kimi", baseUrl: "https://api.moonshot.cn/v1" },
  { id: "openrouter", label: "OpenRouter", baseUrl: "https://openrouter.ai/api/v1" },
  { id: "claude", label: "Claude（OpenAI 兼容入口）", baseUrl: "https://api.anthropic.com/v1" },
  {
    id: "gemini",
    label: "Gemini（OpenAI 兼容入口）",
    baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai",
  },
];

const expanded = ref(0);
const saving = ref(false);
/** 每个服务的 Key 输入框。存完立刻清空，不让凭据留在 DOM 里。 */
const keyDrafts = reactive<Record<number, string>>({});
/** 「从端点拉取」的状态与结果，按服务 id 存。 */
const fetches = reactive<Record<number, { loading: boolean; error?: string; count?: number }>>({});

onMounted(() => {
  if (store.providers.length === 0) void actions.loadProviders();
});

function placeholderFor(type: string): string {
  return TYPES.find((item) => item.id === type)?.baseUrl || "https://example.com/v1";
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
    const created = await actions.createProvider({ name: "新模型服务", type: "openai-compatible" });
    // 新加的直接展开：它是空的，收起来等于加了个看不见的东西。
    expanded.value = created.id;
  });
}

async function patch(
  id: number,
  change: { name?: string; type?: string; baseUrl?: string; models?: string[]; enabled?: boolean },
): Promise<void> {
  await run(() => actions.updateProvider({ id, ...change }));
}

async function saveKey(id: number): Promise<void> {
  const key = keyDrafts[id]?.trim();
  if (!key) return;
  await run(() => actions.updateProvider({ id, apiKey: key }));
  keyDrafts[id] = "";
}

async function remove(provider: ProviderRow): Promise<void> {
  if (!confirm(`删除模型服务「${provider.name}」？用它的会话下次恢复时要重新选模型。`)) return;
  await run(() => actions.deleteProvider(provider.id));
}

function toggleExpand(id: number): void {
  expanded.value = expanded.value === id ? 0 : id;
}

/** 模型清单在文本框里一行一个。能力标记（`名字#vision`）原样留着。 */
function parseModels(raw: string): string[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

/**
 * 给某个模型加上或去掉一个能力。
 *
 * 能力标记直接写在清单项上（`qwen3-vl#vision`），所以这里改的是清单本身——
 * 不另存一份映射：另存一份就要处理「模型改名了标记还留着」这类不同步。
 */
async function toggleRole(provider: ProviderRow, entry: string, role: ModelRole, on: boolean): Promise<void> {
  const parsed = parseModelMark(entry);
  const roles = on
    ? [...parsed.roles, role]
    : parsed.roles.filter((item) => item !== role);
  const next = provider.models.map((item) => (item === entry ? formatModelMark(parsed.name, roles) : item));
  await run(() => actions.updateProvider({ id: provider.id, models: next }));
}

/**
 * 到端点拉一遍模型名，并进清单。
 *
 * 是「并进」不是「换掉」：用户手写的名字（端点 /models 不一定列全）不该被
 * 一次拉取冲掉。拉到的原样加进来，重复的不加。
 */
async function fetchModels(provider: ProviderRow): Promise<void> {
  fetches[provider.id] = { loading: true };
  try {
    const remote = await actions.fetchProviderModels(provider.id);
    const merged = [...provider.models];
    for (const name of remote) if (!merged.includes(name)) merged.push(name);
    await actions.updateProvider({ id: provider.id, models: merged });
    fetches[provider.id] = { loading: false, count: remote.length };
  } catch (error) {
    fetches[provider.id] = { loading: false, error: describeError(error) };
  }
}

/** 收起时那一行右边的状态字。 */
function summary(provider: ProviderRow): string {
  if (!provider.apiKeySet) return "没配 Key";
  if (provider.models.length === 0) return "清单为空";
  return `${provider.models.length} 个模型`;
}
</script>

<template>
  <div class="page">
    <section>
      <header>
        <h2>模型服务</h2>
        <p class="sub">
          一个模型服务就是一个 OpenAI 兼容端点：填地址、Key，再写上要用的模型名。
          清单里的模型就是对话页顶部能选的那些；改动立刻生效，新会话与切模型都用得上。
        </p>
      </header>

      <p class="note">
        Key 只存进本机的配置库，不写进日志、不进诊断包、不经界面回显。
        Claude 与 Gemini 走的是它们的 OpenAI 兼容入口——原生接口不支持。
      </p>

      <div class="add">
        <button class="ghost" @click="add()">+ 添加模型服务</button>
        <button class="ghost" @click="actions.loadProviders()">刷新</button>
      </div>

      <p v-if="store.providersError" class="note warn">{{ store.providersError }}</p>
      <p v-else-if="store.providers.length === 0 && !store.providersLoading" class="note">
        还没有模型服务。添加一个，填上端点与 Key，再写上模型名。
      </p>

      <article
        v-for="provider in store.providers"
        :key="provider.id"
        class="card"
        :class="{ off: !provider.enabled }"
      >
        <div class="card-head">
          <button
            class="disclose"
            :aria-expanded="expanded === provider.id"
            @click="toggleExpand(provider.id)"
          >
            <span class="chevron">{{ expanded === provider.id ? "▾" : "▸" }}</span>
            <span class="name">{{ provider.name || "未命名" }}</span>
            <span class="badge">{{ TYPES.find((t) => t.id === provider.type)?.label ?? provider.type }}</span>
            <span class="state" :class="{ bad: !provider.apiKeySet }">{{ summary(provider) }}</span>
          </button>
          <label class="toggle">
            <input
              type="checkbox"
              :checked="provider.enabled"
              @change="patch(provider.id, { enabled: ($event.target as HTMLInputElement).checked })"
            />
            启用
          </label>
          <button class="icon" title="删除" @click="remove(provider)">×</button>
        </div>

        <template v-if="expanded === provider.id">
          <label>
            <span>名字</span>
            <input
              :value="provider.name"
              placeholder="给这个服务起个名字"
              @change="patch(provider.id, { name: ($event.target as HTMLInputElement).value })"
            />
          </label>

          <div class="pair">
            <label>
              <span>类型</span>
              <select
                :value="provider.type"
                @change="patch(provider.id, { type: ($event.target as HTMLSelectElement).value })"
              >
                <option v-for="t in TYPES" :key="t.id" :value="t.id">{{ t.label }}</option>
              </select>
            </label>
            <label>
              <span>端点</span>
              <input
                :value="provider.baseUrl"
                :placeholder="placeholderFor(provider.type)"
                @change="patch(provider.id, { baseUrl: ($event.target as HTMLInputElement).value })"
              />
            </label>
          </div>
          <p class="hint">填到 <code>/v1</code>。留空用该类型的默认端点（OpenAI 兼容类型必须填）。</p>

          <label>
            <span>Key<em v-if="provider.apiKeySet">已配置</em></span>
            <div class="key-row">
              <input
                v-model="keyDrafts[provider.id]"
                type="password"
                :placeholder="provider.apiKeySet ? '留空表示不修改' : 'sk-…'"
                @keydown.enter="saveKey(provider.id)"
              />
              <button class="ghost" :disabled="!keyDrafts[provider.id]" @click="saveKey(provider.id)">
                保存 Key
              </button>
            </div>
          </label>

          <label>
            <span>模型清单（一行一个）</span>
            <textarea
              rows="4"
              :value="provider.models.join('\n')"
              placeholder="gpt-4.1&#10;o3-mini"
              @change="patch(provider.id, { models: parseModels(($event.target as HTMLTextAreaElement).value) })"
            />
          </label>

          <!-- 能力标记：勾上之后这个模型才会出现在「配置」页对应角色的候选里。
               不勾也不影响它当对话模型用。 -->
          <div v-if="provider.models.length > 0" class="caps">
            <span class="caps-title">这些模型还能做什么</span>
            <div v-for="entry in provider.models" :key="entry" class="cap-row">
              <code class="cap-name">{{ parseModelMark(entry).name }}</code>
              <label v-for="role in MODEL_ROLES" :key="role" class="cap">
                <input
                  type="checkbox"
                  :checked="parseModelMark(entry).roles.includes(role)"
                  @change="toggleRole(provider, entry, role, ($event.target as HTMLInputElement).checked)"
                />
                {{ ROLE_LABELS[role] }}
              </label>
            </div>
            <p class="hint">
              勾了的模型会出现在「配置」页对应角色的候选里：看图用来替对话模型读图，
              另外三样各自对应一个工具。不勾不影响它当对话模型用。
            </p>
          </div>
          <div class="fetch">
            <button
              class="ghost small"
              :disabled="!provider.apiKeySet || fetches[provider.id]?.loading"
              @click="fetchModels(provider)"
            >
              {{ fetches[provider.id]?.loading ? "拉取中…" : "从端点拉取模型名" }}
            </button>
            <span v-if="fetches[provider.id]?.error" class="hint bad">{{ fetches[provider.id]?.error }}</span>
            <span v-else-if="fetches[provider.id]?.count !== undefined" class="hint">
              端点返回 {{ fetches[provider.id]?.count }} 个，已并进清单。
            </span>
            <span v-else-if="!provider.apiKeySet" class="hint">先保存 Key 才能拉取。</span>
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

/* 停用的压暗但不隐藏：让用户看得见自己关了什么。 */
.card.off {
  opacity: 0.6;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* 整行都能点开，不只是那个小三角——收起态下这一行就是这张卡片本身。 */
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

.state.bad {
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

.key-row {
  display: flex;
  gap: 8px;
}

.key-row input {
  flex: 1;
}

/* 按钮不跟着输入框挤，否则「保存 Key」会被压成两行竖排。 */
.key-row button {
  flex: 0 0 auto;
  white-space: nowrap;
}

.fetch {
  display: flex;
  align-items: center;
  gap: 10px;
}

.caps {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px 14px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface-2);
}

.caps-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--ink-2);
}

.cap-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.cap-name {
  flex: 1 1 160px;
  min-width: 0;
  font-family: var(--mono);
  font-size: 11.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cap {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11.5px;
  color: var(--ink-2);
  white-space: nowrap;
  cursor: pointer;
}

.cap input {
  width: auto;
}

button.small {
  padding: 3px 10px;
  font-size: 11.5px;
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

.hint.bad {
  color: var(--danger);
}
</style>
