<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import type { BrowserBridgeView } from "../../shared/types";
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

// ---------- 用我的浏览器（扩展） ----------

const bridge = ref<BrowserBridgeView | null>(null);
const tokenCopied = ref(false);
let stopBridge: (() => void) | null = null;

onMounted(async () => {
  stopBridge = window.aiclaw.on.browserBridge((view) => {
    bridge.value = view as BrowserBridgeView;
  });
  bridge.value = ((await window.aiclaw.browserBridge.status().catch(() => null)) as BrowserBridgeView | null);
});
onUnmounted(() => stopBridge?.());

/** 默认浏览器（主进程已经排在第一个）与其它装着的浏览器。 */
const defaultBrowser = computed(() => bridge.value?.browsers?.[0] ?? null);
const otherBrowsers = computed(() => (bridge.value?.browsers ?? []).slice(1));
const connecting = ref(false);

/**
 * 「连接我的浏览器」：切过去，扩展本来就装着的话它两三秒内自己连上来（或来配对），
 * 那就什么都不用开；没来就在默认浏览器里打开扩展页（上架后是商店页）。
 */
async function connectMyBrowser(): Promise<void> {
  connecting.value = true;
  try {
    await useBackend("extension");
    await new Promise((resolve) => setTimeout(resolve, 2500));
    if (bridge.value?.browser || bridge.value?.pairingCode) return;
    await openInBrowser(defaultBrowser.value?.id ?? "");
  } finally {
    connecting.value = false;
  }
}

async function useBackend(backend: "builtin" | "extension"): Promise<void> {
  await saveField({ browserBackend: backend });
  bridge.value = (await window.aiclaw.browserBridge.status()) as BrowserBridgeView;
}

async function copyToken(): Promise<void> {
  const token = store.config?.browserPairToken ?? "";
  if (!token) return;
  const ok = (await window.aiclaw.clipboard.write(token)) === true;
  tokenCopied.value = ok;
  if (ok) setTimeout(() => (tokenCopied.value = false), 1500);
}

async function repairToken(): Promise<void> {
  try {
    await window.aiclaw.browserBridge.repair();
    await actions.reloadConfig();
  } catch (error) {
    actions.showError(`重新生成失败：${describeError(error)}`);
  }
}

async function openInBrowser(browserId: string): Promise<void> {
  try {
    await window.aiclaw.browserBridge.openPage(browserId);
  } catch (error) {
    actions.showError(`打不开扩展页：${describeError(error)}`);
  }
}

