<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { actions, filterChoices, modelChoices, store } from "../store";
import { describeError } from "../errors";
import {
  classifyFile,
  readAudio,
  inlineText,
  readImage,
  readTextFile,
  type Attachment,
  type AudioAttachment,
  type ImageAttachment,
  type TextAttachment,
} from "../attachments";
import { groupTurns, stepsElapsed, type Turn } from "../turns";
import { renderMarkdown } from "../markdown";
import { answerText, answerTime, formatMessageTime, fullMessageTime } from "../message-meta";
import StepsBlock from "./StepsBlock.vue";

defineProps<{ configured: boolean }>();

const draft = ref("");
const scroller = ref<HTMLElement | null>(null);
const modelOpen = ref(false);
const policyOpen = ref(false);
const statusOpen = ref(false);

async function scrollToEnd(): Promise<void> {
  await nextTick();
  const element = scroller.value;
  if (element) element.scrollTop = element.scrollHeight;
}

watch(
  () => store.timeline.length,
  () => void scrollToEnd(),
);
watch(
  // 流式增量不改变条目数量，所以还要盯住末尾那条的长度。
  () => {
    const last = store.timeline[store.timeline.length - 1];
    if (!last) return 0;
    return last.kind === "step" ? last.detail.length : last.text.length;
  },
  () => void scrollToEnd(),
);

// ---------- 附件 ----------
//
// 贴一张报错截图问「这是什么」是最常见的用法之一。图片走视觉通道发给模型，
// 文本文件并进正文（模型没有别的办法读到它——那个文件可能在任何地方）。

const attachments = ref<Attachment[]>([]);
const attachError = ref("");
const picker = ref<HTMLInputElement | null>(null);

async function accept(files: FileList | File[] | null | undefined): Promise<void> {
  attachError.value = "";
  for (const file of Array.from(files ?? [])) {
    const images = attachments.value.filter((item) => item.kind === "image").length;
    const audio = attachments.value.filter((item) => item.kind === "audio").length;
    const verdict = classifyFile(file, images, audio);
    try {
      if (verdict.accept === "image") attachments.value.push(await readImage(file));
      else if (verdict.accept === "audio") attachments.value.push(await readAudio(file));
      else if (verdict.accept === "text") attachments.value.push(await readTextFile(file));
      // 拒绝的要说出来。默默忽略的话，用户以为模型看过了那份文件。
      else attachError.value = verdict.reason;
    } catch (error) {
      attachError.value = `${file.name} 读不了：${describeError(error)}`;
    }
  }
}

function onPaste(event: ClipboardEvent): void {
  const files = Array.from(event.clipboardData?.files ?? []);
  if (files.length === 0) return;
  // 有文件时才拦：否则粘文字也被吃掉了。
  event.preventDefault();
  void accept(files);
}

function onDrop(event: DragEvent): void {
  event.preventDefault();
  dragging.value = false;
  void accept(event.dataTransfer?.files);
}

const dragging = ref(false);
const workspaceOpen = ref(false);

/** 工具条上只放最后一段目录名，全路径在 title 和菜单里。 */
const workspaceLabel = computed(() => {
  const path = store.sessionInfo?.workspace ?? "";
  if (!path) return "未设工作区";
  const parts = path.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? path;
});

const workspaceTitle = computed(() =>
  store.sessionInfo?.workspace
    ? `会话工作区：${store.sessionInfo.workspace}`
    : "这个会话没有设置工作区，点一下可以指定",
);

/**
 * 对话正文里点到文件名时打开它。
 *
 * 只认我们自己标出来的 `.file-ref`——模型即使伪造一个同名 class 也拿不到
 * 额外能力，真正的校验在主进程（路径按工作区解析、可执行的只在访达里显示、
 * 凭据目录拒绝）。
 */
function onProseClick(event: MouseEvent): void {
  const target = (event.target as HTMLElement | null)?.closest?.(".file-ref");
  if (!target) return;
  event.preventDefault();
  void actions.openFile(target.textContent ?? "");
}

async function chooseWorkspace(): Promise<void> {
  workspaceOpen.value = false;
  await actions.pickWorkspace();
}

async function clearWorkspace(): Promise<void> {
  workspaceOpen.value = false;
  await actions.setWorkspace("");
}

function removeAttachment(index: number): void {
  attachments.value.splice(index, 1);
}

/** 附件上那行字。音频标出大小——转写按时长收费，用户该知道自己发了多大一段。 */
function attachmentLabel(item: Attachment): string {
  if (item.kind === "image") return item.name;
  if (item.kind === "audio") return `${item.name}（${(item.size / 1024 / 1024).toFixed(1)} MB）`;
  return `${item.name}${item.truncated ? "（已截断）" : ""}`;
}

