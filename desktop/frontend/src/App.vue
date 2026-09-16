<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import {
  AddMCPServer,
  AddProvider,
  AddProviderModel,
  AddSearchEngine,
  ArchiveThread,
  BackgroundChats,
  ApproveMemory,
  ChooseAttachments,
  AuthorizeChannelBinding,
  ChannelBindings,
  ChannelStatus,
  ChoosePluginDirectory,
  CreateProject,
  DeleteProject,
  DeleteProvider,
  DeleteMCPServer,
  DeletePlugin,
  DeleteSearchEngine,
  DiscardAttachment,
  ForgetMemory,
  MCPServers,
  Memories,
  MemorySettings,
  LastModelSelection,
  ImportAttachments,
  MoveThreadToProject,
  OpenAttachment,
  OpenOutputFile,
  PluginConfigFields,
  Plugins,
  PollWeChatLogin,
  Projects,
  Providers,
  RevealAttachment,
  RevealOutputFile,
  RevokeChannelBinding,
  RemoveProviderModel,
  SearchEngines,
  SetLastModelSelection,
  SetMemorySettings,
  SetPluginConfig,
  StartChat,
  StartWeChatLogin,
  StartRetry,
  Status,
  SyncProviderModels,
  ThreadMessages,
  Threads,
  ToggleMCPServer,
  TogglePlugin,
  ToggleSearchEngine,
} from "../wailsjs/go/main/App";
import {
  BrowserOpenURL,
  EventsOn,
  OnFileDrop,
  OnFileDropOff,
} from "../wailsjs/runtime/runtime";
import { renderMarkdown } from "./markdown";
import {
  resolveModelSelection,
  type ModelSelection,
} from "./model-selection";

type Provider = {
  id: number;
  name: string;
  type: string;
  base_url: string;
  models: string[];
};
type Project = { uuid: string; name: string };
type Thread = {
  uuid: string;
  title: string;
  project_uuid: string;
  provider_id: number;
  model_name: string;
  updated_at: string;
};
type Attachment = {
  uuid: string;
  filename: string;
  content_type: string;
  file_size: number;
  file_type: string;
  preview_url?: string;
  available: boolean;
};
type OutputFile = {
  path: string;
  filename: string;
  content_type: string;
  file_size: number;
  file_type: string;
  preview_url?: string;
  description?: string;
  available: boolean;
};
type ExecutionStatus = "pending" | "running" | "success" | "error";
type ExecutionStep = {
  id: string;
  name: string;
  status: ExecutionStatus;
  message: string;
  input?: string;
  output?: string;
  error?: string;
  started_at?: number;
  duration_ms?: number;
};
type Message = {
  role: string;
  content: string;
  attachments?: Attachment[];
  files?: OutputFile[];
  execution?: ExecutionStep[];
  retryable?: boolean;
  streaming?: boolean;
  live_request_id?: string;
};
type LiveRun = {
  requestID: string;
  threadID: string;
  message: Message;
};
type BackgroundChat = {
  request_id: string;
  thread_id: string;
  started_at: string;
};
type ChatFinished = {
  request_id: string;
  thread_id: string;
  content?: string;
  error?: string;
};
type MemoryItem = {
  uuid: string;
  kind: string;
  memory_key: string;
  content: string;
  status: string;
  importance: number;
  confidence: number;
  sensitivity: string;
  pinned: boolean;
  updated_at: string;
};
type ChatDelta = { request_id: string; thread_id: string; delta: string };
type ChatProgress = {
  request_id: string;
  thread_id: string;
  turn_id: string;
  kind: string;
  call_id?: string;
  name?: string;
  status?: ExecutionStatus;
  message?: string;
  input?: string;
  output?: string;
  error?: string;
  started_at?: number;
  duration_ms?: number;
};
type Search = {
  id: number;
  provider: string;
  name: string;
  base_url: string;
  enabled: boolean;
  api_key_set: boolean;
};
type MCP = {
  uuid: string;
  name: string;
  description: string;
  transport: string;
  endpoint: string;
  enabled: boolean;
  plugin_uuid: string;
};
type Plugin = {
  uuid: string;
  name: string;
  description: string;
  version: string;
  source: string;
  enabled: boolean;
  skill_count: number;
  mcp_count: number;
  tool_count: number;
  channel_count: number;
  permissions: string[];
  missing_config?: string[];
};
type PluginConfigField = {
  key: string;
  type: string;
  description?: string;
  required: boolean;
  secret: boolean;
  is_set: boolean;
  value?: string;
};
type ChannelBinding = {
  plugin_uuid: string;
  channel_id: string;
  external_key: string;
  display_name: string;
  thread_uuid?: string;
  provider_id?: number;
  model_name?: string;
  allowed: boolean;
  allowed_tools?: string[];
  last_message?: string;
};
type ChannelRuntimeStatus = {
  plugin_uuid: string;
  plugin_name: string;
  channel_id: string;
  display_name?: string;
  state: string;
  attempts?: number;
  last_error?: string;
};
type Theme = "dark" | "light";
type ConfirmationRequest = {
  title: string;
  message: string;
  confirmLabel: string;
  resolve: (confirmed: boolean) => void;
};

