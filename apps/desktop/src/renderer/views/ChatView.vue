<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import { actions, filterChoices, modelChoices, store } from "../store";
import { describeError } from "../errors";
import { t } from "../i18n";
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
import { formatDuration as formatVoiceTime, MAX_SECONDS, VoiceRecorder } from "../voice";
import {
  activeReferences,
  applyMention,
  mentionCandidates,
  mentionQuery,
  mentionTitle,
  type MentionBinding,
} from "../mentions";
import type { SessionSummaryView } from "../../shared/types";
import StepsBlock from "./StepsBlock.vue";
import QuestionCard from "./QuestionCard.vue";

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

// ---------- 语音输入 ----------
//
// 点话筒开始录，再点一下停下、转成文字放进输入框（不直接发：听写会听错）。
// Esc 或点 × 丢掉这段。听写走「配置 → 多模态」里的听写模型。

const input = ref<HTMLTextAreaElement | null>(null);
const voiceState = ref<"idle" | "recording" | "transcribing">("idle");
const voiceError = ref("");
const voiceElapsed = ref(0);
const voiceLevel = ref(0);
let recorder: VoiceRecorder | null = null;
let voiceTimer: ReturnType<typeof setInterval> | undefined;

const sttReady = computed(() => Boolean(store.config?.roles?.stt?.providerId && store.config?.roles?.stt?.model));

const voiceTitle = computed(() => {
  if (voiceState.value === "recording") return t("再点一下结束录音，转成文字");
  if (voiceState.value === "transcribing") return t("正在听写…");
  return sttReady.value ? t("语音输入：点一下开始说话") : t("语音输入：先在「配置 → 多模态」里给「听写」选一个模型");
});

async function toggleVoice(): Promise<void> {
  voiceError.value = "";
  if (voiceState.value === "recording") return finishVoice();
  if (voiceState.value !== "idle") return;
  if (!sttReady.value) {
    voiceError.value = t("还没有配听写模型：到「配置 → 多模态」里给「听写」选一个模型。");
    return;
  }
  const permission = (await window.aiclaw.voice.permission()) as string;
  if (permission !== "granted") {
    voiceError.value = t("没有麦克风权限：到「系统设置 → 隐私与安全性 → 麦克风」里打开 AIClaw，然后重新打开应用。");
    return;
  }
  recorder = new VoiceRecorder();
  try {
    await recorder.start();
  } catch (error) {
    recorder = null;
    voiceError.value = t("打不开麦克风：{error}", { error: describeError(error) });
    return;
  }
  voiceState.value = "recording";
  voiceElapsed.value = 0;
  voiceTimer = setInterval(() => {
    if (!recorder) return;
    voiceElapsed.value = recorder.elapsed();
    voiceLevel.value = recorder.level();
    if (voiceElapsed.value >= MAX_SECONDS) void finishVoice();
  }, 100);
}

async function finishVoice(): Promise<void> {
  clearInterval(voiceTimer);
  const current = recorder;
  recorder = null;
  voiceLevel.value = 0;
  if (!current) {
    voiceState.value = "idle";
    return;
  }
  voiceState.value = "transcribing";
  try {
    const recorded = await current.stop();
    if (!recorded) {
      voiceError.value = t("没有听到声音。检查一下麦克风，靠近一点再说一次。");
      return;
    }
    const text = ((await window.aiclaw.voice.transcribe(recorded.wav)) as string).trim();
    if (!text) {
      voiceError.value = t("没听出内容，再说一次试试。");
      return;
    }
    insertAtCursor(text);
  } catch (error) {
    voiceError.value = describeError(error);
  } finally {
    voiceState.value = "idle";
  }
}

function cancelVoice(): void {
  clearInterval(voiceTimer);
  recorder?.cancel();
  recorder = null;
  voiceLevel.value = 0;
  voiceState.value = "idle";
}