// 轮次进行中也允许发：内核会把输入排进那一轮，模型下一次开口前就看到了。
// 拦住不让发是最难受的——用户想插话，正是因为看见这一轮跑偏了。
async function submit(): Promise<void> {
  const pending = attachments.value;
  // 文本附件并进正文，图片单独走视觉通道。
  const text =
    draft.value +
    pending
      .filter((item): item is TextAttachment => item.kind === "text")
      .map(inlineText)
      .join("");
  const images = pending
    .filter((item): item is ImageAttachment => item.kind === "image")
    .map((item) => item.data);
  // 音频不并进正文：它要先交给听写模型，转写由内核接在消息后面。
  const audio = pending
    .filter((item): item is AudioAttachment => item.kind === "audio")
    .map((item) => ({ name: item.name, data: item.data }));
  if (!text.trim() && images.length === 0 && audio.length === 0) return;
  draft.value = "";
  attachments.value = [];
  attachError.value = "";
  await actions.send(text, images, audio);
  await scrollToEnd();
}

// Enter 发送、Shift+Enter 换行。中文输入法组字期间的 Enter 不能当发送，
// 否则选词就把半句话发出去了。
function onKeydown(event: KeyboardEvent): void {
  if (event.key !== "Enter" || event.shiftKey || event.isComposing) return;
  event.preventDefault();
  void submit();
}

const policy = computed(() =>
  store.profiles.find((profile) => profile.id === store.config?.profile),
);

/** 窗口按量级换单位：272000 写成 272K 才读得出大小。 */
function formatWindow(tokens: number): string {
  if (!tokens) return "";
  return tokens >= 1000 ? `${Math.round(tokens / 1000)}K 上下文` : `${tokens} 上下文`;
}

/** 能选的模型：每个能用的模型服务下的每个模型。 */
const allChoices = computed(() => modelChoices(store.providers));
/** 搜索关键词。一个服务能列出上百个模型，翻找不现实。 */
const modelSearch = ref("");
const choices = computed(() => filterChoices(allChoices.value, modelSearch.value));
const searchBox = ref<HTMLInputElement | null>(null);

function openModelMenu(): void {
  modelOpen.value = !modelOpen.value;
  if (!modelOpen.value) return;
  if (store.providers.length === 0 && !store.providersLoading) {
    void actions.loadProviders();
  }
  // 打开就能直接打字。上一次的关键词留着：连着换两个同系列的模型时省一次输入。
  void nextTick(() => searchBox.value?.select());
}

async function pickModel(choice: { providerId: number; model: string; context: number }): Promise<void> {
  modelOpen.value = false;
  await actions.switchModel(choice.providerId, choice.model, choice.context);
}

async function pickPolicy(id: string): Promise<void> {
  policyOpen.value = false;
  await actions.saveConfig({ profile: id as "on-write" | "always" | "never" | "bypass" });
}

// ---------- 复制 ----------

/** 刚复制过的那一条（key），按钮上显示「已复制」一会儿，让人知道点到了。 */
const copiedKey = ref("");
let copiedTimer: ReturnType<typeof setTimeout> | undefined;

/** 复制失败的那一条，按钮上显示「复制失败」。说不出失败的「已复制」比没有按钮更糟。 */
const failedKey = ref("");

async function copyText(key: string, text: string): Promise<void> {
  if (!text) return;
  let ok = false;
  try {
    // 主进程写系统剪贴板，不受窗口焦点影响；渲染层的剪贴板 API 只作后备。
    ok = (await window.aiclaw.clipboard.write(text)) === true;
  } catch {
    try {
      await navigator.clipboard.writeText(text);
      ok = true;
    } catch {
      ok = false;
    }
  }
  copiedKey.value = ok ? key : "";
  failedKey.value = ok ? "" : key;
  clearTimeout(copiedTimer);
  copiedTimer = setTimeout(() => {
    copiedKey.value = "";
    failedKey.value = "";
  }, 1500);
}

function copyLabel(key: string): string {
  if (copiedKey.value === key) return "已复制";
  if (failedKey.value === key) return "复制失败";
  return "复制";
}

/** 这一轮的回答说完了没有：还在流式输出的时候不给复制，复制到的是半句话。 */
function answerDone(turn: Turn): boolean {
  return turn.messages.length > 0 && turn.messages.every((message) => !message.streaming);
}

/** 按轮分组：一条提问 + 它触发的全部步骤 + 全部回答。 */
const turns = computed<Turn[]>(() => groupTurns(store.timeline));

/**
 * 正在跑的是哪一轮。只有最后一轮可能在跑。
 *
 * 用 busy 而不是「有没有 state==running 的步骤」：轮次刚开始、第一次采样的
 * 事件还没到的那一小段里没有任何步骤，但那时已经在跑了。
 */
const runningKey = computed(() => {
  if (!store.busy) return "";
  return turns.value[turns.value.length - 1]?.key ?? "";
});

