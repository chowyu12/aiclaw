<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import { actions, filterChoices, modelChoices, store } from "../store";
import { describeError } from "../errors";
import {
  MODEL_ROLES,
  ROLE_HINTS,
  ROLE_LABELS,
  roleCandidates,
  type ModelRole,
} from "../model-roles";

const modelPickerOpen = ref(false);
const saving = ref(false);
const purgeConfirm = ref(false);

/**
 * 诊断信息：一段能直接复制给别人的文本。
 *
 * 出了研发范围之后，同事报「它不工作」而我们手上什么都没有——版本、平台、
 * 挂载状态、内核最近报了什么，每次都要一问一答。凭据在宿主侧已经抹过，
 * 见 main/diagnostics.ts 的 redact。
 */
const report = ref("");
const reportOpen = ref(false);
const copied = ref(false);

async function loadReport(): Promise<void> {
  reportOpen.value = !reportOpen.value;
  if (reportOpen.value) report.value = (await window.aiclaw.diagnostics.read()) as string;
}

async function copyReport(): Promise<void> {
  await navigator.clipboard.writeText(report.value);
  copied.value = true;
  setTimeout(() => (copied.value = false), 1500);
}

async function saveField(patch: Record<string, unknown>): Promise<void> {
  saving.value = true;
  try {
    await actions.saveConfig(patch);
  } catch (error) {
    // 存不下去要说出来。不接住的话它只是一条未处理的 promise 拒绝，
    // 界面上看起来就是「点了没反应」——而那正是这一页最难排查的故障。
    actions.showError(`保存失败：${describeError(error)}`);
  } finally {
    saving.value = false;
  }
}

/**
 * 默认模型从各模型服务的清单里挑，不手打。
 *
 * 手打模型名是这一页最容易出错的一格：名字写错了要等到第一次对话报 404 才知道，
 * 而那时错误来自上游、看起来像服务坏了。清单在「模型服务」页维护。
 */
const allChoices = computed(() => modelChoices(store.providers));
/** 搜索关键词。一个服务能列出上百个模型，翻找不现实。 */
const modelSearch = ref("");
const choices = computed(() => filterChoices(allChoices.value, modelSearch.value));
const searchBox = ref<HTMLInputElement | null>(null);
const currentProvider = computed(() =>
  store.providers.find((item) => item.id === store.config?.providerId),
);

function openModelPicker(): void {
  modelPickerOpen.value = !modelPickerOpen.value;
  if (!modelPickerOpen.value) return;
  if (store.providers.length === 0 && !store.providersLoading) {
    void actions.loadProviders();
  }
  void nextTick(() => searchBox.value?.select());
}

async function pickModel(providerId: number, id: string): Promise<void> {
  modelPickerOpen.value = false;
  // 换了模型窗口就不一样了，旧值不能沿用；用户知道的话在下面那格填。
  await saveField({ providerId, model: id, contextWindow: 0 });
}

/**
 * 角色模型：对话之外的四件事各自交给一个模型。
 *
 * 候选来自「模型服务」页勾过对应能力的模型——不在这里列出全部模型让用户猜
 * 哪个能看图：那等于把标记这件事推给每一次选择。
 */
const candidates = computed(() =>
  Object.fromEntries(MODEL_ROLES.map((role) => [role, roleCandidates(store.providers, role)])) as
    Record<ModelRole, ReturnType<typeof roleCandidates>>,
);

/** `providerId/model`，给 select 当值用。空串表示没配。 */
function roleValue(role: ModelRole): string {
  const current = store.config?.roles?.[role];
  return current?.providerId && current.model ? `${current.providerId}/${current.model}` : "";
}

async function pickRole(role: ModelRole, value: string): Promise<void> {
  const [providerId, ...rest] = value.split("/");
  const next = {
    ...(store.config?.roles ?? {}),
    [role]: value
      ? { providerId: Number(providerId), model: rest.join("/") }
      : { providerId: 0, model: "" },
  };
  await saveField({ roles: next });
}

async function purge(): Promise<void> {
  await window.aiclaw.data.purgeAll();
  purgeConfirm.value = false;
  await actions.bootstrap();
}
</script>