async function revealExtension(): Promise<void> {
  try {
    await window.aiclaw.browserBridge.reveal();
  } catch (error) {
    actions.showError(describeError(error));
  }
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

/**
 * 菜单往下还是往上弹。
 *
 * 菜单是绝对定位在按钮下面的，而配置页整页在滚动容器里：页面下半部的选择器
 * （多模态那四个）往下弹会被容器底边裁掉，用户看到的是半截列表。按钮离窗口
 * 底部不够放一份菜单、而上面够的时候，改成往上弹。
 */
const MENU_HEIGHT = 320;
const menuUp = ref(false);
function placeMenu(event: MouseEvent): void {
  const button = (event.currentTarget as HTMLElement | null)?.getBoundingClientRect();
  if (!button) return;
  const below = window.innerHeight - button.bottom;
  menuUp.value = below < MENU_HEIGHT && button.top > below;
}

function openModelPicker(event: MouseEvent): void {
  modelPickerOpen.value = !modelPickerOpen.value;
  if (!modelPickerOpen.value) return;
  placeMenu(event);
  if (store.providers.length === 0 && !store.providersLoading) {
    void actions.loadProviders();
  }
  void nextTick(() => searchBox.value?.select());
}

/**
 * 选默认模型。窗口跟着模型走。
 *
 * 换了模型窗口就不一样了，旧值不能沿用。清单项里有窗口（「模型服务」页按
 * models.dev 标过）就填上——那个数字要去翻文档，没人愿意手抄；没有就清零，
 * 内核退回「等上游报超窗再压缩」。
 */
async function pickModel(choice: { providerId: number; model: string; context: number }): Promise<void> {
  modelPickerOpen.value = false;
  await saveField({
    providerId: choice.providerId,
    model: choice.model,
    contextWindow: choice.context,
  });
}

/**
 * 角色模型：对话之外的四件事各自交给一个模型。
 *
 * 候选来自「模型服务」页勾过对应能力的模型——不在这里列出全部模型让用户猜
 * 哪个能看图：那等于把标记这件事推给每一次选择。
 */
/** 窗口按量级换单位：272000 写成 272K 才读得出大小。 */
function formatWindow(tokens: number): string {
  if (!tokens) return "";
  return tokens >= 1000 ? `${Math.round(tokens / 1000)}K 上下文` : `${tokens} 上下文`;
}

const candidates = computed(() =>
  Object.fromEntries(MODEL_ROLES.map((role) => [role, roleCandidates(store.providers, role)])) as
    Record<ModelRole, ReturnType<typeof roleCandidates>>,
);

/** 哪个角色的选择器开着；空串都收着。搜索词共用一个：同一时间只开一个。 */
const rolePickerOpen = ref<ModelRole | "">("");
const roleSearch = ref("");
const roleSearchBox = ref<HTMLInputElement | null>(null);

function openRolePicker(role: ModelRole, event: MouseEvent): void {
  rolePickerOpen.value = rolePickerOpen.value === role ? "" : role;
  roleSearch.value = "";
  if (!rolePickerOpen.value) return;
  placeMenu(event);
  void nextTick(() => roleSearchBox.value?.focus());
}

/** 某个角色过滤后的候选。RoleCandidate 与 ModelChoice 同形，直接复用同一个过滤器。 */
function roleChoices(role: ModelRole) {
  return filterChoices(candidates.value[role], roleSearch.value);
}

/** 当前选中项的显示文字。 */
function rolePicked(role: ModelRole): { model: string; providerName: string } | null {
  const current = store.config?.roles?.[role];
  if (!current?.providerId || !current.model) return null;
  const provider = store.providers.find((item) => item.id === current.providerId);
  return { model: current.model, providerName: provider?.name ?? `服务 ${current.providerId}` };
}

/** `providerId/model`，给选择器当值用。空串表示没配。 */
function roleValue(role: ModelRole): string {
  const current = store.config?.roles?.[role];
  return current?.providerId && current.model ? `${current.providerId}/${current.model}` : "";
}

async function pickRole(role: ModelRole, value: string): Promise<void> {
  rolePickerOpen.value = "";
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
          <button class="field-button" @click="openModelPicker($event)">
            <span v-if="store.config.model" class="picked-name">
              {{ store.config.model }}
              <em v-if="currentProvider"> · {{ currentProvider.name }}</em>
            </span>
            <span v-else class="placeholder">从已配置的模型服务里选一个</span>
            <span class="chev">⌄</span>
          </button>

          <div v-if="modelPickerOpen" class="backdrop" @click="modelPickerOpen = false" />
          <div v-if="modelPickerOpen" class="menu" :class="{ up: menuUp }">
            <div class="menu-head">
              <span>模型<em v-if="modelSearch"> {{ choices.length }} / {{ allChoices.length }}</em></span>
              <button class="link" @click="actions.loadProviders()">刷新</button>
            </div>
            <input
              ref="searchBox"
              v-model="modelSearch"
              class="menu-search"
              placeholder="搜索模型或服务名"
              @keydown.enter="choices[0] && pickModel(choices[0])"
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
              @click="pickModel(choice)"
            >
              <span class="menu-name">{{ choice.model }}</span>
              <span class="menu-note">
                {{ choice.providerName }}<template v-if="choice.context"> · {{ formatWindow(choice.context) }}</template>
              </span>
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
            placeholder="选模型时自动填"
            :value="store.config.contextWindow || ''"
            @change="
              saveField({ contextWindow: Number(($event.target as HTMLInputElement).value) || 0 })
            "
          />
        </label>
      </div>
      <p class="note">
        上下文窗口填了才能在撑满之前主动压缩历史；不填也能跑，只是要等上游报错再压，
        白花一次请求。选模型时会用清单里记着的值自动填——那个值在「模型服务」页
        点「按 models.dev 标记能力」时一并写进去。
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

      <!-- 与默认模型同一套可搜索的选择器：一个服务标了几十个能看图的模型，
           原生 select 翻不动，也没法搜。 -->
      <div v-for="role in MODEL_ROLES" :key="role" class="field">
        <span class="field-label">{{ ROLE_LABELS[role] }}</span>
        <div class="picker">
          <button
            class="field-button"
            :disabled="candidates[role].length === 0"
            @click="openRolePicker(role, $event)"
          >
            <span v-if="rolePicked(role)" class="picked-name">
              {{ rolePicked(role)!.model }}<em> · {{ rolePicked(role)!.providerName }}</em>
            </span>
            <span v-else class="placeholder">
              {{ candidates[role].length === 0 ? "没有标记为「" + ROLE_LABELS[role] + "」的模型" : "不使用" }}
            </span>
            <span class="chev">⌄</span>
          </button>

          <div v-if="rolePickerOpen === role" class="backdrop" @click="rolePickerOpen = ''" />
          <div v-if="rolePickerOpen === role" class="menu" :class="{ up: menuUp }">
            <div class="menu-head">
              <span>
                {{ ROLE_LABELS[role] }}
                <em v-if="roleSearch"> {{ roleChoices(role).length }} / {{ candidates[role].length }}</em>
              </span>
            </div>
            <input
              ref="roleSearchBox"
              v-model="roleSearch"
              class="menu-search"
              placeholder="搜索模型或服务名"
              @keydown.enter="roleChoices(role)[0] && pickRole(role, `${roleChoices(role)[0]!.providerId}/${roleChoices(role)[0]!.model}`)"
              @keydown.esc="rolePickerOpen = ''"
            />
            <button class="menu-item" :class="{ picked: !roleValue(role) }" @click="pickRole(role, '')">
              <span class="menu-name">不使用</span>
            </button>
            <p v-if="roleChoices(role).length === 0" class="menu-note">没有匹配「{{ roleSearch }}」的模型。</p>
            <button
              v-for="item in roleChoices(role)"
              :key="`${item.providerId}/${item.model}`"
              class="menu-item"
              :class="{ picked: roleValue(role) === `${item.providerId}/${item.model}` }"
              @click="pickRole(role, `${item.providerId}/${item.model}`)"
            >
              <span class="menu-name">{{ item.model }}</span>
              <span class="menu-note">
                {{ item.providerName }}<template v-if="item.context"> · {{ formatWindow(item.context) }}</template>
              </span>
            </button>
          </div>
        </div>
        <span class="hint">{{ ROLE_HINTS[role] }}</span>
      </div>

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
      <label class="switch">
        <input
          type="checkbox"
          :checked="store.config.browser"
          @change="saveField({ browser: ($event.target as HTMLInputElement).checked })"
        />
        <span>浏览器：模型按元素编号打开网页、点、填、读（勾上后可以选用你自己的 Chrome / Edge）</span>
      </label>
      <div v-if="store.config.browser" class="field backend">
        <!-- 已连上：一行状态，外加改回去的入口。 -->
        <template v-if="store.config.browserBackend === 'extension' && bridge?.browser">
          <p class="bridge-status ok">
            <span class="dot" />在你的 {{ bridge.browser }} 里操作：后台标签页、用你已有的登录、不抢鼠标
          </p>
          <div class="row links">
            <button class="link" @click="useBackend('builtin')">改回 AIClaw 自带窗口</button>
          </div>
        </template>

        <!-- 自带窗口：一个按钮切过去。 -->
        <template v-else-if="store.config.browserBackend !== 'extension'">
          <p class="guide-lead">
            现在用 AIClaw 自带的浏览器窗口（独立的登录，窗口可见）。也可以让它在你自己的浏览器里、
            用你已有的登录、在后台标签页里操作：
          </p>
          <div class="row">
            <button class="primary" :disabled="connecting" @click="connectMyBrowser()">
              {{ connecting ? "正在打开…" : `连接我的浏览器${defaultBrowser ? `（${defaultBrowser.name}）` : ""}` }}
            </button>
          </div>
        </template>

        <!-- 连接中：按步骤打勾。 -->
        <template v-else>
          <ol class="progress">
            <li :class="bridge?.pairingCode ? 'done' : 'now'">安装扩展</li>
            <li :class="bridge?.pairingCode ? 'now' : ''">在浏览器里确认配对</li>
            <li>连上</li>
          </ol>

          <div v-if="bridge?.pairingCode" class="pairing">
            <span class="pair-code">{{ bridge.pairingCode }}</span>
            <span>浏览器里弹出了配对页：核对代码一致，在<strong>浏览器里</strong>点「允许」。</span>
          </div>
          <template v-else>
            <p v-if="defaultBrowser?.fromStore" class="guide-lead">
              在打开的商店页点「获取」，装好后浏览器会自动弹出配对页。
            </p>
            <p v-else-if="defaultBrowser" class="guide-lead">
              {{ defaultBrowser.name }} 的扩展页已经打开，扩展目录的路径也复制好了：打开「开发者模式」→
              点「加载已解压的扩展程序」→ 按 <kbd>Cmd</kbd>+<kbd>Shift</kbd>+<kbd>G</kbd> 粘贴、回车、点「选择」。
              装好后浏览器会自动弹出配对页。
            </p>
            <p v-else class="guide-lead">
              没找到 Chrome / Edge。在浏览器的扩展页里打开「开发者模式」，「加载已解压的扩展程序」选这个目录：
              <code class="path">{{ bridge?.extensionDir }}</code>
            </p>
            <div class="row links">
              <button v-if="defaultBrowser" class="link" @click="openInBrowser(defaultBrowser.id)">重新打开扩展页</button>
              <button
                v-for="item in otherBrowsers"
                :key="item.id"
                class="link"
                @click="openInBrowser(item.id)"
              >
                改用 {{ item.name }}
              </button>
              <button class="link" @click="revealExtension">在访达中显示扩展目录</button>
            </div>
          </template>
          <p v-if="bridge?.error" class="bridge-status bad"><span class="dot" />{{ bridge.error }}</p>
          <div class="row links">
            <button class="link" @click="useBackend('builtin')">取消，改回自带窗口</button>
          </div>
        </template>

        <details v-if="store.config.browserBackend === 'extension'" class="manual">
          <summary>它能做什么、怎么让它停下来</summary>
          <p>
            AIClaw 只在它自己开的后台标签页（「AIClaw」标签组）里操作，不切换你正在看的页面；要接管你已经打开的页面，
            它会先在 AIClaw 里请你确认。操作期间浏览器顶部会显示「正在调试此浏览器」，点「取消」就能让它立刻停手；
            空闲一分钟后提示条自己消失。
          </p>
          <p>
            配对页没弹出来？点浏览器工具栏上的 AIClaw 图标，把配对码粘进去：
            <span class="row token">
              <button :disabled="!store.config.browserPairToken" @click="copyToken">
                {{ tokenCopied ? "已复制" : "复制配对码" }}
              </button>
              <button :disabled="!store.config.browserPairToken" @click="repairToken">重新生成</button>
            </span>
            配对码等于这个浏览器的钥匙，别发给别人。
          </p>
        </details>
      </div>
      <p class="note">
        模型拿到的是页面上可交互元素的<strong>编号列表</strong>（链接、按钮、输入框），
        按编号操作，不靠屏幕坐标——比截图便宜、比坐标可靠。窗口是可见的，登录、验证码
        你随时能接手，登录态跨会话保留。<strong>打开网址会请你确认</strong>；页面里的点、填、滚不问，
        严格档位下每一步都问。网页内容一律当作不可信的外部资料交给模型。改动下一个会话生效。
      </p>
      <p class="note">
        computer use（截屏 + 鼠标键盘）是一个插件，开关在「插件」页：启用即授权，
        那一页写着它能碰什么。需要操作浏览器之外的应用时用它；只是上网的话浏览器工具更省更准。
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

.backend {
  padding: 10px 12px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  gap: 8px;
}

.bridge-status {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--muted);
}

.bridge-status .dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--muted);
  flex: 0 0 auto;
}