/**
 * 步骤块的展开状态：**只记用户点过的**，没点过的按当轮状态取默认值。
 *
 * 这样「跑完自动收起」是免费的——默认值从「跑着＝展开」翻到「跑完＝收起」，
 * 而用户点过的那一块不受影响。如果改成记一个 collapsed 集合，跑完那一刻
 * 就得去补写一次状态，而那次补写会把用户自己展开的也一起收掉。
 */
const stepsOverride = ref(new Map<string, boolean>());

function stepsOpen(turn: Turn): boolean {
  return stepsOverride.value.get(turn.key) ?? turn.key === runningKey.value;
}

function toggleSteps(turn: Turn): void {
  // 换一个新 Map 而不是原地改：ref 里装的 Map 改内容不会触发重渲染。
  const next = new Map(stepsOverride.value);
  next.set(turn.key, !stepsOpen(turn));
  stepsOverride.value = next;
}

/** 耗时按量级换单位：毫秒级的东西写成 0.02 s 读不出快慢。 */
function formatDuration(ms: number | undefined): string {
  if (ms === undefined) return "";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

/** 步骤块折叠时那一行：跑着的时候说在跑，跑完了说花了多久。 */
function stepsSummary(turn: Turn): string {
  if (turn.key === runningKey.value) return `执行中 · 已 ${turn.steps.length} 步`;
  const elapsed = stepsElapsed(turn.steps);
  const count = `${turn.steps.length} 个执行步骤`;
  return elapsed ? `${count} · 用时 ${formatDuration(elapsed)}` : count;
}

/**
 * 挂载状态。
 *
 * 「MCP server 到底挂上了没有」原来只能靠模型自己说，而模型经常只捡它想说的讲。
 * 这里如实列出内核报回来的每一项：成功的写挂了几个工具，失败的把原因原样带出来。
 */
const mounts = computed(() => {
  const entries = Object.entries(store.sessionInfo?.mcpStatus ?? {});
  // 失败的排前面：面板会滚动，把唯一需要动手的那条埋在下面等于没显示。
  return entries.sort((a, b) => Number(b[1].includes("失败")) - Number(a[1].includes("失败")));
});
const failedMounts = computed(() =>
  mounts.value.filter(([, status]) => status.includes("失败")),
);
/** 代码模式下收进 exec 的那些也算：它们在脚本里照样能调。 */
const folded = computed(() => store.sessionInfo?.foldedTools ?? []);
const toolCount = computed(() => (store.sessionInfo?.tools.length ?? 0) + folded.value.length);
</script>

<template>
  <div class="chat">
    <div v-if="!configured" class="gate">
      <h2>先完成配置</h2>
      <p>到「模型服务」页添加一个端点、填上 Key、写上模型名，再回来选一个默认模型。</p>
    </div>

    <template v-else>
      <!-- 应用启动时会自己把运行时拉起来，所以「启动中」是打开应用后最常见的
           一屏，不能再显示成「未启动」配一个按钮——那会让人以为要自己点。 -->
      <div v-if="store.runtime.state === 'starting'" class="gate">
        <h2>正在启动本地运行时…</h2>
        <p>
          在本机拉起 Agent 执行内核。
        </p>
      </div>

      <div v-else-if="store.runtime.state !== 'ready'" class="gate">
        <h2>{{ store.runtime.state === "failed" ? "本地运行时启动失败" : "本地运行时未启动" }}</h2>
        <p>
          启动后会在本机拉起 Agent 执行内核。命令会直接在这台电脑上执行，没有沙箱。
        </p>
        <button class="primary" @click="actions.startRuntime()">
          {{ store.runtime.state === "failed" ? "重试" : "启动并开始对话" }}
        </button>
      </div>

      <template v-else>
        <div ref="scroller" class="stream">
          <!-- 切会话时先切过去、历史随后到：这几秒里要有话说，不能是「还没有内容」，
               那句话在一个有几十条记录的会话上是假的。 -->
          <p v-if="store.loadingSession === store.sessionId && store.timeline.length === 0" class="blank">
            正在载入这个会话…
          </p>
          <p v-else-if="store.timeline.length === 0" class="blank">
            这个会话还没有内容。说点什么开始。
          </p>

          <!-- 按轮渲染。一轮里步骤只出现一次，位置随状态走：
               跑着的时候在回答上面（那时用户看的就是它，且回答还没出来），
               跑完了挪到回答下面并收起（那时用户要读的是答案，不是过程）。 -->
          <template v-for="turn in turns" :key="turn.key">
            <!-- 用户消息是纯文本气泡：他自己打的字，不该被 Markdown 重新解释
                 （写个 *星号* 不该变成斜体）。助手消息才渲染 Markdown。 -->
            <div v-if="turn.user" class="msg user">
              <div class="bubble">
                <div v-if="(turn.user.images ?? []).length > 0" class="shots">
                  <img
                    v-for="(shot, index) in turn.user.images"
                    :key="index"
                    :src="shot"
                    alt="附带的图片"
                  />
                </div>
                {{ turn.user.text }}
              </div>
            </div>
            <!-- 时间与复制放在气泡外面一行：塞进气泡里会和正文挤在一起，
                 而且用户复制的只是自己打的字，不该带上时间。 -->
            <div v-if="turn.user" class="msg-meta user-meta">
              <time v-if="turn.user.at" :title="fullMessageTime(turn.user.at)">
                {{ formatMessageTime(turn.user.at) }}
              </time>
              <button
                class="meta-copy"
                :title="copiedKey === `u-${turn.key}` ? '已复制' : '复制这条消息'"
                @click="copyText(`u-${turn.key}`, turn.user.text)"
              >
                {{ copyLabel(`u-${turn.key}`) }}
              </button>
            </div>

            <StepsBlock
              v-if="turn.steps.length > 0 && turn.key === runningKey"
              :turn="turn"
              :open="stepsOpen(turn)"
              :summary="stepsSummary(turn)"
              @toggle="toggleSteps(turn)"
            />

            <div v-for="message in turn.messages" :key="message.id" class="msg agent">
              <!-- 文件名点了直接打开。用事件委托而不是给每个 code 绑监听：
                   这段 HTML 是 v-html 塞进来的，Vue 的事件绑定管不到它。 -->
              <div class="prose" @click="onProseClick" v-html="renderMarkdown(message.text)" />
              <span v-if="message.streaming" class="caret">▌</span>
            </div>
            <!-- 一轮一行，不是每截一行：一轮里模型会被采样好几次，回答散成几截，
                 每截都挂一个复制按钮只会满屏按钮，而用户要的是整段回答。 -->
            <div v-if="answerDone(turn)" class="msg-meta agent-meta">
              <time v-if="answerTime(turn.messages)" :title="fullMessageTime(answerTime(turn.messages))">
                {{ formatMessageTime(answerTime(turn.messages)) }}
              </time>
              <button
                class="meta-copy"
                :title="copiedKey === `a-${turn.key}` ? '已复制' : '复制这一轮的回答（Markdown 原文）'"
                @click="copyText(`a-${turn.key}`, answerText(turn.messages))"
              >
                {{ copyLabel(`a-${turn.key}`) }}
              </button>
            </div>

            <StepsBlock
              v-if="turn.steps.length > 0 && turn.key !== runningKey"
              :turn="turn"
              :open="stepsOpen(turn)"
              :summary="stepsSummary(turn)"
              @toggle="toggleSteps(turn)"
            />
          </template>
        </div>

        <footer class="composer-wrap">
          <!-- 一个圆角容器裹住输入框与工具条，工具条在里面而不是外面：
               视觉上它们是一件东西，用户的注意力不用在两个框之间跳。 -->
          <!-- 透明背板：点菜单以外的任何地方都关掉它。没有它的话菜单开了
               就只能再点一次原按钮才关得掉，用户会以为界面卡住了。 -->
          <div
            v-if="modelOpen || policyOpen || statusOpen || workspaceOpen"
            class="backdrop"
            @click="modelOpen = policyOpen = statusOpen = workspaceOpen = false"
          />

          <div
            class="composer"
            :class="{ busy: store.busy, dragging }"
            @dragover.prevent="dragging = true"
            @dragleave="dragging = false"
            @drop="onDrop"
          >
            <!-- 附件排在输入框上面：它们是这条消息的一部分，
                 放在下面会被工具条挤成「设置」的样子。 -->
            <div v-if="attachments.length > 0" class="chips">
              <div v-for="(item, index) in attachments" :key="index" class="attach">
                <img v-if="item.kind === 'image'" :src="item.preview" alt="" />
                <span v-else-if="item.kind === 'audio'" class="attach-icon">🎙</span>
                <span class="attach-name">{{ attachmentLabel(item) }}</span>
                <button class="attach-x" title="移除" @click="removeAttachment(index)">×</button>
              </div>
            </div>
            <p v-if="attachError" class="attach-error">{{ attachError }}</p>

            <textarea
              v-model="draft"
              rows="2"
              @paste="onPaste"
              :placeholder="
                store.busy
                  ? '这一轮还在跑，现在发的会插进这一轮'
                  : '随心输入…… Enter 发送，Shift+Enter 换行'
              "
              @keydown="onKeydown"
            />
            <input
              ref="picker"
              type="file"
              multiple
              hidden
              @change="accept(($event.target as HTMLInputElement).files); (($event.target as HTMLInputElement).value = '')"
            />

            <div class="bar">
              <div class="bar-left">
                <button class="chip" title="贴图片或文本文件（也可以直接粘贴/拖进来）" @click="picker?.click()">
                  <span class="chip-icon">+</span>
                  附件
                </button>

                <!-- 工作区按会话设。摆在这里而不是配置页：它是「这次要在哪儿
                     干活」，属于会话，而且随时会改。 -->
                <button class="chip" :title="workspaceTitle" @click="workspaceOpen = !workspaceOpen">
                  <span class="chip-icon">▣</span>
                  {{ workspaceLabel }}
                </button>
                <div v-if="workspaceOpen" class="menu" @click.stop>
                  <div class="menu-head"><span>会话工作区</span></div>
                  <div class="menu-note pad">
                    {{
                      store.sessionInfo?.workspace
                        ? store.sessionInfo.workspace
                        : "没有设置。相对路径按主目录解析，任何写入都会先问你一次。"
                    }}
                  </div>
                  <button class="menu-item" @click="chooseWorkspace()">选择目录…</button>
                  <button
                    v-if="store.sessionInfo?.workspace"
                    class="menu-item"
                    @click="clearWorkspace()"
                  >
                    清除（回到未设置）
                  </button>
                </div>
                <!-- 工具数是「这次能用什么」最直接的一个数；有挂载失败时标红。 -->
                <button
                  class="chip"
                  :class="{ bad: failedMounts.length > 0 }"
                  @click="statusOpen = !statusOpen"
                >
                  <span class="chip-icon">{{ failedMounts.length > 0 ? "!" : "·" }}</span>
                  工具 {{ toolCount }}
                </button>
                <div v-if="statusOpen" class="menu status-menu" @click.stop>
                  <div class="menu-head"><span>这个会话挂上了什么</span></div>
                  <div class="status-row">
                    <span class="menu-name">内置工具与插件</span>
                    <span class="menu-note">{{ (store.sessionInfo?.tools ?? []).join("、") || "无" }}</span>
                  </div>
                  <!-- 代码模式：模型面前只有 exec 一个工具，但下面这些在脚本里
                       都能 tools.xxx() 调到。不列出来用户会以为 MCP 没挂上。 -->
                  <div v-if="folded.length > 0" class="status-row">
                    <span class="menu-name">收进 exec 的工具（{{ folded.length }}）</span>
                    <span class="menu-note">{{ folded.join("、") }}</span>
                  </div>
                  <div v-if="(store.sessionInfo?.skills ?? []).length > 0" class="status-row">
                    <span class="menu-name">技能</span>
                    <span class="menu-note">{{ (store.sessionInfo?.skills ?? []).join("、") }}</span>
                  </div>
                  <div v-for="[name, status] in mounts" :key="name" class="status-row">
                    <span class="menu-name" :class="{ bad: status.includes('失败') }">{{ name }}</span>
                    <span class="menu-note">{{ status }}</span>
                  </div>
                  <p v-if="mounts.length === 0" class="menu-note pad">
                    没有挂载任何 MCP server。到「MCP」页添加。
                  </p>
                </div>

                <button class="chip" :data-policy="store.config?.profile" @click="policyOpen = !policyOpen">
                  <span class="chip-icon">!</span>
                  {{ policy?.label ?? "审批" }}
                </button>
                <div v-if="policyOpen" class="menu" @click.stop>
                  <button
                    v-for="profile in store.profiles"
                    :key="profile.id"
                    class="menu-item"
                    :class="{ picked: profile.id === store.config?.profile }"
                    @click="pickPolicy(profile.id)"
                  >
                    <span class="menu-name">{{ profile.label }}</span>
                    <span class="menu-note">{{ profile.description }}</span>
                  </button>
                </div>
              </div>

              <div class="bar-right">
                <div class="model">
                  <button class="model-button" @click="openModelMenu()">
                    {{ store.model || "选择模型" }}
                    <span class="caret-down">⌄</span>
                  </button>
                  <div v-if="modelOpen" class="menu model-menu" @click.stop>
                    <div class="menu-head">
                      <span>模型<em v-if="modelSearch">{{ choices.length }} / {{ allChoices.length }}</em></span>
                      <button class="link" @click="actions.loadProviders()">刷新</button>
                    </div>
                    <input
                      ref="searchBox"
                      v-model="modelSearch"
                      class="menu-search"
                      placeholder="搜索模型或服务名"
                      @keydown.enter="choices[0] && pickModel(choices[0])"
                      @keydown.esc="modelOpen = false"
                    />
                    <p v-if="store.providersLoading" class="menu-note pad">正在读取模型服务…</p>
                    <p v-else-if="store.providersError" class="menu-note pad warn">
                      {{ store.providersError }}
                    </p>
                    <p v-else-if="allChoices.length === 0" class="menu-note pad">
                      还没有能用的模型。到「模型服务」页添加端点、填 Key、写上模型名。
                    </p>
                    <p v-else-if="choices.length === 0" class="menu-note pad">
                      没有匹配「{{ modelSearch }}」的模型。
                    </p>
                    <button
                      v-for="choice in choices"
                      :key="`${choice.providerId}/${choice.model}`"
                      class="menu-item"
                      :class="{ picked: choice.providerId === store.providerId && choice.model === store.model }"
                      @click="pickModel(choice)"
                    >
                      <span class="menu-name">{{ choice.model }}</span>
                      <span class="menu-note">
                        {{ choice.providerName }}<template v-if="choice.context"> · {{ formatWindow(choice.context) }}</template>
                      </span>
                    </button>
                  </div>
                </div>

                <button v-if="store.busy" class="stop" title="停止" @click="actions.interrupt()">
                  ■
                </button>
                <button
                  v-else
                  class="send"
                  :disabled="!draft.trim()"
                  title="发送"
                  @click="submit()"
                >
                  ↑
                </button>
              </div>
            </div>
          </div>

</footer>
      </template>
    </template>
  </div>
</template>

<style scoped>
.chat {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;

  /*
   * 正文栏宽。窗口大就跟着变宽，但有上限。
   *
   * 原来写死 74ch：小窗口挤，大窗口两边空出一大片，把窗口拉到 1600px 宽
   * 也还是那么一条。现在窗口小就占满、窗口大到一定程度就打住——行太长，
   * 眼睛回到下一行开头会找不着位置，那正是「拉大窗口反而更难读」的成因。
   *
   * **用 % 不用 vw**：左边侧边栏占掉两百多像素，按视口算会多算出那一截，
   * 结果内容横向溢出。% 算的是容器宽度，正好是能用的那部分。
   *
   * 减掉的 64px 是小窗口下两边的留白。不减的话窗口一小内容就贴着边，
   * 只剩下气泡自己那 28px 内边距——读起来发憋。窗口大到用上限时这一项
   * 不起作用，那时候两边本来就有富余。
   *
   * 三处（消息、执行步骤、输入框）共用这一个变量，不然它们会各宽各的，
   * 窗口一拉大就看着没对齐。
   */
  --content-width: min(112ch, 100% - 64px);
}

.gate {
  margin: auto;
  max-width: 44ch;
  text-align: center;
  line-height: 1.7;
}

.gate h2 {
  margin: 0 0 8px;
  font-size: 15px;
}

.gate p {
  margin: 0 0 16px;
  color: var(--muted);
  font-size: 13px;
}

.stream {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 24px 0 8px;
}

.blank {
  margin: 48px auto;
  max-width: 60ch;
  color: var(--muted);
  font-size: 13px;
  text-align: center;
}

/* 用户消息靠右、带底色的气泡；助手消息占满宽度、无底色。
   这个不对称是刻意的：用户说的话短而少，气泡把它们标出来；助手的回答长，
   套在气泡里会挤成一条窄柱子，读长文最费劲的就是行宽不够。 */
.msg {
  max-width: var(--content-width);
  margin: 0 auto 20px;
  padding: 0 28px;
}

.msg.user {
  display: flex;
  justify-content: flex-end;
}

.bubble {
  max-width: 82%;
  padding: 9px 14px;
  border-radius: 16px 16px 4px 16px;
  background: var(--active);
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
}

.msg.agent {
  line-height: 1.75;
}

/* 消息下面那一行：时间与复制。平时淡，悬停这一轮时才清楚——一屏几十条消息，
   每条下面都是一行显眼的按钮会盖过正文。 */
.msg-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  max-width: var(--content-width);
  margin: -14px auto 18px;
  padding: 0 28px;
  color: var(--muted);
  font-size: 11px;
  opacity: 0.55;
  transition: opacity 0.12s;
}