<template>
  <!-- config 为 null 只会是 bootstrap 失败。别留白屏——白屏没有任何线索。 -->
  <div class="settings" v-if="!store.config">
    <section>
      <h2>配置未能加载</h2>
      <p class="note warn">
        本地配置没读出来，页面暂时不能用。具体原因见页面顶部的错误条；
        没有错误条的话，重新执行 <code>make build</code> 再启动。
      </p>
      <button @click="actions.bootstrap()">重试</button>
    </section>
  </div>

  <div class="settings" v-else>
    <section>
      <header>
        <h2>模型</h2>
        <p class="sub">新会话默认用哪个模型。端点、Key 与模型清单在「模型服务」页管理。</p>
      </header>

      <p v-if="choices.length === 0 && !store.providersLoading" class="note warn">
        还没有能用的模型服务。到「模型服务」页添加一个端点、填上 Key、写上模型名，回来再选。
        <button class="link" @click="actions.setView('providers')">去添加</button>
      </p>

      <!-- 不用 <label> 包这个选择器。label 会把落在它里面非交互元素上的点击转发给
           第一个可标注的后代——也就是那个 field-button：点遮罩想关掉，转发一次
           又把它打开了，看起来就是「选了关不掉、整页点不动」。 -->
      <div class="field">
        <span class="field-label">默认模型</span>
        <div class="picker">
          <button class="field-button" @click="openModelPicker()">
            <span v-if="store.config.model" class="picked-name">
              {{ store.config.model }}
              <em v-if="currentProvider"> · {{ currentProvider.name }}</em>
            </span>
            <span v-else class="placeholder">从已配置的模型服务里选一个</span>
            <span class="chev">⌄</span>
          </button>

          <div v-if="modelPickerOpen" class="backdrop" @click="modelPickerOpen = false" />
          <div v-if="modelPickerOpen" class="menu">
            <div class="menu-head">
              <span>模型<em v-if="modelSearch"> {{ choices.length }} / {{ allChoices.length }}</em></span>
              <button class="link" @click="actions.loadProviders()">刷新</button>
            </div>
            <input
              ref="searchBox"
              v-model="modelSearch"
              class="menu-search"
              placeholder="搜索模型或服务名"
              @keydown.enter="choices[0] && pickModel(choices[0].providerId, choices[0].model)"
              @keydown.esc="modelPickerOpen = false"
            />
            <p v-if="store.providersLoading" class="menu-note">正在读取…</p>
            <p v-else-if="store.providersError" class="menu-note warn">{{ store.providersError }}</p>
            <p v-else-if="allChoices.length === 0" class="menu-note">没有能用的模型。</p>
            <p v-else-if="choices.length === 0" class="menu-note">没有匹配「{{ modelSearch }}」的模型。</p>
            <button
              v-for="choice in choices"
              :key="`${choice.providerId}/${choice.model}`"
              class="menu-item"
              :class="{ picked: choice.providerId === store.config.providerId && choice.model === store.config.model }"
              @click="pickModel(choice.providerId, choice.model)"
            >
              <span class="menu-name">{{ choice.model }}</span>
              <span class="menu-note">{{ choice.providerName }}</span>
            </button>
          </div>
        </div>
        <span class="hint">
          没设过的话第一次启动会自动挑一个。新会话用它；对话框上方切模型只影响那一个会话。
        </span>
      </div>

      <div class="pair">
        <label>
          <span>推理档位</span>
          <select
            :value="store.config.reasoningEffort"
            @change="saveField({ reasoningEffort: ($event.target as HTMLSelectElement).value })"
          >
            <option value="low">low</option>
            <option value="medium">medium</option>
            <option value="high">high</option>
          </select>
        </label>
        <label>
          <span>上下文窗口</span>
          <input
            type="number"
            min="0"
            step="1000"
            placeholder="不知道就留空"
            :value="store.config.contextWindow || ''"
            @change="
              saveField({ contextWindow: Number(($event.target as HTMLInputElement).value) || 0 })
            "
          />
        </label>
      </div>
      <p class="note">
        上下文窗口填了才能在撑满之前主动压缩历史；不填也能跑，只是要等上游报错再压，
        白花一次请求。
      </p>
    </section>

    <section>
      <header>
        <h2>多模态</h2>
        <p class="sub">
          对话之外的几件事各自交给一个模型。候选来自「模型服务」页上勾过对应能力的模型——
          没有哪个对话模型四样都好，而你手上往往各有一个便宜的专用模型。
        </p>
      </header>

      <label v-for="role in MODEL_ROLES" :key="role">
        <span>{{ ROLE_LABELS[role] }}</span>
        <select
          :value="roleValue(role)"
          :disabled="candidates[role].length === 0"
          @change="pickRole(role, ($event.target as HTMLSelectElement).value)"
        >
          <option value="">
            {{ candidates[role].length === 0 ? "没有标记为「" + ROLE_LABELS[role] + "」的模型" : "不使用" }}
          </option>
          <option
            v-for="item in candidates[role]"
            :key="`${item.providerId}/${item.model}`"
            :value="`${item.providerId}/${item.model}`"
          >
            {{ item.model }} · {{ item.providerName }}
          </option>
        </select>
        <span class="hint">{{ ROLE_HINTS[role] }}</span>
      </label>

      <p class="note">
        没配的角色对应的工具不会出现在会话里——给模型一个用不了的工具，它会调、
        会失败、会重试，而失败原因它无从修复。看图是例外：它不是工具，而是在
        对话模型不认图时替它读图。
      </p>
    </section>

    <section>
      <header>
        <h2>执行</h2>
        <p class="sub">Agent 在这台电脑上能碰什么、动手前问不问你。</p>
      </header>
      <p class="note">
        工作区是<strong>按会话</strong>设的，不在这里——在对话页顶部那个「工作区」上点一下就能改，
        也可以不设。它决定相对路径按哪儿解析、写哪里不用问你。
      </p>
      <p class="note warn">
        本版本没有沙箱：Agent 的命令直接在你的电脑上执行。
        危险命令（如 <code>rm -rf /</code>）硬拒绝；删除、提权、改系统设置这类会先问你；
        普通命令不问。读文件不限于工作区，但涉及凭据的目录（<code>~/.ssh</code> 这些）一律拒绝。
      </p>
      <label>
        <span>审批档位</span>
        <select
          :value="store.config.profile"
          @change="saveField({ profile: ($event.target as HTMLSelectElement).value })"
        >
          <option v-for="p in store.profiles" :key="p.id" :value="p.id">
            {{ p.label }}
          </option>
        </select>
      </label>
      <p class="note">
        {{ store.profiles.find((p) => p.id === store.config?.profile)?.description }}
      </p>

      <label class="switch">
        <input
          type="checkbox"
          :checked="store.config.sandboxCommands"
          @change="
            saveField({ sandboxCommands: ($event.target as HTMLInputElement).checked })
          "
        />
        <span>没经过确认的命令跑在系统沙箱里（macOS）</span>
      </label>
      <p class="note">
        开着的时候，不需要确认的命令由系统内核限制：只能写会话工作区与临时目录，
        读不到 <code>~/.ssh</code> 这类凭据目录。<strong>你点过「允许」的命令不受限制</strong>——
        那正是确认的含义。只有某个命令被沙箱挡了、而你确定它没问题时才需要关掉它。
        Windows 上没有这一层。
      </p>

      <label class="switch">
        <input
          type="checkbox"
          :checked="store.config.codeMode"
          @change="saveField({ codeMode: ($event.target as HTMLInputElement).checked })"
        />
        <span>代码模式：工具收进一个 <code>exec</code>，模型写 JavaScript 调用</span>
      </label>
      <p class="note">
        <strong>工具多才划算</strong>：只有内置那几个工具时基本打平（exec 的说明本身有固定开销），
        而 161 个接口的定义原本约 110KB，收进去之后是 7KB 左右，
        而这部分<strong>每次请求都要重发、且不参与上下文压缩</strong>。
        更大的收益是十几次查询可以写成一段循环，中间结果不再进上下文。
        代价是模型得会写对代码——小模型在这上面更吃力，换模型之后值得再试一次。
        脚本里的每次工具调用<strong>照常走审批</strong>，也照常受沙箱限制。
        开了之后，对话页顶部「工具」菜单里会显示实际省了多少。改动下一个会话生效。
      </p>
      <p class="note">
        computer use（截屏 + 鼠标键盘）是一个插件，开关在「插件」页：启用即授权，
        那一页写着它能碰什么。
      </p>

    </section>

    <section>
      <header>
        <h2>数据</h2>
        <p class="sub">会话与执行记录全部留在本机，不回写云端。</p>
      </header>
      <p class="note">模型服务那边只能看到发给它的请求。</p>
      <label>
        <span>会话保留天数</span>
        <input
          type="number"
          min="0"
          :value="store.config.retentionDays"
          @change="saveField({ retentionDays: Number(($event.target as HTMLInputElement).value) })"
        />
      </label>
      <div v-if="!purgeConfirm">
        <button class="danger" @click="purgeConfirm = true">清空全部本地数据</button>
      </div>
      <div v-else class="row">
        <span class="note">会话记录、凭据、配置将一并清除，无法恢复。</span>
        <button class="ghost" @click="purgeConfirm = false">取消</button>
        <button class="danger" @click="purge()">确认清空</button>
      </div>
    </section>

    <section>
      <header>
        <h2>诊断</h2>
        <p class="sub">
          版本、平台、挂载结果与内核最近的输出，拼成一段可以直接贴给别人的文本。
          凭据已经抹掉，只会显示「已配置 / 未配置」。
        </p>
      </header>
      <div class="row">
        <button class="ghost" @click="loadReport()">
          {{ reportOpen ? "收起" : "查看诊断信息" }}
        </button>
        <button v-if="reportOpen" class="ghost" @click="copyReport()">
          {{ copied ? "已复制" : "复制" }}
        </button>
      </div>
      <pre v-if="reportOpen" class="report">{{ report }}</pre>
    </section>

    <p class="saving" v-if="saving">保存中…</p>
  </div>
