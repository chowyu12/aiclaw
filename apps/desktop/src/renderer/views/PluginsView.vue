<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from "vue";
import { actions, modelChoices, pluginConfigKey, store } from "../store";
import { describeError } from "../errors";

/**
 * 插件页：随应用分发的三个（computer use、微信、企业微信）加用户从目录装的。
 *
 * 一张卡片一个插件，收起时只看名字、来源、贡献了什么、开没开；点开是
 * 权限清单、配置项、以及通道插件特有的两块——连接状态与「谁在找它」（外部
 * 会话的授权）。**启用即授权**：启用那一步把声明的权限一次交出去，所以
 * 缺必填配置时内核会拒绝启用，原因摆到错误条上。
 *
 * 秘密只进不出：Key 类配置项存完只回「已配置」。微信不用手填——扫码登录后
 * 凭据由内核直接写进配置。
 */

type PluginRow = (typeof store.plugins)[number];
type BindingRow = (typeof store.bindings)[number];
type ConnectionRow = PluginRow["connections"][number];

/** 权限名给人看的说法。表在内核那边（internal/skills 的常量），这里只做翻译。 */
const PERMISSION_LABELS: Record<string, string> = {
  "computer.control": "操作屏幕（截屏、鼠标、键盘）",
  "filesystem.read": "读文件",
  "filesystem.write": "写文件",
  "network.access": "访问网络",
  "channel.receive": "接收外部消息",
  "channel.send": "向外部发消息",
  "secrets.read": "读取自己的秘密配置",
  "shell.exec": "执行命令",
};

/** 外部会话可以额外放开的内置工具。只读的那些默认就有，不用选。 */
const ACTING_TOOLS = [
  { name: "write_file", label: "写文件" },
  { name: "edit_file", label: "改文件" },
  { name: "run_command", label: "执行命令" },
];

const expanded = ref("");
const saving = ref(false);
/** 配置项的输入草稿，按「插件/键」存。存完清空，不让秘密留在 DOM 里。 */
const drafts = reactive<Record<string, string>>({});
/** 授权表单的草稿，按外部会话的键存。 */
const authDrafts = reactive<Record<string, { choice: string; tools: string[] }>>({});

// 微信扫码登录。二维码从中继来，几十秒后过期；确认之后凭据由内核写进配置。
// connectionId 空着是「添加一个微信号」，给了是给那个连接重新登录。
const wechatQR = ref<{ uuid: string; connectionId: string; token: string; image: string } | null>(null);
const wechatStatus = ref("");
/** 正在改名的连接与草稿。 */
const renaming = reactive<Record<string, string>>({});
/** 展开了凭据表单的连接（企微机器人刚加上时自动展开）。 */
const editing = reactive<Record<string, boolean>>({});
let pollTimer: ReturnType<typeof setTimeout> | undefined;

onMounted(() => void actions.loadPlugins());
onUnmounted(() => clearTimeout(pollTimer));

const choices = computed(() => modelChoices(store.providers));

function labelOf(permission: string): string {
  return PERMISSION_LABELS[permission] ?? permission;
}

function isChannel(plugin: PluginRow): boolean {
  return plugin.channels > 0;
}

function isWeChat(plugin: PluginRow): boolean {
  return plugin.pluginId === "aiclaw.wechat";
}

function channelsOf(plugin: PluginRow) {
  return store.channels.filter((c) => c.pluginUuid === plugin.uuid);
}

/** 一个连接的运行状态（没在跑就没有）。 */
function statusOf(connection: ConnectionRow) {
  return store.channels.find((c) => c.connectionId === connection.uuid);
}

/** 一个连接的放行记录。 */
function bindingsOf(plugin: PluginRow, connection: ConnectionRow) {
  return store.bindings.filter((b) => b.pluginUuid === plugin.uuid && b.connectionId === connection.uuid);
}

function bindingKey(binding: BindingRow): string {
  return `${binding.pluginUuid}/${binding.channelId}/${binding.connectionId}/${binding.externalKey}`;
}