.msg-meta:hover,
.msg:hover + .msg-meta {
  opacity: 1;
}

.user-meta {
  justify-content: flex-end;
}

.meta-copy {
  padding: 1px 6px;
  border: 1px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font-size: 11px;
  cursor: pointer;
}

.meta-copy:hover {
  border-color: var(--rule-strong);
  color: var(--ink);
}

.caret {
  color: var(--accent);
  margin-left: 1px;
}

/* ---------- 助手正文的 Markdown ---------- */

.prose {
  word-break: break-word;
}

.prose :deep(p) {
  margin: 0 0 0.85em;
}

.prose :deep(p:last-child) {
  margin-bottom: 0;
}

.prose :deep(h3),
.prose :deep(h4),
.prose :deep(h5),
.prose :deep(h6) {
  margin: 1.4em 0 0.5em;
  font-size: 1em;
  font-weight: 650;
}

.prose :deep(h3):first-child,
.prose :deep(h4):first-child {
  margin-top: 0;
}

.prose :deep(ul),
.prose :deep(ol) {
  margin: 0 0 0.85em;
  padding-left: 1.4em;
}

.prose :deep(li) {
  margin: 0.25em 0;
}

.prose :deep(li::marker) {
  color: var(--muted);
}

.prose :deep(code) {
  padding: 0.12em 0.4em;
  border-radius: 4px;
  background: var(--surface-2);
  font-family: var(--mono);
  font-size: 0.88em;
}