// 录音时 Esc 丢掉这段，焦点在不在输入框里都算。
function onVoiceKey(event: KeyboardEvent): void {
  if (event.key === "Escape" && voiceState.value === "recording") {
    event.preventDefault();
    cancelVoice();
  }
}
watch(voiceState, (state) => {
  if (state === "recording") window.addEventListener("keydown", onVoiceKey);
  else window.removeEventListener("keydown", onVoiceKey);
});
// 切走（换会话、去设置页）时别让麦克风一直开着。
onUnmounted(() => {
  window.removeEventListener("keydown", onVoiceKey);
  cancelVoice();
});

/** 把听写出来的字插到光标处；输入框里原来的字不动。 */
function insertAtCursor(text: string): void {
  const element = input.value;
  const value = draft.value;
  const start = element?.selectionStart ?? value.length;
  const end = element?.selectionEnd ?? value.length;
  const before = value.slice(0, start);
  const after = value.slice(end);
  const glue = before && !/\s$/.test(before) && /^[A-Za-z0-9]/.test(text) ? " " : "";
  draft.value = before + glue + text + after;
  void nextTick(() => {
    if (!element) return;
    const caret = before.length + glue.length + text.length;
    element.focus();
    element.setSelectionRange(caret, caret);
  });
}

// ---------- @ 引用会话 ----------
//
// 打 @ 弹出会话列表（标题命中的在前，然后是正文搜索命中的），↑↓ 选、Enter / Tab 确定、
// Esc 关掉。选中插入「@标题 」并记下绑定；发送时只交还留在正文里的那几个。

const bindings = ref<MentionBinding[]>([]);
const mention = ref<{ start: number; query: string } | null>(null);
const mentionIndex = ref(0);
const mentionHits = ref<SessionSummaryView[]>([]);
let mentionSearch: ReturnType<typeof setTimeout> | undefined;

const mentionList = computed(() =>
  mention.value ? mentionCandidates(mention.value.query, store.sessions, mentionHits.value, store.sessionId) : [],
);

/** 光标动了（打字、点击、方向键）就重新看一次是不是在打 @。 */
function updateMention(): void {
  const element = input.value;
  const found = element ? mentionQuery(draft.value, element.selectionStart ?? draft.value.length) : null;
  const changed = found?.query !== mention.value?.query || found?.start !== mention.value?.start;
  mention.value = found;
  if (!changed) return;
  mentionIndex.value = 0;
  clearTimeout(mentionSearch);
  mentionHits.value = [];
  const query = found?.query.trim() ?? "";
  if (!query) return;
  // 正文搜索打一次内核的 LIKE：防抖，打字期间只有最后一次有用。
  mentionSearch = setTimeout(async () => {
    try {
      const hits = (await window.aiclaw.session.search(query)) as SessionSummaryView[];
      if (mention.value?.query.trim() === query) mentionHits.value = hits;
    } catch {
      // 搜不到就只按标题匹配
    }
  }, 150);
}

function pickMention(session: SessionSummaryView): void {
  const element = input.value;
  const current = mention.value;
  if (!element || !current) return;
  const title = mentionTitle(session.title);
  const next = applyMention(draft.value, current.start, element.selectionStart ?? draft.value.length, title);
  draft.value = next.text;
  bindings.value = [...bindings.value, { id: session.id, title }];
  mention.value = null;
  void nextTick(() => {
    element.focus();
    element.setSelectionRange(next.caret, next.caret);
  });
}

/** 「@ 引用会话」按钮：在光标处插一个 @，弹出列表。 */
function startMention(): void {
  const element = input.value;
  if (!element) return;
  const caret = element.selectionStart ?? draft.value.length;
  const before = draft.value.slice(0, caret);
  const glue = before && !/\s$/.test(before) ? " " : "";
  draft.value = `${before}${glue}@${draft.value.slice(caret)}`;
  void nextTick(() => {
    element.focus();
    const position = caret + glue.length + 1;
    element.setSelectionRange(position, position);
    updateMention();
  });
}