/** 连接那一行的状态字。 */
function connectionState(plugin: PluginRow, connection: ConnectionRow): { text: string; kind: string } {
  if (connection.missingConfig.length > 0) {
    return { text: isWeChat(plugin) ? "还没登录" : `缺 ${connection.missingConfig.join("、")}`, kind: "bad" };
  }
  if (!plugin.enabled) return { text: "插件停用中", kind: "" };
  const status = statusOf(connection);
  if (!status) return { text: "未启动", kind: "" };
  return { text: STATE_LABELS[status.state] ?? status.state, kind: status.state };
}

function configOf(plugin: PluginRow, connectionId = "") {
  return store.pluginConfigs[pluginConfigKey(plugin.uuid, connectionId)] ?? [];
}

/** 收起时那一行右边的状态字。 */
function summary(plugin: PluginRow): string {
  if (isChannel(plugin)) {
    const total = plugin.connections.length;
    if (total === 0) return "还没有连接";
    const running = channelsOf(plugin).filter((c) => c.state === "running").length;
    return plugin.enabled ? `${total} 个连接 · ${running} 个已连接` : `${total} 个连接`;
  }
  if (plugin.missingConfig.length > 0) return `缺 ${plugin.missingConfig.join("、")}`;
  const parts: string[] = [];
  if (plugin.tools > 0) parts.push("宿主能力");
  if (plugin.channels > 0) parts.push("通道");
  if (plugin.skills > 0) parts.push(`${plugin.skills} 个技能`);
  if (plugin.mcp > 0) parts.push(`${plugin.mcp} 个 MCP`);
  return parts.join(" · ");
}

const STATE_LABELS: Record<string, string> = {
  starting: "连接中",
  running: "已连接",
  retrying: "重连中",
  failed: "连接失败",
  stopped: "已停止",
};

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

function toggleExpand(plugin: PluginRow): void {
  expanded.value = expanded.value === plugin.uuid ? "" : plugin.uuid;
  if (!expanded.value) return;
  if (isChannel(plugin)) {
    void actions.refreshChannels();
    for (const connection of plugin.connections) void actions.loadPluginConfig(plugin.uuid, connection.uuid);
  } else if (!store.pluginConfigs[plugin.uuid]) {
    void actions.loadPluginConfig(plugin.uuid);
  }
}

function draftKeyOf(plugin: PluginRow, key: string, connectionId = ""): string {
  return `${plugin.uuid}/${connectionId}/${key}`;
}

async function saveField(plugin: PluginRow, key: string, connectionId = ""): Promise<void> {
  const draftKey = draftKeyOf(plugin, key, connectionId);
  const value = (drafts[draftKey] ?? "").trim();
  if (!value) return;
  await run(() => actions.setPluginConfig(plugin.uuid, key, value, connectionId));
  drafts[draftKey] = "";
}

async function clearField(plugin: PluginRow, key: string, connectionId = ""): Promise<void> {
  await run(() => actions.setPluginConfig(plugin.uuid, key, "", connectionId));
}

// ---------- 连接 ----------

/** 加一个连接：企微机器人建好就展开凭据表单；微信直接出二维码，扫完才建。 */
async function addConnection(plugin: PluginRow): Promise<void> {
  if (isWeChat(plugin)) {
    await startWeChatLogin(plugin, "");
    return;
  }
  const id = await actions.createConnection(plugin.uuid);
  if (!id) return;
  editing[id] = true;
  await actions.loadPluginConfig(plugin.uuid, id);
}

function startRename(connection: ConnectionRow): void {
  renaming[connection.uuid] = connection.name;
}

async function finishRename(connection: ConnectionRow): Promise<void> {
  const name = (renaming[connection.uuid] ?? "").trim();
  delete renaming[connection.uuid];
  if (!name || name === connection.name) return;
  await run(() => actions.renameConnection(connection.uuid, name));
}

async function removeConnection(connection: ConnectionRow): Promise<void> {
  if (!confirm(`删除连接「${connection.name}」？它的凭据和放行记录一起删掉，已有的会话留着。`)) return;
  await run(() => actions.deleteConnection(connection.uuid));
}

async function remove(plugin: PluginRow): Promise<void> {
  if (!confirm(`删除插件「${plugin.name}」？它带的技能、MCP server 与通道授权一起删除。`)) return;
  await run(() => actions.deletePlugin(plugin.uuid));
}

// ---------- 通道授权 ----------