.prose :deep(pre) {
  margin: 0 0 0.85em;
  padding: 11px 13px;
  /* 代码块自己横向滚动，别让整页跟着横向滚。 */
  overflow-x: auto;
  border: 1px solid var(--rule);
  border-radius: 10px;
  background: var(--surface-2);
  line-height: 1.55;
}

.prose :deep(pre code) {
  padding: 0;
  background: none;
  font-size: 12px;
}

.prose :deep(blockquote) {
  margin: 0 0 0.85em;
  padding-left: 0.9em;
  border-left: 2px solid var(--rule);
  color: var(--muted);
}

.prose :deep(a) {
  color: var(--accent);
  text-decoration-color: color-mix(in srgb, var(--accent) 40%, transparent);
  text-underline-offset: 2px;
}

/* 表格。列多的表（股东表这种）自己横向滚，不能让整页跟着横滚。 */
.prose :deep(.table-wrap) {
  margin: 10px 0;
  overflow-x: auto;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
}

.prose :deep(table) {
  border-collapse: collapse;
  width: 100%;
  font-size: 12.5px;
  /* 数字列对齐：表格里大多是比例和金额，不等宽会看得很累。 */
  font-variant-numeric: tabular-nums;
}

.prose :deep(th),
.prose :deep(td) {
  padding: 6px 10px;
  text-align: left;
  border-bottom: 1px solid var(--rule);
  white-space: nowrap;
}