function savedTheme(): Theme {
  try {
    return localStorage.getItem("aiclaw-theme") === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

const theme = ref<Theme>(savedTheme());
document.documentElement.dataset.theme = theme.value;

const providers = ref<Provider[]>([]),
  projects = ref<Project[]>([]),
  threads = ref<Thread[]>([]);
const searches = ref<Search[]>([]),
  mcps = ref<MCP[]>([]),
  plugins = ref<Plugin[]>([]),
  bindings = ref<ChannelBinding[]>([]),
  channelStates = ref<ChannelRuntimeStatus[]>([]),
  memories = ref<MemoryItem[]>([]),
  messages = ref<Message[]>([]),
  pendingAttachments = ref<Attachment[]>([]);
const view = ref<"chat" | "settings">("chat"),
  settingsTab = ref<
    "providers" | "memory" | "search" | "computer" | "mcp" | "plugins"
  >("providers");
const projectID = ref(""),
  projectFilter = ref("all"),
  threadID = ref(""),
  providerID = ref(0),
  modelName = ref(""),
  online = ref(true),
  memoryUse = ref(true),
  memoryGenerate = ref(true);
// Plugin configuration is edited in a dialog: the fields a plugin declares
// are specific to it, and a secret is never prefilled.
const configPlugin = ref<Plugin | null>(null),
  configFields = ref<PluginConfigField[]>([]),
  configDrafts = ref<Record<string, string>>({}),
  configSaving = ref(false),
  configError = ref("");
// Authorizing an inbound conversation picks its model and acting tools rather
// than inheriting the desktop session's reach.
const authorizeTarget = ref<ChannelBinding | null>(null),
  // authorizeSelection carries "<provider id>:<model>" so one select picks both.
  authorizeSelection = ref(""),
  authorizeTools = ref<string[]>([]),
  authorizeError = ref("");
// Tools an inbound conversation can be granted. Everything outside this list
// is read-only and always available; these act on the machine.
const grantableTools = [
  { name: "write", label: "写文件" },
  { name: "edit", label: "改文件" },
  { name: "exec", label: "执行命令" },
  { name: "process", label: "管理进程" },
  { name: "code_interpreter", label: "运行代码" },
  { name: "computer", label: "控制屏幕" },
  { name: "cron", label: "定时任务" },
];
// wechatQRImage is a rendered PNG data URI. The relay's other string is the
// code's payload, which is not an image address — treating it as one is what
// produced a broken image here before.
const wechatQRImage = ref(""),
  wechatStatus = ref(""),
  wechatPolling = ref(false);
const prompt = ref(""),
  launching = ref(false),
  error = ref(""),
  projectName = ref(""),
  messagePane = ref<HTMLElement>(),
  projectInput = ref<HTMLInputElement>();
const projectFormOpen = ref(false),
  deletingProjectID = ref(""),
  providerFormOpen = ref(false),
  modelPickerOpen = ref(false);
const confirmation = ref<ConfirmationRequest>();
const executionClock = ref(Date.now());
const liveRuns = ref<Record<string, LiveRun>>({});
const providerForm = ref({
  name: "",
  type: "openai-compatible",
  baseURL: "",
  apiKey: "",
  model: "",
});
const modelManagerID = ref(0),
  remoteModels = ref<string[]>([]),
  modelSearch = ref(""),
  modelPickerSearch = ref(""),
  manualModel = ref(""),
  deletingModel = ref(""),
  attaching = ref(false),
  syncBusy = ref(false);
const searchForm = ref({
  provider: "tavily",
  name: "Tavily",
  baseURL: "https://api.tavily.com",
  apiKey: "",
});
const mcpForm = ref({
  name: "",
  description: "",
  transport: "stdio",
  endpoint: "",
  args: "[]",
  env: "{}",
  headers: "{}",
});

const provider = computed(() =>
  providers.value.find((item) => item.id === providerID.value),
);
const visibleThreads = computed(() => {
  if (projectFilter.value === "all") return threads.value;
  if (projectFilter.value === "unassigned")
    return threads.value.filter((item) => !item.project_uuid);
  return threads.value.filter(
    (item) => item.project_uuid === projectFilter.value,
  );
});
const unassignedThreadCount = computed(
  () => threads.value.filter((item) => !item.project_uuid).length,
);
function projectThreadCount(projectUUID: string) {
  return threads.value.filter((item) => item.project_uuid === projectUUID)
    .length;
}
const filteredRemoteModels = computed(() => {
  const q = modelSearch.value.trim().toLowerCase();
  const current =
    providers.value.find((p) => p.id === modelManagerID.value)?.models || [];
  return remoteModels.value
    .filter(
      (name) =>
        (!q || name.toLowerCase().includes(q)) && !current.includes(name),
    )
    .slice(0, 100);
});
const lastRetryableIndex = computed(() => {
  for (let index = messages.value.length - 1; index >= 0; index--) {
    if (messages.value[index].retryable) return index;
  }
  return -1;
});
const busy = computed(
  () =>
    launching.value ||
    Object.values(liveRuns.value).some(
      (run) =>
        (Boolean(run.threadID) && run.threadID === threadID.value) ||
        messages.value.some(
          (message) => message.live_request_id === run.requestID,
        ),
    ),
);

async function refresh(restoreLastModel = false) {
  try {
    error.value = await Status();
    const modelSelectionPromise: Promise<ModelSelection | undefined> =
      restoreLastModel
        ? LastModelSelection()
        : Promise.resolve(undefined);
    const [
      providerItems,
      projectItems,
      threadItems,
      searchItems,
      mcpItems,
      pluginItems,
      bindingItems,
      channelItems,
      memoryItems,
      memorySettings,
      savedModel,
    ] = await Promise.all([
      Providers(),
      Projects(),
      Threads(),
      SearchEngines(),
      MCPServers(),
      Plugins(),
      ChannelBindings(),
      ChannelStatus(),
      Memories(),
      MemorySettings(),
      modelSelectionPromise,
    ]);
    providers.value = providerItems;
    projects.value = projectItems;
    threads.value = threadItems;
    searches.value = searchItems;
    mcps.value = mcpItems;
    plugins.value = pluginItems;
    bindings.value = bindingItems ?? [];
    channelStates.value = channelItems ?? [];
    memories.value = memoryItems;
    memoryUse.value = memorySettings.use_memories;
    memoryGenerate.value = memorySettings.generate_memories;
    const selected = resolveModelSelection(
      providers.value,
      { provider_id: providerID.value, model_name: modelName.value },
      savedModel,
    );
    const changed =
      selected.provider_id !== providerID.value ||
      selected.model_name !== modelName.value;
    providerID.value = selected.provider_id;
    modelName.value = selected.model_name;
    if (changed) {
      void rememberModelSelection();
    }
  } catch (e) {
    error.value = String(e);
  }
}
async function rememberModelSelection() {
  if (!providerID.value || !modelName.value) return;
  const active = providers.value.find((item) => item.id === providerID.value);
  if (!active?.models.includes(modelName.value)) return;
  try {
    await SetLastModelSelection(providerID.value, modelName.value);
  } catch (e) {
    error.value = String(e);
  }
}
function requestID() {
  return globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`;
}
function chatProfile(id: string) {
  return {
    ProviderID: providerID.value,
    ModelName: modelName.value,
    SearchEnabled: online.value,
    ProjectUUID: projectID.value,
    RequestID: id,
    MemoryUseEnabled: memoryUse.value,
    MemoryGenerateEnabled: memoryGenerate.value,
  };
}
function beginAssistantStream(id: string, index?: number) {
  const message: Message = {
    role: modelName.value,
    content: "",
    execution: [
      {
        id: "analysis",
        name: "分析请求",
        status: "running",
        message: "正在整理上下文与可用能力",
      },
    ],
    retryable: false,
    streaming: true,
    live_request_id: id,
  };
  if (index === undefined) {
    messages.value.push(message);
  } else {
    messages.value.splice(index, 1, message);
  }
  return message;
}
function upsertExecution(message: Message, step: ExecutionStep) {
  message.execution ||= [];
  const current = message.execution.find((item) => item.id === step.id);
  if (current) {
    for (const [key, value] of Object.entries(step)) {
      if (value !== undefined) (current as any)[key] = value;
    }
  }
  else message.execution.push(step);
}
function completeExecution(message: Message, id: string, detail: string) {
  const step = message.execution?.find((item) => item.id === id);
  if (step && (step.status === "pending" || step.status === "running")) {
    step.status = "success";
    step.message = detail;
  }
}
function acceptChatDelta(event: ChatDelta) {
  const run = liveRunForEvent(event.request_id, event.thread_id);
  if (!run) return;
  const message = run.message;
  completeExecution(message, "analysis", "上下文分析完成");
  upsertExecution(message, {
    id: "response",
    name: "生成回复",
    status: "running",
    message: "正在流式生成答案",
  });
  message.content += event.delta;
  if (runIsVisible(run)) void scrollBottom();
}
function acceptChatProgress(event: ChatProgress) {
  const run = liveRunForEvent(event.request_id, event.thread_id);
  if (!run) return;
  const message = run.message;
  if (event.kind === "turn.started") {
    upsertExecution(message, {
      id: "analysis",
      name: "分析请求",
      status: "running",
      message: "正在整理上下文与可用能力",
    });
  } else if (event.kind === "tool.lifecycle") {
    completeExecution(message, "analysis", "已选择执行路径");
    upsertExecution(message, {
      id: event.call_id || `tool-${message.execution?.length || 0}`,
      name: event.name || "工具",
      status: event.status || "running",
      message: event.message || "正在执行",
      input: event.input,
      output: event.output,
      error: event.error,
      started_at: event.started_at,
      duration_ms: event.duration_ms,
    });
  } else if (event.kind === "turn.completed") {
    for (const step of message.execution || []) {
      if (step.status === "pending" || step.status === "running") {
        step.status = "success";
        step.message = step.id === "response" ? "回复生成完成" : "执行完成";
      }
    }
  } else if (event.kind === "turn.failed") {
    const step = [...(message.execution || [])]
      .reverse()
      .find((item) => item.status === "pending" || item.status === "running");
    if (step) {
      step.status = "error";
      step.message = event.error || "执行失败";
    }
  }
  if (runIsVisible(run)) void scrollBottom();
}
function finishAssistantStream(message: Message, fallback: string) {
  if (!message.content) message.content = fallback || "模型没有返回文本。";
  completeExecution(message, "analysis", "上下文分析完成");
  if (message.content) {
    upsertExecution(message, {
      id: "response",
      name: "生成回复",
      status: "success",
      message: "回复生成完成",
    });
  }
  for (const step of message.execution || []) {
    if (step.status === "pending" || step.status === "running") {
      step.status = "success";
      step.message = "执行完成";
    }
  }
  message.streaming = false;
  message.retryable = true;
}
function failAssistantStream(
  assistant: Message,
  message: string,
  prefix: string,
  visible: boolean,
) {
  if (visible) error.value = message;
  assistant.role = "系统";
  assistant.content = assistant.content || `${prefix}：${message}`;
  const step = [...(assistant.execution || [])]
    .reverse()
    .find((item) => item.status === "pending" || item.status === "running");
  if (step) {
    step.status = "error";
    step.message = message;
  }
  assistant.streaming = false;
  assistant.retryable = true;
}
function liveRunForEvent(request: string, eventThreadID: string) {
  const run = liveRuns.value[request];
  if (!run) return undefined;
  if (eventThreadID && !run.threadID) run.threadID = eventThreadID;
  if (
    eventThreadID &&
    !threadID.value &&
    messages.value.some((item) => item.live_request_id === request)
  ) {
    threadID.value = eventThreadID;
  }
  return run;
}
function runIsVisible(run: LiveRun) {
  return (
    (Boolean(run.threadID) && run.threadID === threadID.value) ||
    messages.value.some((item) => item.live_request_id === run.requestID)
  );
}
function removeLiveRun(request: string) {
  const next = { ...liveRuns.value };
  delete next[request];
  liveRuns.value = next;
}
async function acceptChatFinished(event: ChatFinished) {
  const run = liveRunForEvent(event.request_id, event.thread_id);
  if (!run) {
    await refresh();
    return;
  }
  const visible = runIsVisible(run);
  if (event.error)
    failAssistantStream(run.message, event.error, "生成失败", visible);
  else finishAssistantStream(run.message, event.content || "");
  removeLiveRun(event.request_id);
  await refresh();
  if (visible && event.thread_id === threadID.value) {
    try {
      messages.value = await loadThreadMessages(event.thread_id);
    } catch (e) {
      error.value = String(e);
    }
    await scrollBottom();
  }
}
function toggleTheme() {
  theme.value = theme.value === "dark" ? "light" : "dark";
  document.documentElement.dataset.theme = theme.value;
  try {
    localStorage.setItem("aiclaw-theme", theme.value);
  } catch {
    // Theme switching still works when persistent storage is unavailable.
  }
}
function openRenderedLink(event: MouseEvent) {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const anchor = target.closest<HTMLAnchorElement>("a[href]");
  const href = anchor?.getAttribute("href")?.trim();
  if (!href || href.startsWith("#")) return;
  event.preventDefault();
  if (/^(https?:|mailto:)/i.test(href)) BrowserOpenURL(href);
}
function executionIcon(status: ExecutionStatus) {
  if (status === "success") return "✓";
  if (status === "error") return "!";
  if (status === "running") return "";
  return "·";
}
function normalizedExecutionStatus(status: string): ExecutionStatus {
  if (status === "running" || status === "success" || status === "error") {
    return status;
  }
  return "pending";
}
function executionSummary(item: Message) {
  const steps = item.execution || [];
  const active = steps.find((step) => step.status === "running");
  if (active) {
    const command = commandText(active);
    return command ? `${executionVerb(active)} ${command}` : active.message;
  }
  const failed = steps.find((step) => step.status === "error");
  if (failed) {
    const command = commandText(failed);
    return command ? `${executionVerb(failed)} ${command}` : failed.message;
  }
  return steps.length ? `${steps.length} 个步骤已完成` : "";
}
type ToolInput = Record<string, unknown>;
function parsedToolInput(step: ExecutionStep): ToolInput {
  if (!step.input) return {};
  try {
    const value = JSON.parse(step.input);
    return value && typeof value === "object" && !Array.isArray(value)
      ? (value as ToolInput)
      : {};
  } catch {
    return {};
  }
}
function toolName(step: ExecutionStep) {
  return step.name.replace(/^调用\s+/, "").trim() || "工具";
}
function commandText(step: ExecutionStep) {
  const input = parsedToolInput(step);
  const command = input.command ?? input.cmd ?? input.script;
  if (typeof command === "string" && command.trim()) return command.trim();
  if (toolName(step) === "process" && typeof input.action === "string") {
    const session =
      typeof input.session_id === "string" ? ` ${input.session_id}` : "";
    return `process ${input.action}${session}`;
  }
  return "";
}
function workingDirectory(step: ExecutionStep) {
  const input = parsedToolInput(step);
  const value = input.working_dir ?? input.workdir ?? input.cwd;
  return typeof value === "string" ? value : "";
}
function formattedToolInput(step: ExecutionStep) {
  if (!step.input) return "";
  try {
    return JSON.stringify(JSON.parse(step.input), null, 2);
  } catch {
    return step.input;
  }
}
function executionVerb(step: ExecutionStep) {
  const command = commandText(step);
  if (step.status === "running") return command ? "正在运行" : "正在执行";
  if (step.status === "error") return command ? "命令失败" : "执行失败";
  if (step.status === "success") return command ? "已运行" : "已执行";
  return "等待执行";
}
function executionDuration(step: ExecutionStep) {
  let milliseconds = step.duration_ms || 0;
  if (step.status === "running" && step.started_at) {
    milliseconds = Math.max(0, executionClock.value - step.started_at);
  }
  if (!milliseconds) return step.message;
  const seconds = milliseconds / 1000;
  const value = seconds < 10 ? seconds.toFixed(1) : Math.round(seconds).toString();
  if (step.status === "running") return `已运行 ${value}s`;
  return `${value}s 内完成`;
}
function hasExecutionDetails(step: ExecutionStep) {
  return Boolean(step.input || step.output || step.error);
}
function pickerModels(item: Provider) {
  const query = modelPickerSearch.value.trim().toLowerCase();
  if (!query) return item.models;
  return item.models.filter(
    (name) =>
      name.toLowerCase().includes(query) ||
      item.name.toLowerCase().includes(query),
  );
}
function chooseModel(item: Provider, name: string) {
  providerID.value = item.id;
  modelName.value = name;
  modelPickerOpen.value = false;
  modelPickerSearch.value = "";
  void rememberModelSelection();
}
function toggleModelPicker() {
  modelPickerOpen.value = !modelPickerOpen.value;
  if (!modelPickerOpen.value) modelPickerSearch.value = "";
}
function newChat() {
  void discardPendingAttachments();
  threadID.value = "";
  messages.value = [];
  projectID.value =
    projectFilter.value !== "all" && projectFilter.value !== "unassigned"
      ? projectFilter.value
      : "";
  view.value = "chat";
}
function fileIcon(item: { file_type: string; content_type: string }) {
  if (item.file_type === "image") return "▧";
  if (item.content_type.includes("pdf")) return "PDF";
  if (item.content_type.includes("spreadsheet")) return "XLS";
  if (item.content_type.includes("presentation")) return "PPT";
  if (item.content_type.includes("wordprocessing")) return "DOC";
  return "TXT";
}
async function openAttachmentFile(item: Attachment, reveal = false) {
  try {
    error.value = "";
    if (reveal) await RevealAttachment(item.uuid);
    else await OpenAttachment(item.uuid);
  } catch (e) {
    error.value = String(e);
  }
}
async function openOutputFile(item: OutputFile, reveal = false) {
  try {
    error.value = "";
    if (reveal) await RevealOutputFile(item.path);
    else await OpenOutputFile(item.path);
  } catch (e) {
    error.value = String(e);
  }
}
function attachmentSize(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${Math.ceil(size / 1024)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}
function mergeAttachments(items: Attachment[]) {
  const merged = [...pendingAttachments.value];
  for (const item of items) {
    if (!merged.some((existing) => existing.uuid === item.uuid)) merged.push(item);
  }
  pendingAttachments.value = merged;
}
async function chooseAttachments() {
  if (busy.value || attaching.value) return;
  attaching.value = true;
  error.value = "";
  try {
    const items = await ChooseAttachments();
    if (pendingAttachments.value.length + (items?.length || 0) > 10) {
      await Promise.allSettled(
        (items || []).map((item) => DiscardAttachment(item.uuid)),
      );
      error.value = "一次最多添加 10 个附件";
      return;
    }
    mergeAttachments(items || []);
  } catch (e) {
    error.value = String(e);
  } finally {
    attaching.value = false;
  }
}
async function importAttachmentPaths(paths: string[]) {
  if (!paths.length || busy.value || attaching.value) return;
  if (pendingAttachments.value.length + paths.length > 10) {
    error.value = "一次最多添加 10 个附件";
    return;
  }
  attaching.value = true;
  error.value = "";
  try {
    mergeAttachments(await ImportAttachments(paths));
  } catch (e) {
    error.value = String(e);
  } finally {
    attaching.value = false;
  }
}
async function removePendingAttachment(item: Attachment) {
  try {
    await DiscardAttachment(item.uuid);
    pendingAttachments.value = pendingAttachments.value.filter(
      (attachment) => attachment.uuid !== item.uuid,
    );
  } catch (e) {
    error.value = String(e);
  }
}
async function discardPendingAttachments() {
  const items = pendingAttachments.value;
  pendingAttachments.value = [];
  await Promise.allSettled(items.map((item) => DiscardAttachment(item.uuid)));
}
function selectProject(id: string) {
  projectFilter.value = id;
  const active = threads.value.find((item) => item.uuid === threadID.value);
  const activeMatches =
    !active ||
    id === "all" ||
    (id === "unassigned"
      ? !active.project_uuid
      : active.project_uuid === id);
  if (!activeMatches) newChat();
  else if (!active)
    projectID.value = id === "all" || id === "unassigned" ? "" : id;
}
async function createProject() {
  try {
    const item = await CreateProject(projectName.value);
    projectName.value = "";
    projectFormOpen.value = false;
    projectFilter.value = item.uuid;
    newChat();
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function toggleProjectForm() {
  projectFormOpen.value = !projectFormOpen.value;
  if (projectFormOpen.value) {
    await nextTick();
    projectInput.value?.focus();
  } else {
    projectName.value = "";
  }
}
function askConfirmation(
  title: string,
  message: string,
  confirmLabel = "删除",
) {
  return new Promise<boolean>((resolve) => {
    confirmation.value = { title, message, confirmLabel, resolve };
  });
}
function resolveConfirmation(confirmed: boolean) {
  const pending = confirmation.value;
  if (!pending) return;
  confirmation.value = undefined;
  pending.resolve(confirmed);
}
async function removeProject(item: Project) {
  if (deletingProjectID.value) return;
  if (
    !(await askConfirmation(
      "删除项目？",
      `项目“${item.name}”会被删除，其中的会话将移入归档。`,
      "删除项目",
    ))
  )
    return;
  error.value = "";
  deletingProjectID.value = item.uuid;
  try {
    await DeleteProject(item.uuid);
    const activeWasInProject =
      threads.value.find((thread) => thread.uuid === threadID.value)
        ?.project_uuid === item.uuid;
    if (projectFilter.value === item.uuid) projectFilter.value = "all";
    if (projectID.value === item.uuid) projectID.value = "";
    if (activeWasInProject) newChat();
    await refresh();
  } catch (e) {
    error.value = String(e);
  } finally {
    deletingProjectID.value = "";
  }
}
async function openThread(item: Thread) {
  try {
    threadID.value = item.uuid;
    projectID.value = item.project_uuid;
    providerID.value = item.provider_id;
    modelName.value = item.model_name;
    void rememberModelSelection();
    const history = await loadThreadMessages(item.uuid);
    const active = Object.values(liveRuns.value).find(
      (run) => run.threadID === item.uuid,
    );
    if (active) history.push(active.message);
    messages.value = history;
    view.value = "chat";
    await scrollBottom();
  } catch (e) {
    error.value = String(e);
  }
}
async function loadThreadMessages(id: string): Promise<Message[]> {
  return (await ThreadMessages(id)).map((message) => ({
    ...message,
    execution: message.execution?.map((step) => ({
      ...step,
      status: normalizedExecutionStatus(step.status),
    })),
    retryable: message.role !== "你",
  }));
}
async function changeThreadProject(value: string) {
  const previous = projectID.value;
  projectID.value = value;
  if (!threadID.value) return;
  try {
    error.value = "";
    await MoveThreadToProject(threadID.value, value);
    if (projectFilter.value !== "all")
      projectFilter.value = value || "unassigned";
    await refresh();
  } catch (e) {
    projectID.value = previous;
    error.value = String(e);
  }
}
async function archive(item: Thread) {
  try {
    await ArchiveThread(item.uuid);
    if (threadID.value === item.uuid) newChat();
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function send() {
  if (
    (!prompt.value.trim() && !pendingAttachments.value.length) ||
    !providerID.value ||
    !modelName.value ||
    busy.value
  )
    return;
  const input = prompt.value.trim();
  const attachments = [...pendingAttachments.value];
  messages.value.push({
    role: "你",
    content: input || "请分析这些附件。",
    attachments,
  });
  const streamRequestID = requestID();
  const assistant = beginAssistantStream(streamRequestID);
  const run: LiveRun = {
    requestID: streamRequestID,
    threadID: threadID.value,
    message: assistant,
  };
  liveRuns.value = { ...liveRuns.value, [streamRequestID]: run };
  prompt.value = "";
  pendingAttachments.value = [];
  launching.value = true;
  error.value = "";
  void rememberModelSelection();
  await scrollBottom();
  try {
    const result = await StartChat(
      chatProfile(streamRequestID),
      threadID.value,
      input,
      attachments.map((item) => item.uuid),
    );
    run.threadID = result.thread_id;
    if (
      messages.value.some(
        (message) => message.live_request_id === streamRequestID,
      )
    ) {
      threadID.value = result.thread_id;
    }
    await refresh();
    await scrollBottom();
  } catch (e) {
    failAssistantStream(assistant, String(e), "生成失败", runIsVisible(run));
    removeLiveRun(streamRequestID);
  } finally {
    launching.value = false;
  }
}
async function retryLastAnswer() {
  if (!threadID.value || busy.value || lastRetryableIndex.value < 0) return;
  const index = lastRetryableIndex.value;
  const streamRequestID = requestID();
  const targetThreadID = threadID.value;
  const assistant = beginAssistantStream(streamRequestID, index);
  const run: LiveRun = {
    requestID: streamRequestID,
    threadID: targetThreadID,
    message: assistant,
  };
  liveRuns.value = { ...liveRuns.value, [streamRequestID]: run };
  launching.value = true;
  error.value = "";
  try {
    await StartRetry(chatProfile(streamRequestID), targetThreadID);
    await refresh();
  } catch (e) {
    failAssistantStream(assistant, String(e), "重试失败", runIsVisible(run));
    removeLiveRun(streamRequestID);
  } finally {
    launching.value = false;
    await scrollBottom();
  }
}

function isThreadRunning(id: string) {
  return Object.values(liveRuns.value).some((run) => run.threadID === id);
}

async function restoreBackgroundChats() {
  const active = (await BackgroundChats()) as BackgroundChat[];
  const next = { ...liveRuns.value };
  for (const item of active) {
    if (next[item.request_id]) continue;
    const thread = threads.value.find((entry) => entry.uuid === item.thread_id);
    next[item.request_id] = {
      requestID: item.request_id,
      threadID: item.thread_id,
      message: {
        role: thread?.model_name || "AIClaw",
        content: "",
        execution: [
          {
            id: "analysis",
            name: "后台执行",
            status: "running",
            message: "会话正在后台继续运行",
          },
        ],
        streaming: true,
        retryable: false,
        live_request_id: item.request_id,
      },
    };
  }
  liveRuns.value = next;
}
async function saveMemorySettings() {
  try {
    await SetMemorySettings(memoryUse.value, memoryGenerate.value);
  } catch (e) {
    error.value = String(e);
    await refresh();
  }
}
async function approveMemory(item: MemoryItem) {
  try {
    await ApproveMemory(item.uuid);
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function forgetMemory(item: MemoryItem) {
  if (
    !(await askConfirmation(
      "忘记这条记忆？",
      `“${item.content}”将从本地记忆中永久删除。`,
      "忘记",
    ))
  )
    return;
  try {
    await ForgetMemory(item.uuid);
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function scrollBottom() {
  await nextTick();
  if (messagePane.value)
    messagePane.value.scrollTop = messagePane.value.scrollHeight;
}

async function addProvider() {
  try {
    await AddProvider({
      Name: providerForm.value.name,
      Type: providerForm.value.type,
      BaseURL: providerForm.value.baseURL,
      APIKey: providerForm.value.apiKey,
      Model: providerForm.value.model,
    });
    providerForm.value = {
      name: "",
      type: "openai-compatible",
      baseURL: "",
      apiKey: "",
      model: "",
    };
    providerFormOpen.value = false;
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function removeModel(item: Provider, name: string) {
  const key = `${item.id}:${name}`;
  if (deletingModel.value) return;
  try {
    error.value = "";
    deletingModel.value = key;
    const updated = await RemoveProviderModel(item.id, name);
    providers.value = providers.value.map((provider) =>
      provider.id === updated.id ? updated : provider,
    );
    if (providerID.value === item.id && modelName.value === name)
      modelName.value = updated.models[0] || "";
    void rememberModelSelection();
    await refresh();
  } catch (e) {
    error.value = String(e);
  } finally {
    deletingModel.value = "";
  }
}
async function removeProvider(item: Provider) {
  if (
    !(await askConfirmation(
      "删除 Provider？",
      `Provider“${item.name}”及其本地配置将被删除。`,
      "删除 Provider",
    ))
  )
    return;
  try {
    await DeleteProvider(item.id);
    if (providerID.value === item.id) {
      providerID.value = 0;
      modelName.value = "";
    }
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
function manageModels(item: Provider) {
  modelManagerID.value = modelManagerID.value === item.id ? 0 : item.id;
  remoteModels.value = [];
  modelSearch.value = "";
  manualModel.value = "";
}
async function syncModels(item: Provider) {
  syncBusy.value = true;
  error.value = "";
  try {
    remoteModels.value = await SyncProviderModels(item.id);
  } catch (e) {
    error.value = String(e);
  } finally {
    syncBusy.value = false;
  }
}
async function addModel(item: Provider, name: string) {
  name = name.trim();
  if (!name) return;
  try {
    await AddProviderModel(item.id, name);
    manualModel.value = "";
    if (providerID.value === item.id && !modelName.value)
      modelName.value = name;
    await refresh();
    void rememberModelSelection();
  } catch (e) {
    error.value = String(e);
  }
}
async function addSearch() {
  try {
    await AddSearchEngine({
      Provider: searchForm.value.provider,
      Name: searchForm.value.name,
      BaseURL: searchForm.value.baseURL,
      APIKey: searchForm.value.apiKey,
    });
    searchForm.value.apiKey = "";
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function toggleSearch(item: Search) {
  try {
    await ToggleSearchEngine(item.id, !item.enabled);
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function removeSearch(item: Search) {
  try {
    await DeleteSearchEngine(item.id);
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function addMCP() {
  try {
    await AddMCPServer({
      Name: mcpForm.value.name,
      Description: mcpForm.value.description,
      Transport: mcpForm.value.transport,
      Endpoint: mcpForm.value.endpoint,
      Args: mcpForm.value.args,
      Env: mcpForm.value.env,
      Headers: mcpForm.value.headers,
    });
    mcpForm.value = {
      name: "",
      description: "",
      transport: "stdio",
      endpoint: "",
      args: "[]",
      env: "{}",
      headers: "{}",
    };
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function toggleMCP(item: MCP) {
  try {
    await ToggleMCPServer(item.uuid, !item.enabled);
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function removeMCP(item: MCP) {
  try {
    await DeleteMCPServer(item.uuid);
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function installPlugin() {
  try {
    await ChoosePluginDirectory();
    await refresh();
  } catch (e) {
    if (String(e)) error.value = String(e);
  }
}
async function togglePlugin(item: Plugin) {
  try {
    await TogglePlugin(item.uuid, !item.enabled);
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function removePlugin(item: Plugin) {
  try {
    await DeletePlugin(item.uuid);
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function openPluginConfig(item: Plugin) {
  configPlugin.value = item;
  configError.value = "";
  configDrafts.value = {};
  wechatQRImage.value = "";
  wechatStatus.value = "";
  try {
    configFields.value = (await PluginConfigFields(item.uuid)) ?? [];
  } catch (e) {
    configFields.value = [];
    configError.value = String(e);
  }
}
function closePluginConfig() {
  configPlugin.value = null;
  configFields.value = [];
  configDrafts.value = {};
  wechatPolling.value = false;
}
// Only edited fields are written. A secret keeps its stored value unless the
// user typed a replacement, and an emptied field clears the key.
async function savePluginConfig() {
  const target = configPlugin.value;
  if (!target) return;
  configSaving.value = true;
  configError.value = "";
  try {
    for (const field of configFields.value) {
      const draft = configDrafts.value[field.key];
      if (draft === undefined) continue;
      await SetPluginConfig(target.uuid, field.key, draft);
    }
    await refresh();
    const updated = plugins.value.find((item) => item.uuid === target.uuid);
    if (updated) configPlugin.value = updated;
    configFields.value = (await PluginConfigFields(target.uuid)) ?? [];
    configDrafts.value = {};
  } catch (e) {
    configError.value = String(e);
  } finally {
    configSaving.value = false;
  }
}
function pluginChannelStates(item: Plugin) {
  return channelStates.value.filter((state) => state.plugin_uuid === item.uuid);
}
function pluginBindings(item: Plugin) {
  return bindings.value.filter((binding) => binding.plugin_uuid === item.uuid);
}
const pendingBindings = computed(() =>
  bindings.value.filter((binding) => !binding.allowed),
);
const modelOptions = computed(() =>
  providers.value.flatMap((provider) =>
    (provider.models ?? []).map((name) => ({
      key: `${provider.id}:${name}`,
      label: `${provider.name} · ${name}`,
    })),
  ),
);
// Bindings carry RFC3339 timestamps; a raw one is unreadable in a list.
function formatBindingTime(value?: string) {
  if (!value) return "";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
function channelStateLabel(state: string) {
  return (
    {
      starting: "连接中",
      running: "已连接",
      retrying: "重连中",
      failed: "已停止",
      stopped: "未运行",
    }[state] ?? state
  );
}
function openAuthorize(binding: ChannelBinding) {
  authorizeTarget.value = binding;
  authorizeError.value = "";
  const provider = binding.provider_id || providerID.value;
  const model = binding.model_name || modelName.value;
  authorizeSelection.value = provider && model ? `${provider}:${model}` : "";
  authorizeTools.value = [...(binding.allowed_tools ?? [])];
}
function closeAuthorize() {
  authorizeTarget.value = null;
  authorizeError.value = "";
}
function toggleGrant(name: string) {
  const current = authorizeTools.value;
  authorizeTools.value = current.includes(name)
    ? current.filter((item) => item !== name)
    : [...current, name];
}
async function confirmAuthorize() {
  const target = authorizeTarget.value;
  if (!target) return;
  authorizeError.value = "";
  const [rawProvider, ...rest] = authorizeSelection.value.split(":");
  const provider = Number(rawProvider);
  const model = rest.join(":");
  if (!provider || !model) {
    authorizeError.value = "请先选择这个会话使用的模型。";
    return;
  }
  try {
    await AuthorizeChannelBinding(
      target.plugin_uuid,
      target.channel_id,
      target.external_key,
      provider,
      model,
      authorizeTools.value,
    );
    await refresh();
    closeAuthorize();
  } catch (e) {
    authorizeError.value = String(e);
  }
}
async function revokeBinding(binding: ChannelBinding) {
  const confirmed = await askConfirmation(
    "取消授权？",
    `「${binding.display_name || binding.external_key}」之后发来的消息将不再触发助手，历史会话保留。`,
    "取消授权",
  );
  if (!confirmed) return;
  try {
    await RevokeChannelBinding(
      binding.plugin_uuid,
      binding.channel_id,
      binding.external_key,
    );
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
// The WeChat connector signs in by QR scan; the credentials it returns go
// straight into the plugin's configuration and never reach this view.
async function startWeChatLogin() {
  const target = configPlugin.value;
  if (!target) return;
  configError.value = "";
  wechatStatus.value = "";
  try {
    const qr = await StartWeChatLogin();
    wechatQRImage.value = qr.image;
    wechatPolling.value = true;
    void pollWeChatLogin(target.uuid, qr.token);
  } catch (e) {
    configError.value = String(e);
  }
}
async function pollWeChatLogin(pluginUUID: string, token: string) {
  while (wechatPolling.value) {
    await new Promise((resolve) => setTimeout(resolve, 2000));
    if (!wechatPolling.value) return;
    try {
      const status = await PollWeChatLogin(pluginUUID, token);
      wechatStatus.value =
        {
          wait: "等待扫码…",
          scaned: "已扫码，请在手机上确认",
          confirmed: "已确认",
          expired: "二维码已过期，请重新获取",
        }[status.status] ?? status.status;
      if (status.status === "expired") {
        wechatPolling.value = false;
        wechatQRImage.value = "";
        return;
      }
      if (status.saved) {
        wechatPolling.value = false;
        wechatQRImage.value = "";
        wechatStatus.value = "登录成功，凭据已保存在本机";
        await refresh();
        configFields.value = (await PluginConfigFields(pluginUUID)) ?? [];
        return;
      }
    } catch (e) {
      wechatPolling.value = false;
      configError.value = String(e);
      return;
    }
  }
}
let stopChatDelta: (() => void) | undefined;
let stopChatProgress: (() => void) | undefined;
let stopChatFinished: (() => void) | undefined;
let executionClockTimer: number | undefined;
onMounted(() => {
  if ((window as any).runtime) {
    stopChatDelta = EventsOn("chat:delta", acceptChatDelta);
    stopChatProgress = EventsOn("chat:progress", acceptChatProgress);
    stopChatFinished = EventsOn("chat:finished", acceptChatFinished);
    OnFileDrop((_x, _y, paths) => void importAttachmentPaths(paths), true);
  }
  executionClockTimer = window.setInterval(() => {
    executionClock.value = Date.now();
  }, 1000);
  void (async () => {
    await refresh(true);
    await restoreBackgroundChats();
  })();
});
onUnmounted(() => {
  wechatPolling.value = false;
  stopChatDelta?.();
  stopChatProgress?.();
  stopChatFinished?.();
  if (executionClockTimer !== undefined)
    window.clearInterval(executionClockTimer);
  if ((window as any).runtime) OnFileDropOff();
});
</script>

<template>
  <main class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <span class="brand-mark">✦</span>
        <div><b>AIClaw</b><small>LOCAL INTELLIGENCE</small></div>
      </div>
      <button class="new-chat" @click="newChat">
        <span>＋</span> 新对话 <kbd>⌘N</kbd>
      </button>
      <section class="project-section">
        <div class="section-title">
          <span>工作区</span>
          <span class="section-actions"
            ><small>{{ projects.length }} 个项目</small
            ><button
              class="section-add"
              :title="projectFormOpen ? '取消新建项目' : '新建项目'"
              @click="toggleProjectForm"
            >
              {{ projectFormOpen ? "×" : "＋" }}
            </button></span
          >
        </div>
        <form
          v-if="projectFormOpen"
          class="project-create"
          @submit.prevent="createProject"
        >
          <input
            ref="projectInput"
            v-model="projectName"
            placeholder="项目名称"
            required
            @keydown.esc.prevent="toggleProjectForm"
          /><button title="创建项目">✓</button>
        </form>
        <nav class="project-navigation" aria-label="会话工作区">
          <button
            class="nav-row"
            :class="{ active: projectFilter === 'all' }"
            @click="selectProject('all')"
          >
            <span class="nav-icon all-icon">▦</span
            ><span class="nav-label">全部会话</span
            ><small class="nav-count">{{ threads.length }}</small>
          </button>
          <button
            class="nav-row"
            :class="{ active: projectFilter === 'unassigned' }"
            @click="selectProject('unassigned')"
          >
            <span class="nav-icon">○</span
            ><span class="nav-label">未归属</span
            ><small class="nav-count">{{ unassignedThreadCount }}</small>
          </button>
          <div v-for="item in projects" :key="item.uuid" class="nav-wrap">
            <button
              class="nav-row"
              :class="{ active: projectFilter === item.uuid }"
              @click="selectProject(item.uuid)"
            >
              <span class="nav-icon project-icon">◇</span
              ><span class="nav-label">{{ item.name }}</span
              ><small class="nav-count">{{
                projectThreadCount(item.uuid)
              }}</small>
            </button>
            <button
              class="row-action"
              :title="deletingProjectID === item.uuid ? '正在删除项目' : '删除项目'"
              :disabled="Boolean(deletingProjectID)"
              @click.stop="removeProject(item)"
            >
              {{ deletingProjectID === item.uuid ? "…" : "×" }}
            </button>
          </div>
        </nav>
      </section>
      <div class="section-title threads-title">
        <span>会话</span><small>{{ visibleThreads.length }}</small>
      </div>
      <div class="thread-list">
        <div v-for="item in visibleThreads" :key="item.uuid" class="nav-wrap">
          <button
            class="nav-row thread-row"
            :class="{ active: threadID === item.uuid }"
            @click="openThread(item)"
          >
            <span
              class="status-dot"
              :class="{ running: isThreadRunning(item.uuid) }"
            ></span
            ><span>{{ item.title || "新对话" }}</span
            ><small v-if="isThreadRunning(item.uuid)" class="background-label"
              >后台</small
            >
          </button>
          <button
            class="row-action"
            :title="isThreadRunning(item.uuid) ? '后台任务运行中' : '归档会话'"
            :disabled="isThreadRunning(item.uuid)"
            @click="archive(item)"
          >
            ⌁
          </button>
        </div>
        <p v-if="!visibleThreads.length" class="sidebar-empty">还没有会话</p>
      </div>
      <footer>
        <button
          class="theme-toggle"
          :title="theme === 'dark' ? '切换到亮色主题' : '切换到深色主题'"
          :aria-pressed="theme === 'light'"
          @click="toggleTheme"
        >
          <span class="theme-icon">{{ theme === "dark" ? "☀" : "☾" }}</span>
          <span>主题</span
          ><small>{{ theme === "dark" ? "深色" : "亮色" }}</small>
        </button>
        <button
          :class="{ active: view === 'settings' }"
          @click="view = 'settings'"
        >
          ⚙ <span>设置</span
          ><small>{{ plugins.length ? plugins.length + " 插件" : "" }}</small>
        </button>
      </footer>
    </aside>

    <section class="workspace">
      <div v-if="error" class="error-banner">
        <span>!</span>{{ error }}<button @click="error = ''">×</button>
      </div>
      <section v-if="view === 'chat'" class="chat-page">
        <header class="chat-header">
          <div class="chat-title-block">
            <span class="eyebrow">LOCAL WORKSPACE</span>
            <h1>{{ threadID ? "对话" : "开始新的对话" }}</h1>
            <p>
              {{
                threadID
                  ? "保持上下文连续，随时切换模型与能力"
                  : "选择工作区与模型，然后开始协作"
              }}
            </p>
          </div>
        </header>
        <div v-if="!providers.length" class="onboarding">
          <div class="orb">✦</div>
          <h2>连接你的模型</h2>
          <p>添加你自己的 Provider。密钥、项目和会话只保存在本机 SQLite。</p>
          <button
            @click="
              view = 'settings';
              settingsTab = 'providers';
            "
          >
            打开设置
          </button>
        </div>
        <div v-else ref="messagePane" class="messages">
          <div v-if="!messages.length" class="welcome">
            <div class="orb">✦</div>
            <h2>今天想完成什么？</h2>
            <p>本地文件与命令工具已准备就绪；浏览器控制可通过 MCP 或插件接入。</p>
            <div class="capabilities">
              <span>◎ Web Search</span><span>◫ Browser</span
              ><span>⌘ Local Tools</span><span>◇ MCP</span>
            </div>
          </div>
          <article
            v-for="(item, index) in messages"
            :key="index"
            :class="item.role === '你' ? 'user-message' : 'assistant-message'"
          >
            <div class="message-role">{{ item.role }}</div>
            <div v-if="item.attachments?.length" class="message-attachments">
              <article
                v-for="attachment in item.attachments"
                :key="attachment.uuid"
                :class="['message-attachment', attachment.file_type]"
              >
                <img
                  v-if="attachment.preview_url"
                  :src="attachment.preview_url"
                  :alt="attachment.filename"
                />
                <span v-else>{{ fileIcon(attachment) }}</span>
                <div class="file-card-copy">
                  <b>{{ attachment.filename }}</b>
                  <small
                    >{{ attachmentSize(attachment.file_size) }} ·
                    {{ attachment.available ? "本地附件" : "文件缺失" }}</small
                  >
                </div>
                <div class="file-card-actions">
                  <button
                    type="button"
                    :disabled="!attachment.available"
                    @click="openAttachmentFile(attachment)"
                  >
                    打开
                  </button>
                  <button
                    type="button"
                    :disabled="!attachment.available"
                    @click="openAttachmentFile(attachment, true)"
                  >
                    定位
                  </button>
                </div>
              </article>
            </div>
            <details
              v-if="item.role !== '你' && item.execution?.length"
              class="execution-trace"
              :open="item.streaming"
            >
              <summary>
                <span class="trace-glyph">⌁</span>
                <b>执行过程</b>
                <small>{{ executionSummary(item) }}</small>
                <i></i>
              </summary>
              <ol>
                <li
                  v-for="step in item.execution"
                  :key="step.id"
                  :class="`trace-${step.status}`"
                >
                  <details
                    class="trace-step"
                    :class="{ 'no-details': !hasExecutionDetails(step) }"
                    :open="step.status === 'running'"
                  >
                    <summary>
                      <span class="trace-status">{{ executionIcon(step.status) }}</span>
                      <span class="trace-action">
                        <b>{{ executionVerb(step) }}</b>
                        <code v-if="commandText(step)">{{ commandText(step) }}</code>
                        <span v-else>{{ toolName(step) }}</span>
                      </span>
                      <small>{{ executionDuration(step) }}</small>
                      <i v-if="hasExecutionDetails(step)"></i>
                    </summary>
                    <div v-if="hasExecutionDetails(step)" class="trace-detail">
                      <section v-if="commandText(step)">
                        <b>Shell</b><pre>{{ commandText(step) }}</pre>
                      </section>
                      <section v-if="workingDirectory(step)">
                        <b>工作目录</b><code>{{ workingDirectory(step) }}</code>
                      </section>
                      <section v-if="formattedToolInput(step)">
                        <b>调用参数</b><pre>{{ formattedToolInput(step) }}</pre>
                      </section>
                      <section v-if="step.output">
                        <b>输出</b><pre>{{ step.output }}</pre>
                      </section>
                      <section v-if="step.error" class="trace-error-output">
                        <b>错误</b><pre>{{ step.error }}</pre>
                      </section>
                    </div>
                  </details>
                </li>
              </ol>
            </details>
            <div
              class="message-body"
              :class="{ streaming: item.streaming }"
            >
              <div v-if="item.role === '你'" class="message-plain">
                {{ item.content }}
              </div>
              <div
                v-else
                class="message-markdown"
                v-html="renderMarkdown(item.content)"
                @click="openRenderedLink"
              ></div>
            </div>
            <section v-if="item.files?.length" class="output-files">
              <header>
                <span>↗</span>
                <div><b>生成文件</b><small>{{ item.files.length }} 个本地文件</small></div>
              </header>
              <div class="output-file-grid">
                <article
                  v-for="file in item.files"
                  :key="file.path"
                  :class="['output-file', file.file_type, { missing: !file.available }]"
                >
                  <img
                    v-if="file.preview_url"
                    :src="file.preview_url"
                    :alt="file.filename"
                  />
                  <span v-else>{{ fileIcon(file) }}</span>
                  <div class="file-card-copy">
                    <b>{{ file.filename }}</b>
                    <small :title="file.path">{{ file.path }}</small>
                    <em
                      >{{ file.available ? attachmentSize(file.file_size) : "文件已移动或删除" }}</em
                    >
                  </div>
                  <div class="file-card-actions">
                    <button
                      type="button"
                      :disabled="!file.available"
                      @click="openOutputFile(file)"
                    >
                      打开
                    </button>
                    <button
                      type="button"
                      :disabled="!file.available"
                      @click="openOutputFile(file, true)"
                    >
                      定位
                    </button>
                  </div>
                </article>
              </div>
            </section>
            <button
              v-if="item.retryable && index === lastRetryableIndex"
              class="retry-answer"
              :disabled="busy"
              title="重新生成最后一条回答"
              @click="retryLastAnswer"
            >
              ↻ 重试
            </button>
          </article>
        </div>
        <form v-if="providers.length" class="composer" @submit.prevent="send">
          <div class="composer-tools">
            <div class="model-picker">
              <button
                type="button"
                class="model-picker-trigger"
                :class="{ open: modelPickerOpen }"
                @click="toggleModelPicker"
              >
                <span class="model-glyph">✦</span
                ><span class="model-trigger-copy"
                  ><small>模型</small
                  ><b>{{ modelName || "选择模型" }}</b></span
                ><i></i>
              </button>
              <section v-if="modelPickerOpen" class="model-picker-popover">
                <header>
                  <div><b>选择模型</b><small>按 Provider 分组</small></div>
                  <button type="button" @click="toggleModelPicker">×</button>
                </header>
                <input
                  v-model="modelPickerSearch"
                  placeholder="搜索模型或 Provider…"
                  autofocus
                  @keydown.esc.prevent="toggleModelPicker"
                />
                <div class="model-picker-list">
                  <section
                    v-for="item in providers"
                    v-show="pickerModels(item).length"
                    :key="item.id"
                  >
                    <div class="model-provider-name">
                      <span>{{ item.name.slice(0, 1).toUpperCase() }}</span
                      ><b>{{ item.name }}</b
                      ><small>{{ pickerModels(item).length }}</small>
                    </div>
                    <button
                      v-for="name in pickerModels(item)"
                      :key="name"
                      type="button"
                      :class="{
                        active: providerID === item.id && modelName === name,
                      }"
                      @click="chooseModel(item, name)"
                    >
                      <span>{{ name }}</span><i>✓</i>
                    </button>
                  </section>
                </div>
                <p
                  v-if="
                    !providers.some((item) => pickerModels(item).length > 0)
                  "
                >
                  没有匹配的模型
                </p>
              </section>
            </div>
            <button
              type="button"
              class="composer-capability"
              :class="{ enabled: online }"
              @click="online = !online"
            >
              <i></i><span>联网</span>
            </button>
            <button
              type="button"
              class="composer-capability"
              :class="{ enabled: memoryUse }"
              title="是否在对话中使用本地记忆"
              @click="
                memoryUse = !memoryUse;
                saveMemorySettings();
              "
            >
              <i></i><span>记忆</span>
            </button>
            <button
              type="button"
              class="composer-capability attachment-trigger"
              :class="{ enabled: pendingAttachments.length > 0 }"
              :disabled="busy || attaching"
              title="添加文件或图片，也可以拖放到输入框"
              @click="chooseAttachments"
            >
              <span class="paperclip">＋</span
              ><span>{{ attaching ? "读取中" : "附件" }}</span
              ><b v-if="pendingAttachments.length">{{
                pendingAttachments.length
              }}</b>
            </button>
          </div>
          <div v-if="pendingAttachments.length" class="attachment-tray">
            <article
              v-for="item in pendingAttachments"
              :key="item.uuid"
              class="pending-attachment"
            >
              <img
                v-if="item.preview_url"
                :src="item.preview_url"
                :alt="item.filename"
              />
              <span v-else>{{ fileIcon(item) }}</span>
              <div>
                <b>{{ item.filename }}</b
                ><small>{{ attachmentSize(item.file_size) }}</small>
              </div>
              <button
                type="button"
                title="移除附件"
                @click="removePendingAttachment(item)"
              >
                ×
              </button>
            </article>
          </div>
          <textarea
            v-model="prompt"
            :disabled="busy"
            :placeholder="
              pendingAttachments.length
                ? '告诉 AIClaw 如何处理这些附件…'
                : '给 AIClaw 下达任务…'
            "
            @keydown.meta.enter.prevent="send"
          ></textarea>
          <div class="composer-footer">
            <span>⌘ Enter 发送 · 本地工具按权限运行</span
            ><button
              :disabled="
                busy || (!prompt.trim() && !pendingAttachments.length) || !modelName
              "
            >
              {{ busy ? "执行中…" : "发送" }} <b>↗</b>
            </button>
          </div>
        </form>
      </section>

      <section v-else-if="view === 'settings'" class="settings-page">
        <header class="page-header">
          <span class="eyebrow">CONFIGURATION</span>
          <h1>设置</h1>
          <p>管理模型、联网搜索以及本机工具运行环境。</p>
        </header>
        <nav class="tabs">
          <button
            :class="{ active: settingsTab === 'providers' }"
            @click="settingsTab = 'providers'"
          >
            模型 Provider</button
          ><button
            :class="{ active: settingsTab === 'memory' }"
            @click="settingsTab = 'memory'"
          >
            本地记忆</button
          ><button
            :class="{ active: settingsTab === 'search' }"
            @click="settingsTab = 'search'"
          >
            联网搜索</button
          ><button
            :class="{ active: settingsTab === 'computer' }"
            @click="settingsTab = 'computer'"
          >
            本地工具</button
          ><button
            :class="{ active: settingsTab === 'mcp' }"
            @click="settingsTab = 'mcp'"
          >
            MCP</button
          ><button
            :class="{ active: settingsTab === 'plugins' }"
            @click="settingsTab = 'plugins'"
          >
            插件
          </button>
        </nav>
        <div v-if="settingsTab === 'providers'" class="provider-settings">
          <div class="settings-toolbar">
            <div>
              <h2>模型服务</h2>
              <p>添加 Provider 后，可同步并选择要在对话中使用的模型。</p>
            </div>
            <button
              class="primary"
              @click="providerFormOpen = !providerFormOpen"
            >
              {{ providerFormOpen ? "取消" : "＋ 添加 Provider" }}
            </button>
          </div>
          <div :class="providerFormOpen ? 'settings-grid' : 'settings-single'">
            <form
              v-if="providerFormOpen"
              class="panel form-panel"
              @submit.prevent="addProvider"
            >
              <h2>添加 Provider</h2>
              <label
                >名称<input
                  v-model="providerForm.name"
                  placeholder="我的模型服务"
                  required /></label
              ><label
                >类型<select v-model="providerForm.type">
                  <option value="openai-compatible">OpenAI Compatible</option>
                  <option value="openai">OpenAI</option>
                  <option value="qwen">Qwen</option>
                  <option value="kimi">Kimi</option>
                  <option value="openrouter">OpenRouter</option>
                  <option value="claude">Claude</option>
                  <option value="gemini">Gemini</option>
                </select></label
              ><label
                >Base URL<input
                  v-model="providerForm.baseURL"
                  placeholder="https://api.example.com/v1"
                  required /></label
              ><label
                >初始模型（可选）<input
                  v-model="providerForm.model"
                  placeholder="保存后也可同步或添加" /></label
              ><label
                >API Key<input
                  v-model="providerForm.apiKey"
                  type="password"
                  autocomplete="off" /></label
              ><button class="primary">保存 Provider</button>
            </form>
            <div class="panel list-panel provider-list">
              <h2>
                已连接 <small>{{ providers.length }}</small>
              </h2>
              <div
                v-for="item in providers"
                :key="item.id"
                class="provider-entry"
              >
                <article>
                  <div class="item-icon">◆</div>
                  <div>
                    <b>{{ item.name }}</b>
                    <p>{{ item.type }} · {{ item.models.length }} 个模型</p>
                  </div>
                  <button class="secondary" @click="manageModels(item)">
                    {{
                      modelManagerID === item.id ? "收起" : "管理模型"
                    }}</button
                  ><button class="danger" @click="removeProvider(item)">
                    删除
                  </button>
                </article>
                <div v-if="modelManagerID === item.id" class="model-manager">
                  <div class="model-toolbar">
                    <input
                      v-model="modelSearch"
                      placeholder="搜索同步到的模型…"
                    /><button
                      class="secondary"
                      :disabled="syncBusy"
                      @click="syncModels(item)"
                    >
                      {{ syncBusy ? "同步中…" : "↻ 同步模型列表" }}
                    </button>
                  </div>
                  <div v-if="item.models.length" class="model-tags">
                    <span v-for="name in item.models" :key="name"
                      >{{ name
                      }}<button
                        type="button"
                        title="删除模型"
                        :aria-label="`删除模型 ${name}`"
                        :disabled="deletingModel === `${item.id}:${name}`"
                        @click.stop="removeModel(item, name)"
                      >
                        {{
                          deletingModel === `${item.id}:${name}` ? "…" : "×"
                        }}
                      </button></span
                    >
                  </div>
                  <p v-else class="empty-copy">
                    还没有已添加模型。可从服务同步或手动添加。
                  </p>
                  <form
                    class="manual-model"
                    @submit.prevent="addModel(item, manualModel)"
                  >
                    <input
                      v-model="manualModel"
                      placeholder="手动输入模型名称"
                    /><button class="primary">添加</button>
                  </form>
                  <div v-if="remoteModels.length" class="remote-models">
                    <button
                      v-for="name in filteredRemoteModels"
                      :key="name"
                      @click="addModel(item, name)"
                    >
                      <span>{{ name }}</span
                      ><b>＋ 添加</b>
                    </button>
                    <p v-if="!filteredRemoteModels.length" class="empty-copy">
                      没有匹配的未添加模型。
                    </p>
                  </div>
                </div>
              </div>
              <p v-if="!providers.length" class="empty-copy">
                尚未配置 Provider。
              </p>
            </div>
          </div>
        </div>
        <div v-else-if="settingsTab === 'memory'" class="memory-page">
          <section class="panel memory-controls">
            <div class="memory-heading">
              <div class="feature-icon">◉</div>
              <div>
                <h2>本地记忆</h2>
                <p>
                  记忆保存在 ~/.aiclaw/aiclaw.db，只向本机模型请求注入相关条目。
                </p>
              </div>
              <span class="local-badge">LOCAL ONLY</span>
            </div>
            <div class="memory-option">
              <div>
                <b>在新对话中使用记忆</b>
                <p>检索与当前问题相关的已批准记忆并加入上下文。</p>
              </div>
              <button
                class="switch"
                :class="{ on: memoryUse }"
                @click="
                  memoryUse = !memoryUse;
                  saveMemorySettings();
                "
              >
                <i></i>
              </button>
            </div>
            <div class="memory-option">
              <div>
                <b>允许对话生成记忆</b>
                <p>
                  明确要求“记住”时直接保存；模型推断的内容进入候选区等待批准。
                </p>
              </div>
              <button
                class="switch"
                :class="{ on: memoryGenerate }"
                @click="
                  memoryGenerate = !memoryGenerate;
                  saveMemorySettings();
                "
              >
                <i></i>
              </button>
            </div>
          </section>
          <section class="panel memory-library">
            <header>
              <div>
                <h2>
                  记忆库 <small>{{ memories.length }}</small>
                </h2>
                <p>所有条目均可审查、批准或彻底忘记。</p>
              </div>
            </header>
            <article v-for="item in memories" :key="item.uuid">
              <div class="memory-kind">{{ item.kind }}</div>
              <div class="memory-copy">
                <b>{{ item.content }}</b>
                <p>
                  {{ item.pinned ? "固定记忆" : "相关记忆" }} · 重要性
                  {{ item.importance }} · 置信度
                  {{ Math.round(item.confidence * 100) }}%
                </p>
              </div>
              <span class="memory-status" :class="item.status">
                {{ item.status === "active" ? "已启用" : "待批准" }}
              </span>
              <button
                v-if="
                  item.status === 'candidate' &&
                  item.sensitivity !== 'sensitive'
                "
                class="secondary"
                @click="approveMemory(item)"
              >
                批准
              </button>
              <button class="danger" @click="forgetMemory(item)">忘记</button>
            </article>
            <div v-if="!memories.length" class="memory-empty">
              <div class="orb">◌</div>
              <h2>还没有本地记忆</h2>
              <p>试着在对话中说：“请记住，我更喜欢简洁的回答。”</p>
            </div>
          </section>
        </div>
        <div v-else-if="settingsTab === 'search'" class="settings-grid">
          <form class="panel form-panel" @submit.prevent="addSearch">
            <h2>添加搜索引擎</h2>
            <label
              >服务<select v-model="searchForm.provider">
                <option value="tavily">Tavily</option>
                <option value="serpapi">SerpAPI</option>
                <option value="aliyun-iqs">Aliyun IQS</option>
              </select></label
            ><label>名称<input v-model="searchForm.name" required /></label
            ><label
              >Base URL<input v-model="searchForm.baseURL" required /></label
            ><label
              >API Key<input
                v-model="searchForm.apiKey"
                type="password"
                required /></label
            ><button class="primary">保存搜索引擎</button>
          </form>
          <div class="panel list-panel">
            <h2>
              联网搜索 <small>{{ searches.length }}</small>
            </h2>
            <article v-for="item in searches" :key="item.id">
              <div class="item-icon">◎</div>
              <div>
                <b>{{ item.name }}</b>
                <p>
                  {{ item.provider }} ·
                  {{ item.api_key_set ? "密钥已设置" : "无密钥" }}
                </p>
              </div>
              <button
                class="switch"
                :class="{ on: item.enabled }"
                @click="toggleSearch(item)"
              >
                <i></i></button
              ><button class="danger" @click="removeSearch(item)">删除</button>
            </article>
            <p v-if="!searches.length" class="empty-copy">
              配置搜索引擎后，聊天中的联网开关才会提供 web_search 工具。
            </p>
          </div>
        </div>
        <div v-else-if="settingsTab === 'computer'" class="panel feature-panel">
          <div class="feature-icon">⌘</div>
          <div>
            <h2>本地工具与浏览器扩展</h2>
            <p>
              文件、搜索和命令工具由统一执行器提供。浏览器导航、点击和截图请安装兼容的 MCP Server 或插件。
            </p>
            <div class="status-line"><span></span>运行时已启用</div>
          </div>
        </div>
        <div v-else-if="settingsTab === 'mcp'" class="settings-grid">
          <form class="panel form-panel" @submit.prevent="addMCP">
            <h2>添加 MCP Server</h2>
            <label>名称<input v-model="mcpForm.name" required /></label
            ><label
              >传输<select v-model="mcpForm.transport">
                <option value="stdio">stdio</option>
                <option value="sse">SSE</option>
                <option value="streamable_http">Streamable HTTP</option>
              </select></label
            ><label
              >{{ mcpForm.transport === "stdio" ? "命令" : "URL"
              }}<input v-model="mcpForm.endpoint" required /></label
            ><label v-if="mcpForm.transport === 'stdio'"
              >参数 JSON<input v-model="mcpForm.args" /></label
            ><label v-if="mcpForm.transport === 'stdio'"
              >环境变量 JSON<input v-model="mcpForm.env" /></label
            ><label v-else
              >Headers JSON<input v-model="mcpForm.headers" /></label
            ><button class="primary">保存 MCP</button>
          </form>
          <div class="panel list-panel">
            <h2>
              MCP Servers <small>{{ mcps.length }}</small>
            </h2>
            <article v-for="item in mcps" :key="item.uuid">
              <div class="item-icon">◇</div>
              <div>
                <b>{{ item.name }}</b>
                <p>{{ item.transport }} · {{ item.endpoint }}</p>
              </div>
              <button
                class="switch"
                :class="{ on: item.enabled }"
                @click="toggleMCP(item)"
              >
                <i></i>
              </button>
              <button v-if="!item.plugin_uuid" class="danger" @click="removeMCP(item)">删除</button>
            </article>
            <p v-if="!mcps.length" class="empty-copy">尚未配置 MCP Server。</p>
          </div>
        </div>
        <div v-else class="plugins-page">
          <header class="page-header split">
            <div>
              <span class="eyebrow">EXTENSIONS</span>
              <h1>插件</h1>
              <p>安装并管理 Codex 风格的本地插件、技能和 MCP。</p>
            </div>
            <button class="primary" @click="installPlugin">
              ＋ 从目录安装
            </button>
          </header>
          <div class="plugin-summary">
            <span
              ><b>{{ plugins.length }}</b> 插件</span
            ><span
              ><b>{{ plugins.reduce((n, p) => n + p.skill_count, 0) }}</b>
              技能</span
            ><span
              ><b>{{ plugins.reduce((n, p) => n + p.mcp_count, 0) }}</b>
              MCP</span
            ><span
              ><b>{{ plugins.reduce((n, p) => n + p.tool_count, 0) }}</b>
              工具</span
            ><span
              ><b>{{ plugins.reduce((n, p) => n + p.channel_count, 0) }}</b>
              连接器</span
            >
          </div>
          <div v-if="pendingBindings.length" class="pending-bindings">
            <h2>等待授权的会话 <small>{{ pendingBindings.length }}</small></h2>
            <p>
              这些外部会话给助手发过消息，但还没有被放行。收到消息本身不会触发助手——
              放行后你才决定它用哪个模型、能用哪些工具。
            </p>
            <article
              v-for="binding in pendingBindings"
              :key="binding.plugin_uuid + binding.channel_id + binding.external_key"
            >
              <div class="item-icon">◔</div>
              <div>
                <b>{{ binding.display_name || binding.external_key }}</b>
                <p>
                  {{ binding.channel_id }} ·
                  {{
                    binding.last_message
                      ? "最近 " + formatBindingTime(binding.last_message)
                      : "尚无消息"
                  }}
                </p>
              </div>
              <button class="primary" @click="openAuthorize(binding)">
                放行…
              </button>
            </article>
          </div>
          <div class="plugin-list">
            <article class="builtin">
              <div class="plugin-icon">⌘</div>
              <div>
                <b>Local Tool Runtime</b>
                <p>统一注册的本机文件、命令、会话、计划与技能工具。</p>
                <small>BUILT-IN</small>
              </div>
              <span class="state">已启用</span>
            </article>
            <article v-for="item in plugins" :key="item.uuid">
              <div class="plugin-icon">{{ item.source === "builtin" ? "◈" : "✦" }}</div>
              <div>
                <b
                  >{{ item.name }}
                  <em v-if="item.source === 'builtin'" class="badge">内置</em></b
                >
                <p>{{ item.description || "本地 AIClaw 插件" }}</p>
                <small
                  >{{ item.version || "LOCAL" }}
                  <template v-if="item.skill_count">· {{ item.skill_count }} SKILLS</template>
                  <template v-if="item.mcp_count">· {{ item.mcp_count }} MCP</template>
                  <template v-if="item.tool_count">· {{ item.tool_count }} TOOLS</template>
                  <template v-if="item.channel_count">· {{ item.channel_count }} CHANNELS</template>
                </small>
                <small v-if="item.permissions?.length" class="permission-list">{{ item.permissions.join(" · ") }}</small>
                <small v-if="item.missing_config?.length" class="config-warning"
                  >待配置：{{ item.missing_config.join("、") }}</small
                >
                <small
                  v-for="state in pluginChannelStates(item)"
                  :key="state.channel_id"
                  class="channel-state"
                  :class="state.state"
                  >{{ state.display_name || state.channel_id }}:
                  {{ channelStateLabel(state.state) }}
                  <template v-if="state.last_error">— {{ state.last_error }}</template>
                </small>
              </div>
              <button @click="openPluginConfig(item)">配置</button>
              <button
                class="switch"
                :class="{ on: item.enabled }"
                :disabled="!item.enabled && !!item.missing_config?.length"
                :title="
                  !item.enabled && item.missing_config?.length
                    ? '需要先完成配置才能启用'
                    : ''
                "
                @click="togglePlugin(item)"
              >
                <i></i>
              </button>
              <button
                v-if="item.source !== 'builtin'"
                class="danger"
                @click="removePlugin(item)"
              >
                删除
              </button>
            </article>
            <div v-if="!plugins.length" class="plugin-empty">
              <div class="orb">◌</div>
              <h2>添加第一个插件</h2>
              <p>
                支持含
                <code>.codex-plugin/plugin.json</code
                >、<code>SKILL.md</code>、<code>skills/</code> 或
                <code>mcp.json</code> 的目录。
              </p>
              <button class="primary" @click="installPlugin">
                浏览本地目录
              </button>
            </div>
          </div>
          <div
            v-if="bindings.some((binding) => binding.allowed)"
            class="panel list-panel binding-panel"
          >
            <h2>
              已授权的外部会话
              <small>{{ bindings.filter((binding) => binding.allowed).length }}</small>
            </h2>
            <article
              v-for="binding in bindings.filter((item) => item.allowed)"
              :key="binding.plugin_uuid + binding.channel_id + binding.external_key"
            >
              <div class="item-icon">◕</div>
              <div>
                <b>{{ binding.display_name || binding.external_key }}</b>
                <p>
                  {{ binding.channel_id }} · {{ binding.model_name || "未选模型" }}
                </p>
                <small>
                  可用工具：{{
                    binding.allowed_tools?.length
                      ? binding.allowed_tools.join(" · ")
                      : "仅只读"
                  }}
                </small>
              </div>
              <button @click="openAuthorize(binding)">调整…</button>
              <button class="danger" @click="revokeBinding(binding)">
                取消授权
              </button>
            </article>
          </div>
        </div>
      </section>
    </section>
    <div
      v-if="configPlugin"
      class="confirmation-backdrop"
      role="presentation"
      @click.self="closePluginConfig"
    >
      <section class="plugin-dialog" role="dialog" aria-modal="true">
        <header>
          <h2>{{ configPlugin.name }} · 配置</h2>
          <p>{{ configPlugin.description }}</p>
        </header>
        <p v-if="configError" class="dialog-error">{{ configError }}</p>
        <div v-if="configPlugin.permissions?.length" class="dialog-permissions">
          <b>启用后将授予</b>
          <span v-for="permission in configPlugin.permissions" :key="permission">{{
            permission
          }}</span>
        </div>
        <div
          v-if="configPlugin.description.includes('iLink')"
          class="dialog-notice"
        >
          该连接器通过第三方中继访问个人微信，并非官方接口。可用性与账号风险由使用者自行承担。
        </div>
        <div v-if="wechatQRImage || wechatStatus" class="wechat-login">
          <img v-if="wechatQRImage" :src="wechatQRImage" alt="微信登录二维码" />
          <p>{{ wechatStatus || "请使用微信扫描二维码" }}</p>
        </div>
        <button
          v-if="configFields.some((field) => field.key === 'bot_token')"
          @click="startWeChatLogin"
        >
          {{ wechatPolling ? "重新获取二维码" : "扫码登录微信" }}
        </button>
        <form v-if="configFields.length" @submit.prevent="savePluginConfig">
          <label v-for="field in configFields" :key="field.key">
            <span
              >{{ field.key
              }}<em v-if="field.required" class="required">必填</em></span
            >
            <input
              :type="field.secret ? 'password' : 'text'"
              :value="field.secret ? configDrafts[field.key] ?? '' : configDrafts[field.key] ?? field.value ?? ''"
              :placeholder="
                field.secret && field.is_set
                  ? '已配置，输入新值可替换'
                  : field.description || ''
              "
              :autocomplete="field.secret ? 'new-password' : 'off'"
              @input="
                configDrafts[field.key] = ($event.target as HTMLInputElement).value
              "
            />
            <small v-if="field.description">{{ field.description }}</small>
          </label>
          <div class="dialog-actions">
            <button type="button" @click="closePluginConfig">关闭</button>
            <button class="primary" :disabled="configSaving">
              {{ configSaving ? "保存中…" : "保存" }}
            </button>
          </div>
        </form>
        <div v-else class="dialog-actions">
          <p class="empty-copy">这个插件没有需要填写的配置。</p>
          <button type="button" @click="closePluginConfig">关闭</button>
        </div>
      </section>
    </div>
    <div
      v-if="authorizeTarget"
      class="confirmation-backdrop"
      role="presentation"
      @click.self="closeAuthorize"
    >
      <section class="plugin-dialog" role="dialog" aria-modal="true">
        <header>
          <h2>
            放行「{{ authorizeTarget.display_name || authorizeTarget.external_key }}」
          </h2>
          <p>
            这个会话发来的消息会触发一次助手回答。消息内容是外部数据，其中的指令不代表你的授权，
            所以默认只有只读工具；下面勾选的工具才会开放给它。
          </p>
        </header>
        <p v-if="authorizeError" class="dialog-error">{{ authorizeError }}</p>
        <form @submit.prevent="confirmAuthorize">
          <label
            >使用模型<select v-model="authorizeSelection">
              <option v-if="!modelOptions.length" value="">
                请先在「模型」里添加提供商
              </option>
              <option
                v-for="option in modelOptions"
                :key="option.key"
                :value="option.key"
              >
                {{ option.label }}
              </option>
            </select></label
          >
          <div class="grant-list">
            <b>额外开放的工具</b>
            <label
              v-for="tool in grantableTools"
              :key="tool.name"
              class="grant"
            >
              <input
                type="checkbox"
                :checked="authorizeTools.includes(tool.name)"
                @change="toggleGrant(tool.name)"
              />
              <span>{{ tool.label }}<code>{{ tool.name }}</code></span>
            </label>
          </div>
          <div class="dialog-actions">
            <button type="button" @click="closeAuthorize">取消</button>
            <button class="primary">确认放行</button>
          </div>
        </form>
      </section>
    </div>
    <div
      v-if="confirmation"
      class="confirmation-backdrop"
      role="presentation"
      @click.self="resolveConfirmation(false)"
    >
      <section
        class="confirmation-dialog"
        role="alertdialog"
        aria-modal="true"
        :aria-labelledby="'confirmation-title'"
        @keydown.esc.prevent="resolveConfirmation(false)"
      >
        <div class="confirmation-icon">!</div>
        <div class="confirmation-copy">
          <h2 id="confirmation-title">{{ confirmation.title }}</h2>
          <p>{{ confirmation.message }}</p>
        </div>
        <div class="confirmation-actions">
          <button type="button" @click="resolveConfirmation(false)">
            取消
          </button>
          <button
            type="button"
            class="confirmation-danger"
            autofocus
            @click="resolveConfirmation(true)"
          >
            {{ confirmation.confirmLabel }}
          </button>
        </div>
      </section>
    </div>
  </main>
</template>
