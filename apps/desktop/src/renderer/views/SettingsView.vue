<script setup lang="ts">
import { ref } from "vue";
import { actions, store } from "../store";

const llmKey = ref("");
/** 「高级」默认收起：这三项地址与模型对绝大多数人都是默认值，不该占据视线。 */
const advanced = ref(false);
const modelPickerOpen = ref(false);
const clawToken = ref("");
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
  } finally {
    saving.value = false;
  }
}

async function saveKeys(): Promise<void> {
  const patch: { llmKey?: string; clawToken?: string } = {};
  if (llmKey.value) patch.llmKey = llmKey.value;
  if (clawToken.value) patch.clawToken = clawToken.value;
  if (Object.keys(patch).length === 0) return;
  await actions.saveCredentials(patch);
  // 存完立刻清空输入框：不让凭据留在 DOM 里。
  llmKey.value = "";
  clawToken.value = "";
}

/**
 * 默认模型改成从 airouter 的列表里挑。
 *
 * 手打模型名是这一页最容易出错的一格：名字写错了要等到第一次对话报 404 才知道，
 * 而那时错误来自上游、看起来像服务坏了。挑出来的同时把上下文窗口一起填上，
 * 省掉一个需要用户去查文档的数字。
 */
function openModelPicker(): void {
  modelPickerOpen.value = !modelPickerOpen.value;
  if (modelPickerOpen.value && store.models.length === 0 && !store.modelsLoading) {
    void actions.loadModels();
  }
}

async function pickModel(id: string, contextWindow: number): Promise<void> {
  modelPickerOpen.value = false;
  await saveField({ model: id, contextWindow });
}

function formatWindow(tokens: number): string {
  if (!tokens) return "窗口未知";
  return tokens >= 1000 ? `${Math.round(tokens / 1000)}K 上下文` : `${tokens} 上下文`;
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
        <h2>凭据</h2>
        <p class="sub">只存进系统钥匙串，不写配置文件、不进日志、不进诊断包。</p>
      </header>
      <p class="note warn">
        BFF Key 等同于登录态，且<strong>永不过期</strong>——内部平台侧的校验不检查有效期或吊销时间。
        它只存在系统钥匙串里，不会写进配置文件、日志或诊断包。怀疑泄露时请到内部平台删除该
        Key，改密码没有用。
      </p>
      <label>
        <span>LLM Key<em v-if="store.credentials.llmKey">已配置</em></span>
        <input v-model="llmKey" type="password" placeholder="留空表示不修改" />
      </label>
      <label>
        <span>内部平台 BFF Key<em v-if="store.credentials.clawToken">已配置</em></span>
        <input v-model="clawToken" type="password" placeholder="bff-…（留空表示不修改）" />
      </label>
      <button class="primary" :disabled="!llmKey && !clawToken" @click="saveKeys()">
        保存凭据
      </button>

      <!-- 地址与模型收进「高级」：公司里这两个地址对所有人都一样，默认值就是对的，
           摊在页面上只是让人怀疑自己是不是漏填了什么。 -->
      <button class="disclosure" @click="advanced = !advanced">
        <span class="caret" :class="{ open: advanced }">›</span>
        高级
        <span class="disclosure-note">模型端点、内部平台地址、默认模型</span>
      </button>

      <div v-if="advanced" class="advanced">
        <label>
          <span>模型端点</span>
          <input
            :value="store.config.modelBaseUrl"
            placeholder="https://llm.example.internal/v1"
            @change="saveField({ modelBaseUrl: ($event.target as HTMLInputElement).value })"
          />
          <span class="hint">airouter 的 OpenAI 兼容地址，填到 <code>/v1</code>。清空会退回默认值。</span>
        </label>

        <label>
          <span>内部平台地址</span>
          <input
            :value="store.config.clawUrl"
            placeholder="https://claw.example.internal"
            @change="saveField({ clawUrl: ($event.target as HTMLInputElement).value })"
          />
          <span class="hint">内部平台能力（数据 API、知识库这些）从这里拉。清空会退回默认值。</span>
        </label>

        <label>
          <span>默认模型</span>
          <div class="picker">
            <button class="field-button" @click="openModelPicker()">
              <span v-if="store.config.model" class="picked-name">{{ store.config.model }}</span>
              <span v-else class="placeholder">从 airouter 选一个</span>
              <span class="chev">⌄</span>
            </button>

            <div v-if="modelPickerOpen" class="backdrop" @click="modelPickerOpen = false" />
            <div v-if="modelPickerOpen" class="menu" @click.stop>
              <div class="menu-head">
                <span>airouter 上这把 Key 能调的模型</span>
                <button class="link" @click="actions.loadModels(true)">刷新</button>
              </div>
              <p v-if="store.modelsLoading" class="menu-note">正在拉取…</p>
              <p v-else-if="store.modelsError" class="menu-note warn">{{ store.modelsError }}</p>
              <p v-else-if="store.models.length === 0" class="menu-note">
                没有返回可用模型。先填好 LLM Key，再检查这把 Key 的模型白名单。
              </p>
              <button
                v-for="model in store.models"
                :key="model.id"
                class="menu-item"
                :class="{ picked: model.id === store.config.model }"
                @click="pickModel(model.id, model.contextWindow)"
              >
                <span class="menu-name">{{ model.id }}</span>
                <span class="menu-note">
                  {{ model.ownedBy }} · {{ formatWindow(model.contextWindow) }}
                  <template v-if="model.securityLevel"> · {{ model.securityLevel }}</template>
                </span>
              </button>
            </div>
          </div>
          <span class="hint">
            没设过的话第一次启动会自动挑一个。新会话用它；对话框上方切模型只影响那一个会话。
          </span>
        </label>

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
          白花一次请求。挑模型会自动带出这个值。
        </p>
      </div>
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
          :checked="store.config.enableComputerUse"
          @change="
            saveField({ enableComputerUse: ($event.target as HTMLInputElement).checked })
          "
        />
        <span>启用 computer use（截屏 + 鼠标键盘）</span>
      </label>
      <p class="note warn">
        开了之后 Agent 能<strong>看见并操作整个屏幕</strong>，不只是工作目录——
        包括别的应用、系统设置、以及本应用自己的窗口。每个动作都会请你确认；
        当最前面的应用是内部平台自己时会直接拒绝，免得它点到自己的审批弹窗。
        即便如此，这仍然是这里权限最大的一项，不用就关掉。
      </p>
      <p v-if="store.config.enableComputerUse" class="note">
        还需要系统授权：macOS 在「隐私与安全性」里给<strong>屏幕录制</strong>（截屏）
        与<strong>辅助功能</strong>（鼠标键盘）。另外模型得看得懂图——
        换一个不支持视觉的模型，截屏发过去它也读不出来。
      </p>
    </section>

    <section>
      <header>
        <h2>数据</h2>
        <p class="sub">会话与执行记录全部留在本机，不回写云端。</p>
      </header>
      <p class="note">云端只能看到 LLM 用量与内部平台 API 调用。</p>
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

.disclosure {
  display: flex;
  align-items: center;
  gap: 6px;
  align-self: flex-start;
  padding: 4px 0;
  border: none;
  background: none;
  color: var(--ink-2);
  font-size: 12.5px;
  font-weight: 500;
  cursor: pointer;
}

.disclosure:hover {
  color: var(--ink);
}

.caret {
  display: inline-block;
  color: var(--muted);
  transition: transform 0.12s;
}

.caret.open {
  transform: rotate(90deg);
}

.disclosure-note {
  color: var(--muted);
  font-weight: 400;
}

.advanced {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--rule);
}

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