.prose :deep(thead th) {
  background: var(--surface-2);
  font-weight: 600;
  color: var(--ink-2);
  position: sticky;
  top: 0;
}

.prose :deep(tbody tr:last-child td) {
  border-bottom: none;
}

.prose :deep(tbody tr:hover) {
  background: var(--hover);
}

.prose :deep(hr) {
  margin: 14px 0;
  border: none;
  border-top: 1px solid var(--rule);
}

.prose :deep(del) {
  color: var(--muted);
}

/* 任务列表：去掉列表符号，复选框顶到左边。 */
.prose :deep(li.task-item) {
  list-style: none;
  margin-left: -1.2em;
}

.prose :deep(li.task-item input) {
  margin: 0 6px 0 0;
  vertical-align: baseline;
}

/* 图片按缩略图显示，点开看原图。 */
.prose :deep(.image-thumb) {
  display: inline-block;
  max-width: 100%;
  margin: 6px 0;
  line-height: 0;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  overflow: hidden;
}

.prose :deep(.image-thumb img),
.prose :deep(img) {
  display: block;
  max-width: 100%;
  /* 限高而不是限宽：对话气泡里塞一张原尺寸的图会把整屏顶掉，
     而这里的图多半只是「看一眼是不是这个」。 */
  max-height: 220px;
  width: auto;
  object-fit: contain;
}