function authDraft(binding: BindingRow) {
  const key = bindingKey(binding);
  if (!authDrafts[key]) {
    const current = binding.providerId && binding.model ? `${binding.providerId}/${binding.model}` : "";
    authDrafts[key] = { choice: current, tools: [...binding.allowedTools] };
  }
  return authDrafts[key];
}

function toggleTool(binding: BindingRow, name: string, on: boolean): void {
  const draft = authDraft(binding);
  draft.tools = on ? [...new Set([...draft.tools, name])] : draft.tools.filter((t) => t !== name);
}

async function authorize(binding: BindingRow): Promise<void> {
  const draft = authDraft(binding);
  const [providerId, ...rest] = draft.choice.split("/");
  const model = rest.join("/");
  if (!providerId || !model) {
    actions.showError("放行前先给这个会话选一个模型。");
    return;
  }
  await run(() =>
    actions.authorizeBinding({
      pluginUuid: binding.pluginUuid,
      channelId: binding.channelId,
      connectionId: binding.connectionId,
      externalKey: binding.externalKey,
      providerId: Number(providerId),
      model,
      allowedTools: draft.tools,
    }),
  );
}

async function revoke(binding: BindingRow): Promise<void> {
  await run(() =>
    actions.revokeBinding({
      pluginUuid: binding.pluginUuid,
      channelId: binding.channelId,
      connectionId: binding.connectionId,
      externalKey: binding.externalKey,
    }),
  );
}