.bridge-status.ok {
  color: var(--ok);
}

.bridge-status.ok .dot {
  background: var(--ok);
}

.bridge-status.bad {
  color: var(--danger);
}

.bridge-status.bad .dot {
  background: var(--danger);
}

.progress {
  display: flex;
  gap: 18px;
  margin: 0;
  padding: 0;
  list-style: none;
  counter-reset: step;
  font-size: 12.5px;
  color: var(--muted);
}

.progress li {
  display: flex;
  align-items: center;
  gap: 6px;
  counter-increment: step;
}

.progress li::before {
  content: counter(step);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: 1px solid var(--rule-strong);
  border-radius: 50%;
  font-size: 10.5px;
}

.progress li.now {
  color: var(--ink);
  font-weight: 600;
}

.progress li.now::before {
  border-color: var(--ok);
  color: var(--ok);
}

.progress li.done {
  color: var(--ok);
}

.progress li.done::before {
  content: "✓";
  border-color: var(--ok);
  background: var(--ok);
  color: #fff;
}

.links {
  flex-wrap: wrap;
  gap: 14px;
}

button.primary {
  padding: 6px 16px;
  border: 1px solid var(--ok);
  border-radius: var(--r-md);
  background: var(--ok);
  color: #fff;
  font-size: 12.5px;
  cursor: pointer;
}

