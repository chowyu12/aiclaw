<script setup lang="ts">
import { computed, onMounted } from "vue";
import { actions, store } from "../store";

/**
 * 内部平台能力：自动同步 + 逐个开关。
 *
 * 不再让用户「添加」——内部平台上给你什么权限，这里就有什么，默认全开。
 * 权限那一层已经在内部平台判过一遍，再要求在这里把同一件事点一遍只是重复劳动。
 * 关掉的会记住（`capability-optout.json`），之后每次同步都保持关着，
 * 所以这里也不提供「移除」：同步一次就自己回来的删除按钮是骗人的，
 * 「不想要」的表达方式就是关掉它。
 */

type Kind = "data-api" | "api-operation" | "knowledge" | "corpus" | "web-search";

const KINDS: { id: Kind; label: string; hint: string }[] = [
  {
    id: "data-api",
    label: "数据 API",
    hint: "按库列。库里每个数据 API 成为一个只读查询工具，新增的下次开会话自动带上",
  },
  {
    id: "api-operation",
    label: "API 服务",
    hint: "按服务列。服务里的接口都会挂给模型，写类的标成非只读（调用时按审批策略处理）",
  },
  { id: "knowledge", label: "知识库", hint: "语义检索，范围按你在内部平台的授权" },
  { id: "corpus", label: "语料库", hint: "按企业统一口径查词条定义" },
  {
    id: "web-search",
    label: "联网搜索",
    hint: "内部平台里那个搜索引擎接口，单拎出来挂成一个 search 工具。它本来也在 API 服务里，但埋在上百个接口的目录里模型很难想到用",
  },
];

const LABELS: Record<string, string> = Object.fromEntries(
  KINDS.map((kind) => [kind.id, kind.label]),
);

// 进这一页时不强制同步：同步在应用启动时后台跑过了，这里只是把结果读出来。
// 每次切页都打一次内部平台的话，网络差的时候这一页会一直在转。
onMounted(() => void actions.refreshCapabilities());

const grouped = computed(() =>
  KINDS.map((kind) => ({
    ...kind,
    items: store.capabilities.filter((capability) => capability.type === kind.id),
  })),
);

const enabledCount = computed(() => store.capabilities.filter((c) => c.enabled).length);

const syncedAt = computed(() => {
  if (!store.capabilitiesSyncedAt) return "";
  const at = new Date(store.capabilitiesSyncedAt);
  return Number.isNaN(at.getTime()) ? "" : at.toLocaleString();
});
</script>

<template>
  <div class="page">
    <section>
      <header>
        <h2>内部平台能力</h2>
        <p class="sub">
          配好内部平台之后，你有权限的数据 API 库、API 服务、知识库与语料库会自动拉下来并<strong>默认启用</strong>，
          每次启动应用再同步一次。关掉的会记住，之后同步不会把它开回来。改动下一个会话生效。
          当前启用 <strong>{{ enabledCount }}</strong> 项。
        </p>
        <p class="sub">
          库或服务里对象不多时，每个对象直接成为一个工具；多了会自动改成
          <strong>目录模式</strong>——挂 search / describe / call 三个工具，模型先搜再调。
          工具清单每次请求都要整份重发、且不参与上下文压缩，102 个数据 API 直挂就是
          十万 token 级的固定开销，小窗口的模型会开局就发不出请求。
          实际挂了多少、占多少上下文，在对话页顶部的运行状态里能看到。
        </p>
      </header>

      <p v-if="!store.credentials.clawToken" class="note warn">
        还没填内部平台 BFF Key。到「配置 → 凭据」填上，才能拉到你有权限的对象。
      </p>

      <div class="sync">
        <button class="ghost" :disabled="store.capabilitiesSyncing" @click="actions.syncCapabilities()">
          {{ store.capabilitiesSyncing ? "同步中…" : "立即同步" }}
        </button>
        <span class="sub">
          {{ syncedAt ? `上次同步 ${syncedAt}` : "本次启动还没同步过，显示的是上次的结果" }}
        </span>
      </div>

      <!-- 某一类拉不到时如实说一声，并且说明它保留的是上次的结果：
           列表突然少一类而界面什么都不说，用户会以为权限被收了。 -->
      <p v-for="failure in store.capabilitySyncFailures" :key="failure.type" class="note warn">
        {{ LABELS[failure.type] ?? "内部平台" }}没拉到，保留上次的结果：{{ failure.reason }}
      </p>
    </section>

    <section v-for="kind in grouped" :key="kind.id">
      <header>
        <h3>{{ kind.label }}</h3>
        <p class="sub">{{ kind.hint }}</p>
      </header>

      <p v-if="kind.items.length === 0" class="note">
        内部平台上没有你有权限的{{ kind.label }}，或者还没同步过。
      </p>

      <article
        v-for="item in kind.items"
        :key="item.id"
        class="card"
        :class="{ off: !item.enabled }"
      >
        <div class="card-head">
          <div class="who">
            <span class="name">{{ item.name }}</span>
            <span class="id">#{{ item.id }}</span>
          </div>
          <label class="toggle">
            <input
              type="checkbox"
              :checked="item.enabled"
              @change="
                actions.toggleCapability(item.type, item.id, ($event.target as HTMLInputElement).checked)
              "
            />
            启用
          </label>
        </div>
        <p v-if="item.description" class="desc">{{ item.description }}</p>
      </article>
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
  gap: 12px;
  max-width: 660px;
  margin: 0 auto 26px;
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

h3 {
  margin: 0;
  font-size: 13.5px;
  font-weight: 600;
}

.sub {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}

.sync {
  display: flex;
  align-items: center;
  gap: 10px;
}

.sync button {
  flex: 0 0 auto;
  white-space: nowrap;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px 15px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface);
  box-shadow: var(--shadow-1);
}

/* 停用的压暗但不隐藏：让用户看得见自己关了什么。 */
.card.off {
  opacity: 0.55;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.who {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.name {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.id {
  flex: 0 0 auto;
  color: var(--muted);
  font-family: var(--mono);
  font-size: 10.5px;
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

.desc {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
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
</style>