</template>

<style scoped>
.report {
  max-height: 320px;
  margin: 0;
  padding: 12px 14px;
  overflow: auto;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface-2);
  color: var(--ink-2);
  font-family: var(--mono);
  font-size: 11px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}

.settings {
  height: 100%;
  overflow-y: auto;
  padding: 28px 28px 64px;
}

/* 每段一张卡片。原来是几条横线分隔的长表单，扫下来分不出哪几项是一组的。 */
section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 620px;
  margin: 0 auto 18px;
  padding: 20px 22px;
  border: 1px solid var(--rule);
  border-radius: var(--r-lg);
  background: var(--surface);
  box-shadow: var(--shadow-1);
}

header {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

h2 {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
  letter-spacing: 0.01em;
}

.sub {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
}

label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
}

/* 与 label 同一套外观，但不是 label——见模板里的说明。 */
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
}

.field-label,
label > span:first-child {
  color: var(--ink-2);
  font-size: 12px;
  font-weight: 500;
  display: flex;
  align-items: center;
  gap: 8px;
}

.hint {
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.55;
}

.switch {
  flex-direction: row;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}

.switch input {
  width: auto;
}

.switch > span {
  color: var(--ink);
  font-size: 13px;
}

/* 两个短字段并排，省掉一半纵向滚动。 */
.pair {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}