function formatTime(iso?: string): string {
  if (!iso) return "";
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

// ---------- 微信扫码 ----------

async function startWeChatLogin(plugin: PluginRow, connectionId: string): Promise<void> {
  clearTimeout(pollTimer);
  wechatStatus.value = "正在向中继要二维码…";
  try {
    const qr = await actions.wechatLoginStart();
    wechatQR.value = { uuid: plugin.uuid, connectionId, token: qr.token, image: qr.image };
    wechatStatus.value = "用微信扫一扫";
    void pollWeChat();
  } catch (error) {
    wechatQR.value = null;
    wechatStatus.value = describeError(error);
  }
}

/**
 * 轮询扫码进度。中继那边一次轮询最多等 40 秒（长轮询），所以这里不用自己
 * 加间隔；扫完、确认、过期任一结果都停下。
 */
async function pollWeChat(): Promise<void> {
  const current = wechatQR.value;
  if (!current) return;
  try {
    const result = await actions.wechatLoginPoll(current.uuid, current.token, current.connectionId);
    if (wechatQR.value?.token !== current.token) return;
    if (result.saved) {
      wechatStatus.value = current.connectionId
        ? "已重新登录，连接用新凭据重连。"
        : "已登录，加好了一个微信号。插件启用着的话它马上就连上。";
      wechatQR.value = null;
      await actions.loadPlugins();
      await actions.refreshChannels();
      return;
    }
    if (result.status === "expired") {
      wechatStatus.value = "二维码已过期，点「重新获取」。";
      wechatQR.value = null;
      return;
    }
    wechatStatus.value = result.status === "scaned" ? "已扫码，请在手机上确认" : "用微信扫一扫";
    pollTimer = setTimeout(() => void pollWeChat(), 1000);
  } catch (error) {
    wechatStatus.value = describeError(error);
    wechatQR.value = null;
  }
}
</script>

<template>
  <div class="page">
    <section>
      <header>
        <h2>插件</h2>
        <p class="sub">
          一个插件是一个带 <code>plugin.json</code> 的目录，声明它要的权限、配置项，
          以及它带来的东西：技能、MCP server、宿主能力（computer use）、通道（微信、企业微信）。
          装好是停用的——<strong>启用那一步才把声明的权限交出去</strong>。
        </p>
      </header>

      <div class="add">
        <button class="ghost" @click="actions.installPluginFromDirectory()">+ 从目录安装</button>
        <button class="ghost" @click="actions.loadPlugins()">刷新</button>
      </div>

      <p v-if="store.pluginsError" class="note warn">{{ store.pluginsError }}</p>

      <article
        v-for="plugin in store.plugins"
        :key="plugin.uuid"
        class="card"
        :class="{ off: !plugin.enabled }"
      >
        <div class="card-head">
          <button class="disclose" :aria-expanded="expanded === plugin.uuid" @click="toggleExpand(plugin)">
            <span class="chevron">{{ expanded === plugin.uuid ? "▾" : "▸" }}</span>
            <span class="name">{{ plugin.name }}</span>
            <span class="badge">{{ plugin.source === "builtin" ? "内置" : "本地" }}</span>
            <span class="state" :class="{ bad: plugin.missingConfig.length > 0 }">{{ summary(plugin) }}</span>
          </button>
          <label class="toggle">
            <input
              type="checkbox"
              :checked="plugin.enabled"
              @change="actions.togglePlugin(plugin.uuid, ($event.target as HTMLInputElement).checked)"
            />
            启用
          </label>
          <!-- 内置的只能停用：删了下次启动又会回来。 -->
          <button v-if="plugin.source !== 'builtin'" class="icon" title="删除" @click="remove(plugin)">×</button>
        </div>

        <template v-if="expanded === plugin.uuid">
          <p class="desc">{{ plugin.description || "（没有说明）" }}<span v-if="plugin.version" class="version"> v{{ plugin.version }}</span></p>

          <!-- 权限清单是启用前该看的东西，排在配置前面。 -->
          <div class="block">
            <div class="block-head"><span class="block-title">启用后获得的权限</span></div>
            <p v-if="plugin.permissions.length === 0" class="hint">不需要任何权限。</p>
            <ul v-else class="perms">
              <li v-for="permission in plugin.permissions" :key="permission">
                {{ labelOf(permission) }}<code>{{ permission }}</code>
              </li>
            </ul>
            <p v-if="plugin.tools > 0" class="note warn">
              启用后模型能<strong>看见并操作整个屏幕</strong>，不只是工作目录——包括别的应用、
              系统设置、以及本应用自己的窗口。除截屏外每个动作都会请你确认；最前面的应用是
              AIClaw 自己时直接拒绝。macOS 还要在「隐私与安全性」里给屏幕录制与辅助功能授权，
              且模型得看得懂图。这是这里权限最大的一项，不用就关掉。
            </p>
          </div>

          <!-- 配置项：秘密只进不出。渠道插件的配置在各自的连接里。 -->
          <div v-if="!isChannel(plugin) && configOf(plugin).length > 0" class="block">
            <div class="block-head"><span class="block-title">配置</span></div>
            <div v-for="field in configOf(plugin)" :key="field.key" class="field">
              <label>
                <span>
                  {{ field.key }}
                  <em v-if="field.required" class="req">必填</em>
                  <em v-if="field.isSet" class="set">已配置</em>
                </span>
                <div class="key-row">
                  <input
                    v-model="drafts[draftKeyOf(plugin, field.key)]"
                    :type="field.secret ? 'password' : 'text'"
                    :placeholder="field.secret ? (field.isSet ? '留空表示不修改' : '填入后只保存在本机') : (field.value || field.description || '')"
                    @keydown.enter="saveField(plugin, field.key)"
                  />
                  <button class="ghost small" :disabled="!drafts[draftKeyOf(plugin, field.key)]" @click="saveField(plugin, field.key)">
                    保存
                  </button>
                  <button v-if="field.isSet" class="ghost small" @click="clearField(plugin, field.key)">清掉</button>
                </div>
                <span v-if="field.description" class="hint">{{ field.description }}</span>
              </label>
            </div>
          </div>

          <!-- 渠道插件：一个连接（一个微信号、一个企微机器人）一张卡片。 -->
          <div v-if="isChannel(plugin)" class="block">
            <div class="block-head">
              <span class="block-title">连接</span>
              <span class="actions">
                <button class="ghost small" @click="actions.refreshChannels()">刷新</button>
                <button class="primary small" @click="addConnection(plugin)">
                  {{ isWeChat(plugin) ? "+ 添加微信号（扫码）" : "+ 添加机器人" }}
                </button>
              </span>
            </div>
            <p class="hint">
              每个连接各自一套凭据、各自在线；同一个人找不同的连接，是不同的会话。
              <template v-if="isWeChat(plugin)">登录走第三方中继（iLink），可用性与账号风险由你自己承担。</template>
            </p>

            <!-- 添加微信号时的二维码（还没有连接，扫完才建）。 -->
            <div v-if="wechatQR?.uuid === plugin.uuid && !wechatQR.connectionId" class="qr-box">
              <img class="qr" :src="wechatQR.image" alt="微信登录二维码" />
              <p class="hint">{{ wechatStatus }}</p>
            </div>
            <p v-else-if="wechatStatus && isWeChat(plugin) && !wechatQR" class="hint">{{ wechatStatus }}</p>

            <p v-if="plugin.connections.length === 0" class="hint">
              还没有连接。{{ isWeChat(plugin) ? "点「添加微信号」用微信扫码登录。" : "点「添加机器人」填入企业微信智能机器人的 bot_id 与 secret。" }}
            </p>

            <div v-for="connection in plugin.connections" :key="connection.uuid" class="connection">
              <div class="conn-head">
                <span class="dot" :class="connectionState(plugin, connection).kind" />
                <input
                  v-if="renaming[connection.uuid] !== undefined"
                  v-model="renaming[connection.uuid]"
                  class="rename"
                  @keydown.enter="finishRename(connection)"
                  @keydown.esc="delete renaming[connection.uuid]"
                  @blur="finishRename(connection)"
                />
                <span v-else class="conn-name" title="双击改名" @dblclick="startRename(connection)">{{ connection.name }}</span>
                <span class="conn-state" :class="connectionState(plugin, connection).kind">{{ connectionState(plugin, connection).text }}</span>
                <span v-if="statusOf(connection)?.lastError" class="hint bad">{{ statusOf(connection)?.lastError }}</span>
                <span class="actions">
                  <button class="ghost small" @click="startRename(connection)">改名</button>
                  <button v-if="isWeChat(plugin)" class="ghost small" @click="startWeChatLogin(plugin, connection.uuid)">重新扫码</button>
                  <button v-else class="ghost small" @click="editing[connection.uuid] = !editing[connection.uuid]">
                    {{ editing[connection.uuid] ? "收起凭据" : "凭据" }}
                  </button>
                  <button class="icon" title="删除这个连接" @click="removeConnection(connection)">×</button>
                </span>
              </div>

              <!-- 给这个连接重新登录的二维码。 -->
              <div v-if="wechatQR?.connectionId === connection.uuid" class="qr-box">
                <img class="qr" :src="wechatQR.image" alt="微信登录二维码" />
                <p class="hint">{{ wechatStatus }}</p>
              </div>

              <!-- 企微等手填凭据的连接：没配齐时一直展开。 -->
              <div v-if="!isWeChat(plugin) && (editing[connection.uuid] || connection.missingConfig.length > 0)" class="conn-config">
                <div v-for="field in configOf(plugin, connection.uuid)" :key="field.key" class="field">
                  <label>
                    <span>
                      {{ field.key }}
                      <em v-if="field.required" class="req">必填</em>
                      <em v-if="field.isSet" class="set">已配置</em>
                    </span>
                    <div class="key-row">
                      <input
                        v-model="drafts[draftKeyOf(plugin, field.key, connection.uuid)]"
                        :type="field.secret ? 'password' : 'text'"
                        :placeholder="field.secret ? (field.isSet ? '留空表示不修改' : '填入后只保存在本机') : (field.value || field.description || '')"
                        @keydown.enter="saveField(plugin, field.key, connection.uuid)"
                      />
                      <button
                        class="ghost small"
                        :disabled="!drafts[draftKeyOf(plugin, field.key, connection.uuid)]"
                        @click="saveField(plugin, field.key, connection.uuid)"
                      >
                        保存
                      </button>
                      <button v-if="field.isSet" class="ghost small" @click="clearField(plugin, field.key, connection.uuid)">清掉</button>
                    </div>
                  </label>
                </div>
              </div>

              <!-- 谁在通过这个连接找它。 -->
              <div class="conn-bindings">
                <p v-if="bindingsOf(plugin, connection).length === 0" class="hint">还没有人通过这个连接发过消息。</p>
                <div
                  v-for="binding in bindingsOf(plugin, connection)"
                  :key="bindingKey(binding)"
                  class="binding"
                  :class="{ allowed: binding.allowed }"
                >
                  <div class="binding-head">
                    <span class="name">{{ binding.displayName || binding.externalKey }}</span>
                    <code class="ext">{{ binding.externalKey }}</code>
                    <span class="badge" :class="{ ok: binding.allowed }">{{ binding.allowed ? "已放行" : "待放行" }}</span>
                    <span v-if="binding.lastMessage" class="hint">最近 {{ formatTime(binding.lastMessage) }}</span>
                  </div>
                  <div class="binding-form">
                    <select v-model="authDraft(binding).choice">
                      <option value="">选模型…</option>
                      <option v-for="c in choices" :key="`${c.providerId}/${c.model}`" :value="`${c.providerId}/${c.model}`">
                        {{ c.model }} · {{ c.providerName }}
                      </option>
                    </select>
                    <label v-for="tool in ACTING_TOOLS" :key="tool.name" class="tool">
                      <input
                        type="checkbox"
                        :checked="authDraft(binding).tools.includes(tool.name)"
                        @change="toggleTool(binding, tool.name, ($event.target as HTMLInputElement).checked)"
                      />
                      {{ tool.label }}
                    </label>
                    <button class="primary small" @click="authorize(binding)">
                      {{ binding.allowed ? "更新" : "放行" }}
                    </button>
                    <button v-if="binding.allowed" class="ghost small" @click="revoke(binding)">收回</button>
                  </div>
                </div>
              </div>
            </div>
            <p class="hint">
              外部会话（群或单聊）第一次发消息只会被记下，<strong>不会</strong>触发回答；你放行之后它才能用。
              放行时选模型、选它能动的工具——默认只有只读工具，因为发消息的人不是你。
            </p>
          </div>
        </template>
      </article>

      <p v-if="saving || store.pluginsLoading" class="note">{{ saving ? "保存中…" : "读取中…" }}</p>
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

.badge.ok {
  background: var(--accent-soft);
  color: var(--accent);
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

.desc {
  margin: 0;
  color: var(--ink-2);
  font-size: 12.5px;
  line-height: 1.7;
}

.version {
  color: var(--muted);
  font-family: var(--mono);
  font-size: 11px;
}

.block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface-2);
}