/* 模型写的展示型 HTML 也得有个样子。 */
.prose :deep(details) {
  margin: 8px 0;
  padding: 8px 12px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface-2);
}

.prose :deep(summary) {
  cursor: pointer;
  font-weight: 500;
}

.prose :deep(mark) {
  background: var(--accent-soft);
  color: inherit;
  padding: 0 2px;
  border-radius: 2px;
}

.prose :deep(kbd) {
  padding: 1px 5px;
  border: 1px solid var(--rule-strong);
  border-bottom-width: 2px;
  border-radius: 4px;
  font-family: var(--mono);
  font-size: 11px;
}

/* 脚注区：正文之后的小字，别跟正文抢注意力。 */
.prose :deep(.footnotes) {
  margin-top: 10px;
  font-size: 12px;
  color: var(--muted);
}

.prose :deep(.footnotes-sep) {
  margin: 12px 0 8px;
}

.prose :deep(.footnote-ref a) {
  text-decoration: none;
}

.prose :deep(strong) {
  font-weight: 650;
}

/* ---------- 输入区 ---------- */

/* 贴进来的图片按缩略图显示：气泡里铺一张原图会把对话挤没。 */
.shots {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 6px;
}

.shots img {
  max-width: 180px;
  max-height: 140px;
  border-radius: var(--r-sm);
  object-fit: cover;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 8px 10px 0;
}

.attach {
  display: flex;
  align-items: center;
  gap: 6px;
  max-width: 220px;
  padding: 3px 6px 3px 3px;
  border: 1px solid var(--rule);
  border-radius: var(--r-full);
  background: var(--surface-2);
}

.attach img {
  width: 22px;
  height: 22px;
  border-radius: var(--r-full);
  object-fit: cover;
}

.attach-icon {
  font-size: 14px;
}

