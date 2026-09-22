<script setup lang="ts">
import { reactive, ref } from "vue";
import { actions, store } from "../store";
import { describeError } from "../errors";
import type { McpProbeView, McpServerView } from "../../shared/types";

/**
 * 自定义 MCP server 的管理。
 *
 * 配好的默认**收起来**，点开才是编辑表单与工具清单。配三四个 server 之后
 * 一屏全是输入框，看不出「我到底配了什么、哪个是通的」——而那两件事才是
 * 回到这一页的理由。展开时试连一次，工具清单就是「它到底能干什么」的答案。
 */

/** store 里那份是 readonly() 包过的，模板上拿到的元素就是这个形状。 */
type ServerRow = (typeof store.mcpServers)[number];

const saving = ref(false);
/** 展开的 server id。一次只展开一个：这一页上同时比较两份表单没有意义。 */
const expanded = ref("");
/** 试连结果，按 server id 存。改了地址/命令之后作废。 */
const probes = reactive<Record<string, { loading: boolean; result?: McpProbeView }>>({});

async function commit(servers: McpServerView[]): Promise<void> {
  saving.value = true;
  try {
    await actions.saveMcpServers(servers);
  } finally {
    saving.value = false;
  }
}

async function add(transport: "stdio" | "http"): Promise<void> {
  const servers = actions.getMcpServers();
  const id = `m_${Date.now().toString(36)}`;
  servers.push({
    id,
    label: transport === "http" ? "remote" : "local",
    transport,
    enabled: false,
    ...(transport === "http" ? { url: "" } : { command: "", args: [] }),
  });
  await commit(servers);
  // 新加的直接展开：它是空的，收起来等于加了个看不见的东西。
  expanded.value = id;
}

async function patch(id: string, change: Partial<McpServerView>): Promise<void> {
  const servers = actions.getMcpServers().map((s) => (s.id === id ? { ...s, ...change } : s));
  await commit(servers);
  // 连接参数变了，上次试连的结果就不作数了。只改名字或开关不用清。
  if ("url" in change || "headers" in change || "command" in change || "args" in change || "env" in change) {
    delete probes[id];
  }
}

async function remove(id: string, label: string): Promise<void> {
  if (!confirm(`删除 MCP server「${label}」？下一个会话起它的工具就不再挂载。`)) return;
  await commit(actions.getMcpServers().filter((server) => server.id !== id));
  delete probes[id];
}

function toggleExpand(id: string): void {
  expanded.value = expanded.value === id ? "" : id;
  if (expanded.value && !probes[id]) void probe(id);
}

/**
 * 试连一次。失败的原因原样显示——「连不上」三个字帮不了任何人排查。
 *
 * 取的是 getMcpServers() 里那份可变副本：store 是 readonly() 包过的，
 * 它的元素带着 DeepReadonly，直接交给 IPC 过不了类型也克隆不了代理。
 */
async function probe(id: string): Promise<void> {
  const server = actions.getMcpServers().find((item) => item.id === id);
  if (!server) return;
  probes[id] = { loading: true };
  try {
    probes[id] = { loading: false, result: await actions.probeMcp(server) };
  } catch (error) {
    probes[id] = { loading: false, result: { ok: false, error: describeError(error) } };
  }
}

/** 收起时那一行右边的状态字。 */
function summary(server: ServerRow): string {
  const probed = probes[server.id];
  if (probed?.loading) return "检测中…";
  if (!probed?.result) return server.transport === "http" ? server.url ?? "" : server.command ?? "";
  if (!probed.result.ok) return "连不上";
  return `${probed.result.tools?.length ?? 0} 个工具`;
}