.block-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.block-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--ink-2);
}

.perms {
  margin: 0;
  padding-left: 18px;
  font-size: 12.5px;
  line-height: 1.8;
}

.perms code {
  margin-left: 6px;
  color: var(--muted);
  font-family: var(--mono);
  font-size: 11px;
}

.field label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 13px;
}

.field label > span {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--ink-2);
  font-family: var(--mono);
  font-size: 12px;
  font-weight: 500;
}

.field em {
  padding: 1px 7px;
  border-radius: var(--r-full);
  font-family: var(--sans, inherit);
  font-size: 10.5px;
  font-style: normal;
}

.field em.req {
  background: var(--surface);
  color: var(--muted);
}

.field em.set {
  background: var(--accent-soft);
  color: var(--accent);
}

.key-row {
  display: flex;
  gap: 8px;
}

.key-row input {
  flex: 1;
}

.key-row button {
  flex: 0 0 auto;
  white-space: nowrap;
}

button.small {
  padding: 3px 10px;
  font-size: 11.5px;
}

.qr {
  width: 200px;
  height: 200px;
  border-radius: var(--r-md);
  background: #fff;
  align-self: flex-start;
}

.channel {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--muted);
}

.dot.running {
  background: var(--accent);
}

.dot.failed,
.dot.retrying {
  background: var(--danger);
}

.connection {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
}

.conn-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.conn-name {
  font-weight: 600;
  font-size: 13px;
  cursor: text;
}

.conn-state {
  font-size: 12px;
  color: var(--muted);
}

.conn-state.running {
  color: var(--ok);
}

.conn-state.bad,
.conn-state.failed {
  color: var(--danger);
}

.conn-head .actions,
.block-head .actions {
  display: flex;
  gap: 6px;
  margin-left: auto;
}

.rename {
  width: 180px;
  padding: 2px 6px;
  font-size: 13px;
}

.conn-config,
.conn-bindings {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-left: 16px;
}

.qr-box {
  display: flex;
  align-items: center;
  gap: 14px;
}

.binding {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface);
}

.binding-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}

.ext {
  color: var(--muted);
  font-family: var(--mono);
  font-size: 11px;
}

.binding-form {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.binding-form select {
  min-width: 200px;
}

.tool {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--ink-2);
  white-space: nowrap;
}

.tool input {
  width: auto;
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