button.primary:disabled {
  opacity: 0.6;
  cursor: default;
}

.pairing {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: var(--r-md);
  background: var(--ok-soft);
  color: var(--ink);
  font-size: 12.5px;
  line-height: 1.6;
}

.pair-code {
  flex: 0 0 auto;
  font-family: var(--mono);
  font-size: 22px;
  font-weight: 650;
  letter-spacing: 4px;
  color: var(--ok);
}

.guide-lead {
  margin: 0;
  font-size: 12px;
  color: var(--ink-2);
}

.open-buttons {
  flex-wrap: wrap;
}

.manual {
  font-size: 12px;
  color: var(--muted);
}

.manual summary {
  cursor: pointer;
}

.manual p {
  margin: 6px 0 0;
  line-height: 1.8;
}

kbd {
  padding: 0 4px;
  border: 1px solid var(--rule-strong);
  border-radius: 3px;
  font-family: var(--mono);
  font-size: 11px;
}

.steps {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  line-height: 1.8;
  color: var(--ink-2);
}

.path {
  display: block;
  word-break: break-all;
  font-size: 11px;
  color: var(--muted);
}

.token {
  display: inline-flex;
  margin-left: 4px;
}

.steps button {
  font-size: 11.5px;
  padding: 2px 8px;
}

.link {
  border: none;
  background: none;
  padding: 0;
  color: var(--accent, var(--ok));
  cursor: pointer;
  text-decoration: underline;
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
.menu.up {
  top: auto;
  bottom: calc(100% + 4px);
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