/** 参数按空格切。带空格的参数要用引号括起来。 */
function parseArgs(raw: string): string[] {
  return (raw.match(/"[^"]*"|'[^']*'|\S+/g) ?? []).map((part) =>
    part.replace(/^["']|["']$/g, ""),
  );
}

/** `KEY=value` 一行一条。 */
function parsePairs(raw: string): Record<string, string> {
  const result: Record<string, string> = {};
  for (const line of raw.split("\n")) {
    const separator = line.indexOf("=");
    if (separator <= 0) continue;
    result[line.slice(0, separator).trim()] = line.slice(separator + 1).trim();
  }
  return result;
}

function formatPairs(pairs: Record<string, string> | undefined): string {
  return Object.entries(pairs ?? {})
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}
</script>

<template>
  <div class="page">
    <section>
      <header>
        <h2>自定义 MCP server</h2>
        <p class="sub">
          挂第三方 MCP server 进来，它的工具会和内置工具一起交给模型。
          本地的用 stdio（拉起一个进程），远程的用 HTTP。改动下一个会话生效，
          回到已有会话也会重新挂一遍。
        </p>
      </header>

      <p class="note warn">
        第三方工具默认按<strong>有副作用</strong>处理，每次调用都会请你确认；
        只有 server 自己声明了只读（<code>readOnlyHint</code>）的工具才不问。
        本版本没有沙箱，不确定的时候宁可多问。
      </p>

      <div class="add">
        <button class="ghost" @click="add('stdio')">+ 本地（stdio）</button>
        <button class="ghost" @click="add('http')">+ 远程（HTTP）</button>
      </div>

      <p v-if="store.mcpServers.length === 0" class="note">
        还没有配置。常见的本地 server 形如
        <code>npx -y @modelcontextprotocol/server-filesystem /some/dir</code>。
      </p>

      <article
        v-for="server in store.mcpServers"
        :key="server.id"
        class="card"
        :class="{ off: !server.enabled }"
      >
        <div class="card-head">
          <button class="disclose" :aria-expanded="expanded === server.id" @click="toggleExpand(server.id)">
            <span class="chevron">{{ expanded === server.id ? "▾" : "▸" }}</span>
            <span class="name">{{ server.label || "未命名" }}</span>
            <span class="badge">{{ server.transport === "http" ? "HTTP" : "stdio" }}</span>
            <span
              class="state"
              :class="{ bad: probes[server.id]?.result?.ok === false }"
            >{{ summary(server) }}</span>
          </button>
          <label class="toggle">
            <input
              type="checkbox"
              :checked="server.enabled"
              @change="patch(server.id, { enabled: ($event.target as HTMLInputElement).checked })"
            />
            启用
          </label>
          <button class="icon" title="删除" @click="remove(server.id, server.label)">×</button>
        </div>

        <template v-if="expanded === server.id">
          <!-- 工具清单排在编辑表单前面：点开一个已经配好的 server，
               想看的是「它能干什么」，不是再确认一遍地址。 -->
          <div class="tools">
            <div class="tools-head">
              <span class="tools-title">工具</span>
              <button
                class="ghost small"
                :disabled="probes[server.id]?.loading"
                @click="probe(server.id)"
              >
                {{ probes[server.id]?.loading ? "检测中…" : "重新检测" }}
              </button>
            </div>

            <p v-if="probes[server.id]?.loading" class="hint">正在连接并拉取工具清单…</p>
            <p v-else-if="probes[server.id]?.result?.ok === false" class="note warn">
              连不上：{{ probes[server.id]?.result?.error }}
            </p>
            <p v-else-if="(probes[server.id]?.result?.tools?.length ?? 0) === 0" class="hint">
              连上了，但这个 server 没有暴露任何工具。
            </p>
            <div
              v-for="tool in probes[server.id]?.result?.tools ?? []"
              :key="tool.name"
              class="tool"
            >
              <div class="tool-head">
                <code>{{ server.label || server.id }}__{{ tool.name }}</code>
                <span class="badge">{{ tool.readOnly ? "只读 · 不问" : "要确认" }}</span>
              </div>
              <p v-if="tool.description" class="hint">{{ tool.description }}</p>
            </div>
          </div>

          <label>
            <span>名字（也是工具名前缀）</span>
            <input
              :value="server.label"
              placeholder="名字（也是工具名前缀）"
              @change="patch(server.id, { label: ($event.target as HTMLInputElement).value })"
            />
          </label>

          <template v-if="server.transport === 'stdio'">
            <label>
              <span>命令</span>
              <input
                :value="server.command"
                placeholder="npx"
                @change="patch(server.id, { command: ($event.target as HTMLInputElement).value })"
              />
            </label>
            <label>
              <span>参数</span>
              <input
                :value="(server.args ?? []).join(' ')"
                placeholder="-y @modelcontextprotocol/server-filesystem /some/dir"
                @change="patch(server.id, { args: parseArgs(($event.target as HTMLInputElement).value) })"
              />
            </label>
            <label>
              <span>环境变量</span>
              <textarea
                rows="2"
                :value="formatPairs(server.env)"
                placeholder="KEY=value，一行一条"
                @change="patch(server.id, { env: parsePairs(($event.target as HTMLTextAreaElement).value) })"
              />
            </label>
          </template>

          <template v-else>
            <label>
              <span>地址</span>
              <input
                :value="server.url"
                placeholder="https://example.com/mcp"
                @change="patch(server.id, { url: ($event.target as HTMLInputElement).value })"
              />
            </label>
            <label>
              <span>请求头</span>
              <textarea
                rows="2"
                :value="formatPairs(server.headers)"
                placeholder="Authorization=Bearer xxx，一行一条"
                @change="
                  patch(server.id, { headers: parsePairs(($event.target as HTMLTextAreaElement).value) })
                "
              />
            </label>
            <p class="hint">
              走 MCP 的 Streamable HTTP 传输（2025-03-26）。只支持 SSE 的旧版传输不支持。
            </p>
          </template>
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
  /* 不加这条「启用」会被挤到复选框下面一行。 */
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

.tools {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border: 1px solid var(--rule);
  border-radius: var(--r-md);
  background: var(--surface-2);
}

.tools-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.tools-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--ink-2);
}

button.small {
  padding: 3px 10px;
  font-size: 11.5px;
}

.tool {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.tool-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}

.tool code {
  font-family: var(--mono);
  font-size: 11.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 13px;
}

label > span {
  color: var(--ink-2);
  font-size: 12px;
  font-weight: 500;
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