/** 输入框的键盘：列表开着时 ↑↓ / Enter / Tab / Esc 归它，不然照常。 */
function onMentionKey(event: KeyboardEvent): boolean {
  if (!mention.value || mentionList.value.length === 0 || event.isComposing) return false;
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    const count = mentionList.value.length;
    mentionIndex.value = (mentionIndex.value + (event.key === "ArrowDown" ? 1 : count - 1)) % count;
    return true;
  }
  if (event.key === "Enter" || event.key === "Tab") {
    event.preventDefault();
    pickMention(mentionList.value[mentionIndex.value]!);
    return true;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    mention.value = null;
    return true;
  }
  return false;
}

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
      attachError.value = t("{name} 读不了：{error}", { name: file.name, error: describeError(error) });
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
  if (!path) return t("未设工作区");
  const parts = path.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? path;
});

const workspaceTitle = computed(() =>
  store.sessionInfo?.workspace
    ? t("会话工作区：{path}", { path: store.sessionInfo.workspace })
    : t("这个会话没有设置工作区，点一下可以指定"),
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
  if (item.kind === "audio") return t("{name}（{size} MB）", { name: item.name, size: (item.size / 1024 / 1024).toFixed(1) });
  return item.truncated ? t("{name}（已截断）", { name: item.name }) : item.name;
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
  const references = activeReferences(text, bindings.value);
  draft.value = "";
  attachments.value = [];
  attachError.value = "";
  bindings.value = [];
  mention.value = null;
  await actions.send(text, images, audio, references);
  await scrollToEnd();
}