label em {
  font-style: normal;
  font-family: var(--mono);
  font-size: 10px;
  color: var(--ok);
  background: var(--ok-soft);
  border-radius: var(--r-full);
  padding: 1px 7px;
}

.row {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* 按钮不跟着挤：不加这两条，「选择…」会被压成两行竖排。 */
.row button {
  flex: 0 0 auto;
  white-space: nowrap;
}

.note {
  font-size: 12px;
  color: var(--muted);
  line-height: 1.7;
  margin: 0;
}

.note.warn {
  color: var(--danger);
  background: var(--danger-soft);
  padding: 10px 13px;
  border-radius: var(--r-md);
}

.saving {
  position: sticky;
  bottom: 0;
  text-align: center;
  font-size: 12px;
  color: var(--muted);
}

/* ---------- 高级 ---------- */

/* ---------- 模型选择器 ---------- */

.picker {
  position: relative;
}

.field-button {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--rule-strong);
  border-radius: var(--r-sm);
  background: var(--surface);
  color: inherit;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.field-button:hover {
  border-color: var(--muted);
}

.picked-name {
  font-family: var(--mono);
  font-size: 12.5px;
}

.picked-name em {
  color: var(--muted);
  font-style: normal;
  font-family: inherit;
}

.placeholder {
  color: var(--muted);
}

.chev {
  color: var(--muted);
  font-size: 12px;
}

.backdrop {
  position: fixed;
  inset: 0;
  z-index: 10;
}

.menu {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  z-index: 20;
  display: flex;
  flex-direction: column;
  max-height: 300px;
  overflow-y: auto;
  padding: 4px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface);
  box-shadow: var(--shadow-3);
}

.menu-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 9px 4px;
  color: var(--muted);
  font-size: 11px;
}

.menu-head em {
  font-style: normal;
}

/* 搜索框不跟着列表滚：菜单里一滚就找不到输入框在哪儿了。 */
.menu-search {
  position: sticky;
  top: 0;
  z-index: 1;
  width: calc(100% - 8px);
  margin: 0 4px 4px;
  padding: 5px 8px;
  border: 1px solid var(--rule);
  border-radius: var(--r-sm);
  background: var(--surface);
  color: inherit;
  font-size: 12.5px;
}

.link {
  border: none;
  background: none;
  color: var(--accent);
  font-size: 11px;
  cursor: pointer;
  padding: 0;
}

.menu-item {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 7px 9px;
  border: none;
  border-radius: var(--r-sm);
  background: none;
  text-align: left;
  color: inherit;
  cursor: pointer;
}

.menu-item:hover {
  background: var(--hover);
}

.menu-item.picked {
  background: var(--active);
}

.menu-name {
  font-family: var(--mono);
  font-size: 12.5px;
}

.menu-note {
  color: var(--muted);
  font-size: 11px;
  line-height: 1.5;
  margin: 0;
  padding: 0 9px;
}

.menu-item .menu-note {
  padding: 0;
}

.menu-note.warn {
  color: var(--danger);
}
</style>