.attach-name {
  min-width: 0;
  font-size: 11.5px;
  color: var(--ink-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.attach-x {
  flex: 0 0 auto;
  border: none;
  background: none;
  color: var(--muted);
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
}

.attach-error {
  margin: 6px 10px 0;
  color: var(--danger);
  font-size: 11.5px;
  line-height: 1.5;
}

/* 拖到上面时给一条明显的边：没有反馈的话用户不知道该松手了没有。 */
.composer.dragging {
  border-color: var(--accent, var(--rule-strong));
  background: var(--surface-2);
}

/* 文件名：看着就该点。下划线用虚线，与普通链接区分开——
   点它打开的是本机文件，不是网页。 */
.prose :deep(.file-ref) {
  cursor: pointer;
  text-decoration: underline;
  text-decoration-style: dotted;
  text-underline-offset: 2px;
}

.prose :deep(.file-ref:hover) {
  background: var(--active);
  text-decoration-style: solid;
}

.composer-wrap {
  position: relative;
  padding: 8px 24px 12px;
}

.backdrop {
  position: fixed;
  inset: 0;
  z-index: 10;
}

.composer {
  max-width: var(--content-width);
  margin: 0 auto;
  border: 1px solid var(--rule-strong);
  border-radius: var(--r-lg);
  background: var(--surface);
  box-shadow: var(--shadow-2);
  transition: border-color 0.15s, box-shadow 0.15s;
}

.composer:focus-within {
  border-color: var(--accent);
  box-shadow: var(--shadow-2), 0 0 0 3px var(--accent-soft);
}

/* 输入框本身不要全局表单样式的边框与内阴影——它已经在容器里了。 */
.composer textarea:focus {
  border: none;
  box-shadow: none;
}

.composer textarea {
  display: block;
  width: 100%;
  border: none;
  background: none;
  resize: none;
  padding: 12px 14px 4px;
  font: inherit;
  line-height: 1.6;
  color: inherit;
  outline: none;
}

.bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 8px 8px 12px;
}

.bar-left,
.bar-right {
  position: relative;
  display: flex;
  align-items: center;
  gap: 8px;
}

.chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 9px 3px 6px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: none;
  font-size: 12px;
  color: var(--muted);
  cursor: pointer;
}

.chip:hover {
  border-color: var(--rule);
}

.chip-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border: 1.5px solid currentcolor;
  border-radius: 50%;
  font-size: 9px;
  font-weight: 700;
}

/* 越危险的档位越显眼：无人值守不弹任何确认，用户必须一眼看见自己开着它。 */
.chip[data-policy="on-write"] {
  color: var(--muted);
}

.chip[data-policy="always"] {
  color: var(--accent);
}

.chip[data-policy="never"],
.chip[data-policy="bypass"] {
  color: var(--danger);
}

.chip.bad {
  color: var(--danger);
}

.status-menu {
  left: 0;
  min-width: 340px;
  max-width: 460px;
}

.status-row {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 6px 10px;
}

.status-row + .status-row {
  border-top: 1px solid var(--rule);
}

.menu-name.bad {
  color: var(--danger);
}

.model-button {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 3px 8px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: none;
  font-size: 12px;
  color: var(--muted);
  cursor: pointer;
  max-width: 24ch;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-button:hover {
  border-color: var(--rule);
  color: inherit;
}

.caret-down {
  font-size: 11px;
  line-height: 1;
}

.send,
.stop {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 50%;
  background: var(--accent);
  color: var(--on-accent);
  font-size: 14px;
  cursor: pointer;
  transition: background-color 0.12s, opacity 0.12s;
}

.send:hover:not(:disabled) {
  background: var(--accent-hover);
}

.send:disabled {
  opacity: 0.25;
  cursor: not-allowed;
}

.stop {
  background: var(--danger);
  font-size: 10px;
}

.menu {
  position: absolute;
  bottom: 34px;
  z-index: 20;
  display: flex;
  flex-direction: column;
  min-width: 260px;
  max-height: 320px;
  overflow-y: auto;
  padding: 4px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface);
  box-shadow: var(--shadow-3);
}

.bar-left .menu {
  left: 0;
}

.model-menu {
  right: 0;
  min-width: 300px;
}

/* 搜索框不跟着列表滚：菜单里一滚就找不到输入框在哪儿了。 */
.menu-search {
  position: sticky;
  top: 0;
  z-index: 1;
  width: calc(100% - 12px);
  margin: 0 6px 4px;
  padding: 5px 8px;
  border: 1px solid var(--rule);
  border-radius: var(--r-sm);
  background: var(--surface);
  color: inherit;
  font-size: 12.5px;
}

.menu-head em {
  font-style: normal;
  margin-left: 6px;
}

.menu-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px 4px;
  color: var(--muted);
  font-size: 11px;
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
  gap: 2px;
  padding: 7px 10px;
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
  font-size: 13px;
}

.menu-note {
  color: var(--muted);
  font-size: 11px;
  line-height: 1.5;
}

.menu-note.pad {
  padding: 8px 10px;
}

.menu-note.warn {
  color: var(--danger);
}

</style>