// Enter 发送、Shift+Enter 换行。中文输入法组字期间的 Enter 不能当发送，
// 否则选词就把半句话发出去了。
function onKeydown(event: KeyboardEvent): void {
  if (onMentionKey(event)) return;
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
  return tokens >= 1000 ? t("{n}K 上下文", { n: Math.round(tokens / 1000) }) : t("{n} 上下文", { n: tokens });
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
/** 当前会话里等着回答的问题。别的会话的问题在切过去时出现。 */
const currentQuestions = computed(() => store.questions.filter((question) => question.sessionId === store.sessionId));

const copiedKey = ref("");
let copiedTimer: ReturnType<typeof setTimeout> | undefined;

/** 复制失败的那一条，按钮上显示「复制失败」。说不出失败的「已复制」比没有按钮更糟。 */
const failedKey = ref("");

async function copyText(key: string, text: string, html?: string): Promise<void> {
  if (!text) return;
  let ok = false;
  try {
    // 主进程写系统剪贴板，不受窗口焦点影响；渲染层的剪贴板 API 只作后备。
    ok = (await window.aiclaw.clipboard.write(text, html)) === true;
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

/** 刚复制完（或失败）的那一行保持显示一会儿：鼠标一移开就消失，用户看不到结果。 */
function isMarked(key: string): boolean {
  return copiedKey.value === key || failedKey.value === key;
}

function copyLabel(key: string): string {
  if (copiedKey.value === key) return t("已复制");
  if (failedKey.value === key) return t("复制失败");
  return t("复制");
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
  if (turn.key === runningKey.value) return t("执行中 · 已 {n} 步", { n: turn.steps.length });
  const elapsed = stepsElapsed(turn.steps);
  const n = turn.steps.length;
  return elapsed ? t("{n} 个执行步骤 · 用时 {time}", { n, time: formatDuration(elapsed) }) : t("{n} 个执行步骤", { n });
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
  return entries.sort((a, b) => Number(mountFailed(b[1])) - Number(mountFailed(a[1])));
});
/**
 * 这一行是不是挂载失败。状态文字由内核按界面语言给出：中文带「失败」，英文带 "failed"
 * （"Failed to mount: …"）。两种都认——切换语言之后，已经开着的会话里还是旧语言的文字。
 */
function mountFailed(status: string): boolean {
  return status.includes("失败") || /\bfailed\b/i.test(status);
}
const failedMounts = computed(() =>
  mounts.value.filter(([, status]) => mountFailed(status)),
);
/** 代码模式下收进 exec 的那些也算：它们在脚本里照样能调。 */
const folded = computed(() => store.sessionInfo?.foldedTools ?? []);
const toolCount = computed(() => (store.sessionInfo?.tools.length ?? 0) + folded.value.length);
</script>

<template>
  <div class="chat">
    <div v-if="!configured" class="gate">
      <h2>{{ t("先完成配置") }}</h2>
      <p>{{ t("到「模型服务」页添加一个端点、填上 Key、写上模型名，再回来选一个默认模型。") }}</p>
    </div>

    <template v-else>
      <!-- 应用启动时会自己把运行时拉起来，所以「启动中」是打开应用后最常见的
           一屏，不能再显示成「未启动」配一个按钮——那会让人以为要自己点。 -->
      <div v-if="store.runtime.state === 'starting'" class="gate">
        <h2>{{ t("正在启动本地运行时…") }}</h2>
        <p>
          {{ t("在本机拉起 Agent 执行内核。") }}
        </p>
      </div>

      <div v-else-if="store.runtime.state !== 'ready'" class="gate">
        <h2>{{ store.runtime.state === "failed" ? t("本地运行时启动失败") : t("本地运行时未启动") }}</h2>
        <p>
          {{ t("启动后会在本机拉起 Agent 执行内核。命令会直接在这台电脑上执行，没有沙箱。") }}
        </p>
        <button class="primary" @click="actions.startRuntime()">
          {{ store.runtime.state === "failed" ? t("重试") : t("启动并开始对话") }}
        </button>
      </div>

      <template v-else>
        <div ref="scroller" class="stream">
          <!-- 切会话时先切过去、历史随后到：这几秒里要有话说，不能是「还没有内容」，
               那句话在一个有几十条记录的会话上是假的。 -->
          <p v-if="store.loadingSession === store.sessionId && store.timeline.length === 0" class="blank">
            {{ t("正在载入这个会话…") }}
          </p>
          <p v-else-if="store.timeline.length === 0" class="blank">
            {{ t("这个会话还没有内容。说点什么开始。") }}
          </p>

          <!-- 按轮渲染。一轮里步骤只出现一次，位置随状态走：
               跑着的时候在回答上面（那时用户看的就是它，且回答还没出来），
               跑完了挪到回答下面并收起（那时用户要读的是答案，不是过程）。 -->
          <template v-for="turn in turns" :key="turn.key">
            <!-- 用户消息是纯文本气泡：他自己打的字，不该被 Markdown 重新解释
                 （写个 *星号* 不该变成斜体）。助手消息才渲染 Markdown。 -->
            <!-- 提问与它下面那一行包在一起：鼠标在这块上面时才显示时间与复制
                 （与 Codex / Claude 一样），平时整屏只有正文。 -->
            <div v-if="turn.user" class="say">
            <div class="msg user">
              <div class="bubble">
                <div v-if="(turn.user.images ?? []).length > 0" class="shots">
                  <img
                    v-for="(shot, index) in turn.user.images"
                    :key="index"
                    :src="shot"
                    :alt="t(`附带的图片`)"
                  />
                </div>
                {{ turn.user.text }}
              </div>
            </div>
            <!-- 这条消息引用的会话：点一下打开它。 -->
            <div v-if="(turn.user.references ?? []).length > 0" class="refs">
              <button
                v-for="ref in turn.user.references"
                :key="ref.id"
                class="ref"
                :title="t(`打开「{title}」`, { title: ref.title })"
                @click="actions.openSession(ref.id)"
              >
                ↪ {{ ref.title }}
              </button>
            </div>
            <!-- 时间与复制放在气泡外面一行：塞进气泡里会和正文挤在一起，
                 而且用户复制的只是自己打的字，不该带上时间。 -->
            <div class="msg-meta user-meta" :class="{ pinned: isMarked(`u-${turn.key}`) }">
              <button
                class="meta-copy"
                :title="copyLabel(`u-${turn.key}`)"
                :aria-label="copyLabel(`u-${turn.key}`)"
                @click="copyText(`u-${turn.key}`, turn.user.text)"
              >
                <svg v-if="copiedKey === `u-${turn.key}`" viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7" /></svg>
                <svg v-else-if="failedKey === `u-${turn.key}`" viewBox="0 0 16 16" aria-hidden="true"><path d="M4.5 4.5l7 7M11.5 4.5l-7 7" /></svg>
                <svg v-else viewBox="0 0 16 16" aria-hidden="true">
                  <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
                  <path d="M10.5 5.5V3.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2" />
                </svg>
              </button>
              <time v-if="turn.user.at" :title="fullMessageTime(turn.user.at)">
                {{ formatMessageTime(turn.user.at) }}
              </time>
            </div>
            </div>

            <StepsBlock
              v-if="turn.steps.length > 0 && turn.key === runningKey"
              :turn="turn"
              :open="stepsOpen(turn)"
              live
              :summary="stepsSummary(turn)"
              @toggle="toggleSteps(turn)"
            />

            <!-- 一轮的回答与它下面那一行包在一起，理由同提问。 -->
            <div v-if="turn.messages.length > 0" class="say">
            <div v-for="message in turn.messages" :key="message.id" class="msg agent">
              <!-- 文件名点了直接打开。用事件委托而不是给每个 code 绑监听：
                   这段 HTML 是 v-html 塞进来的，Vue 的事件绑定管不到它。 -->
              <div class="prose" @click="onProseClick" v-html="renderMarkdown(message.text)" />
              <span v-if="message.streaming" class="caret">▌</span>
            </div>
            <!-- 一轮一行，不是每截一行：一轮里模型会被采样好几次，回答散成几截，
                 每截都挂一个复制按钮只会满屏按钮，而用户要的是整段回答。 -->
            <div v-if="answerDone(turn)" class="msg-meta agent-meta" :class="{ pinned: isMarked(`a-${turn.key}`) }">
              <button
                class="meta-copy"
                :title="copiedKey === `a-${turn.key}` || failedKey === `a-${turn.key}` ? copyLabel(`a-${turn.key}`) : t(`复制回答（Markdown 原文）`)"
                :aria-label="copyLabel(`a-${turn.key}`)"
                @click="copyText(`a-${turn.key}`, answerText(turn.messages), renderMarkdown(answerText(turn.messages)))"
              >
                <svg v-if="copiedKey === `a-${turn.key}`" viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7" /></svg>
                <svg v-else-if="failedKey === `a-${turn.key}`" viewBox="0 0 16 16" aria-hidden="true"><path d="M4.5 4.5l7 7M11.5 4.5l-7 7" /></svg>
                <svg v-else viewBox="0 0 16 16" aria-hidden="true">
                  <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
                  <path d="M10.5 5.5V3.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2" />
                </svg>
              </button>
              <time v-if="answerTime(turn.messages)" :title="fullMessageTime(answerTime(turn.messages))">
                {{ formatMessageTime(answerTime(turn.messages)) }}
              </time>
            </div>
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

          <!-- 模型在等你回答的问题（ask_user）。在输入框上方而不是弹窗：上面模型做到
               哪一步还看得见；卡片有自己的输入框，不动你正在写的草稿。 -->
          <QuestionCard
            v-for="question in currentQuestions"
            :key="question.id"
            :question="question"
            @answer="(answer) => actions.answerQuestion(question.id, answer)"
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
                <button class="attach-x" :title="t(`移除`)" @click="removeAttachment(index)">×</button>
              </div>
            </div>
            <p v-if="attachError" class="attach-error">{{ attachError }}</p>
            <p v-if="voiceError" class="attach-error">
              {{ voiceError }}
              <button v-if="!sttReady" class="link" @click="actions.setView('settings')">{{ t("去配置") }}</button>
            </p>

            <!-- @ 引用会话的候选列表：浮在输入框上面。 -->
            <div v-if="mention && mentionList.length > 0" class="mention-menu" role="listbox">
              <div class="mention-head">{{ t("引用会话：模型会先读它，再回答你") }}</div>
              <button
                v-for="(session, index) in mentionList"
                :key="session.id"
                class="mention-option"
                :class="{ on: index === mentionIndex }"
                role="option"
                :aria-selected="index === mentionIndex"
                @mousedown.prevent="pickMention(session)"
                @mouseenter="mentionIndex = index"
              >
                <span class="mention-title">{{ session.title || t("未命名会话") }}</span>
                <span class="mention-meta">{{ session.parentId ? t("子 agent · ") : "" }}{{ formatMessageTime(Date.parse(session.updatedAt)) }}</span>
              </button>
            </div>
            <textarea
              ref="input"
              v-model="draft"
              rows="2"
              @input="updateMention"
              @click="updateMention"
              @keyup="(event: KeyboardEvent) => { if (!['ArrowUp', 'ArrowDown', 'Enter', 'Tab', 'Escape'].includes(event.key)) updateMention(); }"
              @blur="mention = null"
              @paste="onPaste"
              :placeholder="
                store.busy
                  ? t(`这一轮还在跑，现在发的会插进这一轮`)
                  : t(`随心输入…… Enter 发送，Shift+Enter 换行`)
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
                <button class="chip" :title="t(`贴图片或文本文件（也可以直接粘贴/拖进来）`)" @click="picker?.click()">
                  <span class="chip-icon">+</span>
                  {{ t("附件") }}
                </button>
                <button class="chip" :title="t(`引用另一个会话：模型会先读它（也可以直接打 @）`)" @mousedown.prevent @click="startMention()">
                  <span class="chip-icon">@</span>
                  {{ t("引用会话") }}
                </button>

                <!-- 工作区按会话设。摆在这里而不是配置页：它是「这次要在哪儿
                     干活」，属于会话，而且随时会改。 -->
                <button class="chip" :title="workspaceTitle" @click="workspaceOpen = !workspaceOpen">
                  <span class="chip-icon">▣</span>
                  {{ workspaceLabel }}
                </button>
                <div v-if="workspaceOpen" class="menu" @click.stop>
                  <div class="menu-head"><span>{{ t("会话工作区") }}</span></div>
                  <div class="menu-note pad">
                    {{
                      store.sessionInfo?.workspace
                        ? store.sessionInfo.workspace
                        : t("没有设置。相对路径按主目录解析，任何写入都会先问你一次。")
                    }}
                  </div>
                  <button class="menu-item" @click="chooseWorkspace()">{{ t("选择目录…") }}</button>
                  <button
                    v-if="store.sessionInfo?.workspace"
                    class="menu-item"
                    @click="clearWorkspace()"
                  >
                    {{ t("清除（回到未设置）") }}
                  </button>
                </div>
                <!-- 工具数是「这次能用什么」最直接的一个数；有挂载失败时标红。 -->
                <button
                  class="chip"
                  :class="{ bad: failedMounts.length > 0 }"
                  @click="statusOpen = !statusOpen"
                >
                  <span class="chip-icon">{{ failedMounts.length > 0 ? "!" : "·" }}</span>
                  {{ t("工具 {n}", { n: toolCount }) }}
                </button>
                <div v-if="statusOpen" class="menu status-menu" @click.stop>
                  <div class="menu-head"><span>{{ t("这个会话挂上了什么") }}</span></div>
                  <div class="status-row">
                    <span class="menu-name">{{ t("内置工具与插件") }}</span>
                    <span class="menu-note">{{ (store.sessionInfo?.tools ?? []).join(t("、")) || t("无") }}</span>
                  </div>
                  <!-- 代码模式：模型面前只有 exec 一个工具，但下面这些在脚本里
                       都能 tools.xxx() 调到。不列出来用户会以为 MCP 没挂上。 -->
                  <div v-if="folded.length > 0" class="status-row">
                    <span class="menu-name">{{ t("收进 exec 的工具（{n}）", { n: folded.length }) }}</span>
                    <span class="menu-note">{{ folded.join(t("、")) }}</span>
                  </div>
                  <div v-if="(store.sessionInfo?.skills ?? []).length > 0" class="status-row">
                    <span class="menu-name">{{ t("技能") }}</span>
                    <span class="menu-note">{{ (store.sessionInfo?.skills ?? []).join(t("、")) }}</span>
                  </div>
                  <div v-for="[name, status] in mounts" :key="name" class="status-row">
                    <span class="menu-name" :class="{ bad: mountFailed(status) }">{{ name }}</span>
                    <span class="menu-note">{{ status }}</span>
                  </div>
                  <p v-if="mounts.length === 0" class="menu-note pad">
                    {{ t("没有挂载任何 MCP server。到「MCP」页添加。") }}
                  </p>
                </div>

                <button class="chip" :data-policy="store.config?.profile" @click="policyOpen = !policyOpen">
                  <span class="chip-icon">!</span>
                  {{ policy?.label ?? t("审批") }}
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
                    {{ store.model || t("选择模型") }}
                    <span class="caret-down">⌄</span>
                  </button>
                  <div v-if="modelOpen" class="menu model-menu" @click.stop>
                    <div class="menu-head">
                      <span>{{ t("模型") }}<em v-if="modelSearch">{{ choices.length }} / {{ allChoices.length }}</em></span>
                      <button class="link" @click="actions.loadProviders()">{{ t("刷新") }}</button>
                    </div>
                    <input
                      ref="searchBox"
                      v-model="modelSearch"
                      class="menu-search"
                      :placeholder="t(`搜索模型或服务名`)"
                      @keydown.enter="choices[0] && pickModel(choices[0])"
                      @keydown.esc="modelOpen = false"
                    />
                    <p v-if="store.providersLoading" class="menu-note pad">{{ t("正在读取模型服务…") }}</p>
                    <p v-else-if="store.providersError" class="menu-note pad warn">
                      {{ store.providersError }}
                    </p>
                    <p v-else-if="allChoices.length === 0" class="menu-note pad">
                      {{ t("还没有能用的模型。到「模型服务」页添加端点、填 Key、写上模型名。") }}
                    </p>
                    <p v-else-if="choices.length === 0" class="menu-note pad">
                      {{ t("没有匹配「{query}」的模型。", { query: modelSearch }) }}
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

                <!-- 语音输入：录音时显示时长与音量，旁边的 × 丢掉这段。 -->
                <span v-if="voiceState === 'recording'" class="voice-live">
                  <span class="voice-dot" :style="{ transform: `scale(${1 + voiceLevel * 0.9})` }"></span>
                  {{ formatVoiceTime(voiceElapsed) }}
                  <button class="voice-cancel" :title="t(`丢掉这段（Esc）`)" @click="cancelVoice()">×</button>
                </span>
                <button
                  class="mic"
                  :class="{ recording: voiceState === 'recording', busy: voiceState === 'transcribing', off: !sttReady }"
                  :disabled="voiceState === 'transcribing'"
                  :title="voiceTitle"
                  :aria-label="voiceTitle"
                  @click="toggleVoice()"
                >
                  <svg v-if="voiceState !== 'transcribing'" viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">
                    <path
                      fill="currentColor"
                      d="M12 14a3 3 0 0 0 3-3V5a3 3 0 1 0-6 0v6a3 3 0 0 0 3 3Zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.92V21h2v-3.08A7 7 0 0 0 19 11h-2Z"
                    />
                  </svg>
                  <span v-else class="voice-spin" aria-hidden="true"></span>
                </button>
                <button v-if="store.busy" class="stop" :title="t(`停止`)" @click="actions.interrupt()">
                  ■
                </button>
                <button
                  v-else
                  class="send"
                  :disabled="!draft.trim()"
                  :title="t(`发送`)"
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

/* 消息下面那一行：复制与时间。**鼠标移到这条消息上才出现**（与 Codex / Claude
   一样）——一屏几十条消息，每条下面常驻一行按钮会盖过正文。用 opacity 而不是
   display:none：位置先占好，出现时正文不会跳。键盘聚焦进来也显示。 */
.msg-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  max-width: var(--content-width);
  margin: -16px auto 14px;
  padding: 0 24px;
  color: var(--muted);
  font-size: 11px;
  opacity: 0;
  transition: opacity 0.12s;
}

.say:hover .msg-meta,
.say:focus-within .msg-meta,
.msg-meta.pinned {
  opacity: 1;
}

/* 提问靠右：复制按钮贴着气泡的右边缘，时间在它左边。 */
.user-meta {
  flex-direction: row-reverse;
}

.meta-copy {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.meta-copy:hover {
  background: var(--active);
  color: var(--ink);
}

.meta-copy svg {
  width: 14px;
  height: 14px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.4;
  stroke-linecap: round;
  stroke-linejoin: round;
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
  position: relative;
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

.mic {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border: 1px solid var(--rule);
  border-radius: 50%;
  background: transparent;
  color: var(--ink-2);
  cursor: pointer;
  transition: background-color 0.12s, color 0.12s;
}

.mic:hover:not(:disabled) {
  background: var(--surface-2, var(--accent-soft));
  color: var(--ink);
}

.mic.off {
  color: var(--muted);
}

.mic.recording {
  border-color: var(--danger);
  background: var(--danger);
  color: #fff;
}

.mic.busy {
  cursor: progress;
}

.voice-spin {
  width: 13px;
  height: 13px;
  border: 2px solid var(--rule);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: voice-spin 0.8s linear infinite;
}

@keyframes voice-spin {
  to {
    transform: rotate(360deg);
  }
}

.voice-live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--danger);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.voice-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--danger);
  transition: transform 0.1s linear;
}

.voice-cancel {
  border: none;
  background: transparent;
  color: var(--muted);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
}

.attach-error .link {
  margin-left: 6px;
  border: none;
  background: none;
  color: var(--accent);
  font-size: inherit;
  cursor: pointer;
  text-decoration: underline;
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


/* @ 引用会话 */
.mention-menu {
  position: absolute;
  left: 8px;
  right: 8px;
  bottom: calc(100% + 6px);
  z-index: 30;
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

.mention-head {
  padding: 4px 8px 6px;
  color: var(--muted);
  font-size: 11px;
}

.mention-option {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 8px;
  border: none;
  border-radius: var(--r-sm);
  background: transparent;
  color: var(--ink);
  text-align: left;
  cursor: pointer;
}

.mention-option.on {
  background: var(--accent-soft);
}

.mention-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
}

.mention-meta {
  flex: 0 0 auto;
  color: var(--muted);
  font-size: 11px;
}

/* 与消息同一条中线、同样的左右留白，标签贴着气泡的右边缘。 */
.refs {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
  max-width: var(--content-width);
  margin: -14px auto 20px;
  padding: 0 28px;
}

.ref {
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding: 2px 9px;
  border: 1px solid var(--rule);
  border-radius: var(--r-full);
  background: var(--surface);
  color: var(--accent);
  font-size: 11.5px;
  cursor: pointer;
}

.ref:hover {
  background: var(--accent-soft);
}
</style>
