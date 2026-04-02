<template>
  <div class="chat-page">
    <!-- 侧栏 -->
    <aside class="chat-aside">
      <div class="aside-header">
        <div class="aside-brand">
          <span class="aside-brand-dot" />
          <!-- 多 Agent：name 变为可点击的下拉切换器 -->
          <el-dropdown
            v-if="agentStore.agents.length > 1"
            trigger="click"
            class="aside-agent-dropdown"
            @command="(uuid: string) => { const ag = agentStore.agents.find(a => a.uuid === uuid); if (ag) { agentStore.setActiveAgent(ag); defaultAgent = ag; resetChat(); loadConversations(); } }"
          >
            <div class="aside-agent-trigger" :title="currentAgentName">
              <span class="aside-title aside-title--agent">{{ currentAgentName }}</span>
              <el-icon class="aside-trigger-arrow"><ArrowDown /></el-icon>
            </div>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item
                  v-for="ag in agentStore.agents"
                  :key="ag.uuid"
                  :command="ag.uuid"
                  :class="{ 'agent-item--active': ag.uuid === currentAgent?.uuid }"
                >
                  <span class="agent-item-dot" :class="{ active: ag.uuid === currentAgent?.uuid }" />
                  {{ ag.name }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <!-- 单 Agent：纯文本 -->
          <span v-else class="aside-title" :class="{ 'aside-title--agent': !!defaultAgent }" :title="defaultAgent ? currentAgentName : undefined">
            {{ defaultAgent ? currentAgentName : '会话' }}
          </span>
        </div>
        <el-button class="aside-new-btn" circle size="small" @click="newConversation" :disabled="!currentAgent" title="新对话">
          <el-icon><Plus /></el-icon>
        </el-button>
      </div>

      <!-- Agent 与历史会话同一区域：Agent 固定在该区域顶部，下方为「最近」列表 -->
      <div class="aside-body">
        <div class="aside-session-scroll">
          <div class="aside-agent-card" v-if="currentAgent">
            <div class="agent-icon">
              <el-icon :size="22"><Cpu /></el-icon>
            </div>
            <div class="agent-info">
              <div class="agent-model agent-model--solo">{{ currentAgent.model_name }}</div>
            </div>
            <router-link to="/agents" class="settings-link">
              <el-icon :size="14"><Setting /></el-icon>
            </router-link>
          </div>
          <div v-else class="aside-empty-agent">
            <p>尚未配置 Agent</p>
            <router-link to="/agents" class="aside-empty-link">前往设置</router-link>
          </div>

          <template v-if="currentAgent">
            <div class="aside-divider" v-if="conversations.length > 0">
              <span>最近</span>
            </div>
            <div
              v-for="conv in conversations"
              :key="conv.id"
              class="conv-item"
              :class="{ active: activeConvId === conv.id }"
              @click="loadConversation(conv)"
            >
              <el-icon :size="14" class="conv-icon"><ChatDotRound /></el-icon>
              <div class="conv-info">
                <div class="conv-title">{{ conv.title || '未命名对话' }}</div>
                <div class="conv-time">{{ formatTime(conv.updated_at) }}</div>
              </div>
              <el-icon
                class="conv-delete"
                :size="14"
                @click.stop="deleteConv(conv.id)"
                title="删除"
              ><Delete /></el-icon>
            </div>
            <div v-if="conversations.length === 0" class="aside-conv-hint">
              暂无历史会话，点击右上角 + 开始新对话
            </div>
          </template>
        </div>
      </div>
    </aside>

    <!-- 主区域 -->
    <main class="chat-main">
      <header class="chat-main-head">
        <div class="chat-main-head-left">
          <h1 class="chat-main-title">对话</h1>
          <p v-if="currentAgent" class="chat-main-sub">{{ currentAgentName }} · {{ currentAgent.model_name }}</p>
          <p v-else class="chat-main-sub muted">配置 Agent 后即可开始</p>
        </div>
      </header>

      <div class="messages-area" ref="messagesArea">
        <div class="messages-inner">
        <!-- 加载中 -->
        <div v-if="loadingHistory" class="empty-state">
          <el-icon class="is-loading" :size="32" color="#3370ff"><Loading /></el-icon>
          <div class="empty-desc" style="margin-top: 12px">加载会话中...</div>
        </div>
        <!-- 空状态 -->
        <div v-else-if="!currentAgent || messages.length === 0" class="empty-state">
          <div class="empty-glow" />
          <div class="empty-icon-wrap">
            <el-icon :size="36"><ChatDotRound /></el-icon>
          </div>
          <div class="empty-title">{{ !currentAgent ? '请先完成 Agent 配置' : '新对话' }}</div>
          <div class="empty-desc" v-if="currentAgent">
            在下方输入消息，与 <strong>{{ currentAgentName }}</strong> 开始交流
          </div>
          <div class="empty-hint" v-else>在侧栏进入设置，绑定模型供应商与参数</div>
        </div>

        <!-- 消息列表 -->
        <template v-else>
          <div v-for="(msg, i) in messages" :key="i" :class="['msg-row', msg.role]">
            <div class="msg-avatar" :class="msg.role">
              <el-icon :size="16" v-if="msg.role === 'user'"><User /></el-icon>
              <el-icon :size="16" v-else><Cpu /></el-icon>
            </div>
            <div class="msg-body">
              <div class="msg-meta">
                <span class="msg-sender">{{ msg.role === 'user' ? '你' : currentAgentName }}</span>
                <span v-if="msg.role === 'assistant' && msg.tokens_used" class="msg-tokens">{{ msg.tokens_used }} tokens</span>
              </div>

              <!-- 消息气泡（附件 + 文本统一展示） -->
              <div class="msg-bubble">
                <div v-if="msg.files && msg.files.length > 0" class="bubble-attachments">
                  <template v-for="f in msg.files" :key="f.uuid">
                    <img v-if="f.file_type === 'image'" :src="'/public/files/' + f.uuid" :alt="f.filename" class="attach-img" />
                    <a v-else :href="'/public/files/' + f.uuid" target="_blank" class="attach-file">
                      <span class="attach-file-icon">{{ fileTypeIcon(f.file_type) }}</span>
                      <span class="attach-file-name">{{ f.filename }}</span>
                      <span class="attach-file-size" v-if="f.file_size">{{ formatFileSize(f.file_size) }}</span>
                    </a>
                  </template>
                </div>
                <div class="bubble-text" v-html="formatMessage(msg.content)"></div>
              </div>

              <!-- 操作按钮 -->
              <div class="msg-actions">
                <button class="action-btn" @click="copyMessage(msg, i)" title="复制">
                  <el-icon :size="14"><CopyDocument /></el-icon>
                  <span>{{ copiedMsgIdx === i ? '已复制' : '复制' }}</span>
                </button>
                <button v-if="msg.role === 'assistant'" class="action-btn" @click="retryMessage(i)" :disabled="streaming" title="重试">
                  <el-icon :size="14"><RefreshRight /></el-icon>
                  <span>重试</span>
                </button>
              </div>

              <!-- 执行步骤 -->
              <div v-if="msg.role === 'assistant' && msg.steps && msg.steps.length > 0" class="steps-panel">
                <div class="steps-toggle" @click="msg._showSteps = !msg._showSteps">
                  <el-icon :size="14"><Operation /></el-icon>
                  <span>{{ msg.steps.length }} 个执行步骤</span>
                  <el-icon class="toggle-icon" :class="{ open: msg._showSteps }"><ArrowDown /></el-icon>
                </div>
                <transition name="fold">
                  <div v-if="msg._showSteps" class="steps-list">
                    <div v-for="step in msg.steps" :key="step.step_order" class="step-row">
                      <div class="step-indicator">
                        <span class="step-dot" :class="stepDotClass(step)"></span>
                        <span class="step-line"></span>
                      </div>
                      <div class="step-body">
                        <div class="step-head">
                          <span class="step-badge" :class="stepBadgeClass(step)">{{ stepTypeLabel(step.step_type, step.name) }}</span>
                          <span v-if="subAgentDepthLabel(step)" class="step-depth">{{ subAgentDepthLabel(step) }}</span>
                          <span class="step-title">{{ step.name === 'sub_agent' ? (step.metadata?.tool_name || 'sub_agent') : step.name }}</span>
                          <el-tag
                            :type="step.status === 'success' ? 'success' : 'danger'"
                            size="small" round effect="plain"
                          >{{ step.status === 'success' ? step.duration_ms + 'ms' : 'failed' }}</el-tag>
                          <span v-if="step.tokens_used" class="step-tokens">{{ step.tokens_used }} tokens</span>
                        </div>
                        <div class="step-detail">
                          <template v-if="step.input">
                            <div class="detail-label">Input</div>
                            <pre class="detail-code">{{ truncateText(step.input, 500) }}</pre>
                          </template>
                          <template v-if="step.output">
                            <div class="detail-label">Output</div>
                            <pre class="detail-code">{{ truncateText(step.output, 500) }}</pre>
                          </template>
                          <template v-if="step.error">
                            <div class="detail-label detail-label--err">Error</div>
                            <pre class="detail-code detail-code--err">{{ step.error }}</pre>
                          </template>
                          <div class="detail-meta" v-if="step.metadata">
                            <span v-if="step.metadata.channel_type" class="step-channel">
                              渠道 {{ step.metadata.channel_type }}<template v-if="step.metadata.channel_id"> #{{ step.metadata.channel_id }}</template>
                              <template v-if="step.metadata.channel_thread_key"> · {{ step.metadata.channel_thread_key }}</template>
                              <template v-if="step.metadata.channel_sender_id"> · {{ step.metadata.channel_sender_id }}</template>
                            </span>
                            <span v-if="step.metadata.provider">{{ step.metadata.provider }}</span>
                            <span v-if="step.metadata.model">{{ step.metadata.model }}</span>
                            <span v-if="step.metadata.skill_name">Skill: {{ step.metadata.skill_name }}</span>
                            <span v-if="step.metadata.skill_tools?.length">{{ step.metadata.skill_tools.join(', ') }}</span>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                </transition>
              </div>
            </div>
          </div>

          <!-- 流式响应 -->
          <div v-if="streaming" class="msg-row assistant">
            <div class="msg-avatar assistant">
              <el-icon :size="16"><Cpu /></el-icon>
            </div>
            <div class="msg-body">
              <div class="msg-meta">
                <span class="msg-sender">{{ currentAgentName }}</span>
              </div>

              <!-- 实时步骤时间线 -->
              <div v-if="pendingSteps.length > 0 || !streamingContent" class="wf-timeline">
                <div v-for="(step, idx) in pendingSteps" :key="idx" class="wf-node">
                  <div class="wf-node-head" @click="step._expanded = !step._expanded">
                    <span class="wf-dot" :class="step.name === 'sub_agent' ? 'wf-dot--sub_agent' : ('wf-dot--' + step.step_type)"></span>
                    <span class="wf-label" :class="{ 'wf-label--sub_agent': step.name === 'sub_agent' }">{{ stepTypeLabel(step.step_type, step.name) }}</span>
                    <span v-if="subAgentDepthLabel(step)" class="wf-depth">{{ subAgentDepthLabel(step) }}</span>
                    <span class="wf-name">{{ step.name === 'sub_agent' ? (step.metadata?.tool_name || 'sub_agent') : step.name }}</span>
                    <el-tag v-if="step.status === 'success'" type="success" size="small" round effect="plain">{{ step.duration_ms }}ms</el-tag>
                    <el-tag v-else-if="step.status === 'error'" type="danger" size="small" round effect="plain">failed</el-tag>
                    <span v-if="step.tokens_used" class="wf-tokens">{{ step.tokens_used }} tokens</span>
                    <el-icon class="wf-arrow" :class="{ open: step._expanded }"><ArrowRight /></el-icon>
                  </div>
                  <transition name="fold">
                    <div v-if="step._expanded" class="wf-node-body">
                      <template v-if="step.input">
                        <div class="detail-label">Input</div>
                        <pre class="detail-code">{{ truncateText(step.input, 500) }}</pre>
                      </template>
                      <template v-if="step.output">
                        <div class="detail-label">Output</div>
                        <pre class="detail-code">{{ truncateText(step.output, 500) }}</pre>
                      </template>
                      <template v-if="step.error">
                        <div class="detail-label detail-label--err">Error</div>
                        <pre class="detail-code detail-code--err">{{ step.error }}</pre>
                      </template>
                      <div class="detail-meta" v-if="step.metadata">
                        <span v-if="step.metadata.channel_type" class="step-channel">
                          渠道 {{ step.metadata.channel_type }}<template v-if="step.metadata.channel_id"> #{{ step.metadata.channel_id }}</template>
                          <template v-if="step.metadata.channel_thread_key"> · {{ step.metadata.channel_thread_key }}</template>
                          <template v-if="step.metadata.channel_sender_id"> · {{ step.metadata.channel_sender_id }}</template>
                        </span>
                        <span v-if="step.metadata.provider">{{ step.metadata.provider }}</span>
                        <span v-if="step.metadata.model">{{ step.metadata.model }}</span>
                        <span v-if="step.metadata.skill_name">Skill: {{ step.metadata.skill_name }}</span>
                        <span v-if="step.metadata.skill_tools?.length">{{ step.metadata.skill_tools.join(', ') }}</span>
                      </div>
                    </div>
                  </transition>
                </div>

                <div v-if="!streamingContent" class="wf-node wf-node--thinking">
                  <span class="wf-dot wf-dot--thinking"><el-icon class="is-loading" :size="10"><Loading /></el-icon></span>
                  <span class="wf-thinking-text">{{ pendingSteps.length > 0 ? '生成回复中...' : '思考中...' }}</span>
                </div>
              </div>

              <!-- 流式文本 -->
              <div v-if="streamingContent" class="msg-bubble">
                <span v-html="formatMessage(streamingContent)"></span>
                <span class="typing-cursor"></span>
              </div>
            </div>
          </div>
        </template>
        </div>
      </div>

      <!-- 输入区域 -->
      <div class="input-area" :class="{ disabled: !defaultAgent }">
        <!-- 附件预览条 -->
        <div v-if="pendingFiles.length > 0 || pendingURLs.length > 0" class="attach-bar">
          <div v-for="(f, idx) in pendingFiles" :key="f.uuid" class="attach-chip">
            <span class="chip-icon">{{ fileTypeIcon(f.file_type) }}</span>
            <span class="chip-name">{{ f.filename }}</span>
            <span class="chip-size">{{ formatFileSize(f.file_size) }}</span>
            <el-icon class="chip-close" @click="removeFile(idx)"><Close /></el-icon>
          </div>
          <div v-for="(u, idx) in pendingURLs" :key="u" class="attach-chip chip--url">
            <el-icon :size="12"><Link /></el-icon>
            <span class="chip-name" :title="u">{{ u.length > 40 ? u.slice(0, 40) + '...' : u }}</span>
            <el-icon class="chip-close" @click="removeURL(idx)"><Close /></el-icon>
          </div>
        </div>

        <!-- URL 输入 -->
        <div v-if="showURLInput" class="url-bar">
          <el-input
            v-model="urlInput"
            size="small"
            placeholder="粘贴文件 URL，回车添加"
            @keydown.enter.prevent="addURL"
            clearable
            class="url-input"
          />
          <el-button size="small" type="primary" @click="addURL" :disabled="!urlInput.trim()">添加</el-button>
          <el-button size="small" text @click="showURLInput = false; urlInput = ''">取消</el-button>
        </div>

        <!-- 输入框 -->
        <div class="composer">
          <div class="composer-tools">
            <label class="tool-btn" :class="{ off: !defaultAgent || streaming || uploading }" title="上传文件">
              <el-icon :size="18"><UploadFilled /></el-icon>
              <input
                type="file" multiple style="display:none"
                accept=".txt,.md,.json,.csv,.xml,.yaml,.yml,.log,.pdf,.docx,.doc,.xlsx,.xls,.png,.jpg,.jpeg,.gif,.webp"
                :disabled="!defaultAgent || streaming || uploading"
                @change="handleFileUpload"
              />
            </label>
            <button
              class="tool-btn"
              :class="{ off: !defaultAgent || streaming, active: showURLInput }"
              :disabled="!defaultAgent || streaming"
              @click="showURLInput = !showURLInput"
              title="添加 URL"
            >
              <el-icon :size="18"><Link /></el-icon>
            </button>
          </div>
          <div class="composer-input">
            <el-input
              v-model="inputMessage"
              type="textarea"
              :autosize="{ minRows: 1, maxRows: 5 }"
              placeholder="输入消息，Enter 发送，Shift + Enter 换行"
              :disabled="!defaultAgent || streaming"
              @keydown="handleKeydown"
              resize="none"
            />
          </div>
          <button
            v-if="streaming"
            class="stop-btn"
            @click="stopGeneration"
            title="停止生成"
          >
            <span class="stop-square"></span>
          </button>
          <button
            v-else
            class="send-btn"
            :class="{ ready: defaultAgent && inputMessage.trim() }"
            :disabled="!defaultAgent || !inputMessage.trim()"
            @click="sendMessage"
          >
            <el-icon><Promotion /></el-icon>
          </button>
        </div>
      </div>
    </main>
  </div>
</template>

<script lang="ts">
import { ref } from 'vue'
import type { ExecutionStep, FileInfo } from '../../api/chat'

interface ChatMessage {
  id?: number
  role: string
  content: string
  tokens_used?: number
  steps?: ExecutionStep[]
  files?: FileInfo[]
  _showSteps?: boolean
}

// Module-level state — survives component unmount / remount on page navigation
const _messages = ref<ChatMessage[]>([])
const _streaming = ref(false)
const _streamingContent = ref('')
const _pendingSteps = ref<ExecutionStep[]>([])
const _conversationId = ref('')
const _activeConvId = ref<number>(0)
let _streamController: AbortController | null = null
</script>

<script setup lang="ts">
import { computed, onMounted, nextTick, reactive } from 'vue'
import { type Agent } from '../../api/agent'
import { useAgentStore } from '../../stores/agent'
import { chatApi, streamChat, retryStream, fileApi, type StreamChunk, type ChatFile, type Conversation, type Message } from '../../api/chat'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Marked } from 'marked'
import hljs from 'highlight.js/lib/core'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import python from 'highlight.js/lib/languages/python'
import bash from 'highlight.js/lib/languages/bash'
import json from 'highlight.js/lib/languages/json'
import css from 'highlight.js/lib/languages/css'
import xml from 'highlight.js/lib/languages/xml'
import sql from 'highlight.js/lib/languages/sql'
import go from 'highlight.js/lib/languages/go'
import yaml from 'highlight.js/lib/languages/yaml'
import markdown from 'highlight.js/lib/languages/markdown'
import diff from 'highlight.js/lib/languages/diff'

hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('js', javascript)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('ts', typescript)
hljs.registerLanguage('python', python)
hljs.registerLanguage('py', python)
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('sh', bash)
hljs.registerLanguage('shell', bash)
hljs.registerLanguage('json', json)
hljs.registerLanguage('css', css)
hljs.registerLanguage('html', xml)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('go', go)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('yml', yaml)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('md', markdown)
hljs.registerLanguage('diff', diff)

const md = new Marked({
  breaks: true,
  gfm: true,
  renderer: {
    code({ text, lang }: { text: string; lang?: string }) {
      const language = lang && hljs.getLanguage(lang) ? lang : ''
      const highlighted = language
        ? hljs.highlight(text, { language }).value
        : escapeHtml(text)
      const langLabel = lang || 'text'
      return `<div class="code-block"><div class="code-header"><span class="code-lang">${langLabel}</span><button class="code-copy" onclick="navigator.clipboard.writeText(this.closest('.code-block').querySelector('code').textContent).then(()=>{this.textContent='Copied!';setTimeout(()=>this.textContent='Copy',1500)})">Copy</button></div><pre><code class="hljs${language ? ' language-' + language : ''}">${highlighted}</code></pre></div>`
    },
    codespan({ text }: { text: string }) {
      return `<code class="inline-code">${text}</code>`
    },
    image({ href, title, text }: { href: string; title?: string | null; text?: string }) {
      const alt = text ? ` alt="${text}"` : ''
      const t = title ? ` title="${title}"` : ''
      return `<img src="${href}"${alt}${t} class="md-img" />`
    },
  },
})

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

interface UploadedFile {
  uuid: string
  filename: string
  file_type: 'text' | 'image' | 'document'
  file_size: number
}

const messages = _messages
const streaming = _streaming
const streamingContent = _streamingContent
const pendingSteps = _pendingSteps
const conversationId = _conversationId
const activeConvId = _activeConvId

const defaultAgent = ref<Agent | null>(null)
const inputMessage = ref('')
const messagesArea = ref<HTMLElement>()
const pendingFiles = ref<UploadedFile[]>([])
const pendingURLs = ref<string[]>([])
const urlInput = ref('')
const showURLInput = ref(false)
const uploading = ref(false)
const conversations = ref<Conversation[]>([])
const loadingHistory = ref(false)
const copiedMsgIdx = ref(-1)

// 多 Agent 支持
const agentStore = useAgentStore()

const currentAgentName = computed(() => agentStore.activeAgent?.name || defaultAgent.value?.name || 'Agent')

const currentAgent = computed(() => agentStore.activeAgent || defaultAgent.value)

onMounted(async () => {
  await agentStore.loadAgents()
  if (agentStore.activeAgent) {
    defaultAgent.value = agentStore.activeAgent
  }
  if (defaultAgent.value || agentStore.activeAgent) {
    loadConversations()
    scrollToBottom()
  }
})

async function loadConversations() {
  const ag = currentAgent.value
  if (!ag) { conversations.value = []; return }
  try {
    const params: any = { page: 1, page_size: 50, user_id: 'default' }
    if (ag.uuid) params.agent_uuid = ag.uuid
    const res: any = await chatApi.conversations(params)
    conversations.value = res.data?.list || []
  } catch {
    conversations.value = []
  }
  syncActiveConvId()
}

function syncActiveConvId() {
  if (!conversationId.value) {
    activeConvId.value = 0
    return
  }
  const match = conversations.value.find(c => c.uuid === conversationId.value)
  activeConvId.value = match ? match.id : 0
}

function parseChatMessages(msgs: Message[]): ChatMessage[] {
  return msgs
    .filter(m => {
      if (m.role === 'user') return true
      if (m.role === 'assistant') {
        if (!m.content?.trim()) return false
        if (m.tool_calls && typeof m.tool_calls === 'string') {
          try { const tc = JSON.parse(m.tool_calls); if (Array.isArray(tc) && tc.length > 0) return false } catch {}
        } else if (Array.isArray(m.tool_calls) && m.tool_calls.length > 0) {
          return false
        }
        return true
      }
      return false
    })
    .map(m => reactive({
      id: m.id,
      role: m.role,
      content: m.content,
      tokens_used: m.tokens_used,
      steps: m.steps,
      files: m.files,
      _showSteps: false,
    }))
}

async function loadConversation(conv: Conversation) {
  if (activeConvId.value === conv.id) return
  activeConvId.value = conv.id
  conversationId.value = conv.uuid
  loadingHistory.value = true
  try {
    const res: any = await chatApi.messages(conv.id, 100, true)
    messages.value = parseChatMessages(res.data || [])
    scrollToBottom()
  } catch {
    ElMessage.error('加载会话失败')
  } finally {
    loadingHistory.value = false
  }
}

async function reloadCurrentMessages() {
  await loadConversations()
  const conv = conversations.value.find(c => c.uuid === conversationId.value)
  if (conv) {
    activeConvId.value = conv.id
    try {
      const res: any = await chatApi.messages(conv.id, 100, true)
      messages.value = parseChatMessages(res.data || [])
    } catch {}
  }
  scrollToBottom()
}

async function deleteConv(id: number) {
  try {
    await ElMessageBox.confirm('确定删除该会话？', '删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  try {
    await chatApi.deleteConversation(id)
    conversations.value = conversations.value.filter(c => c.id !== id)
    if (activeConvId.value === id) {
      resetChat()
    }
  } catch {
    ElMessage.error('删除失败')
  }
}

function formatTime(t: string): string {
  if (!t) return ''
  const d = new Date(t)
  const now = new Date()
  const isToday = d.toDateString() === now.toDateString()
  if (isToday) return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' }) + ' ' + d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

function newConversation() {
  resetChat()
}

function resetChat() {
  conversationId.value = ''
  activeConvId.value = 0
  messages.value = []
  streamingContent.value = ''
  pendingSteps.value = []
  pendingFiles.value = []
  pendingURLs.value = []
  urlInput.value = ''
  showURLInput.value = false
  if (_streamController) {
    _streamController.abort()
    _streamController = null
  }
  streaming.value = false
}

function stopGeneration() {
  if (_streamController) {
    _streamController.abort()
    _streamController = null
  }
  if (streaming.value) {
    if (streamingContent.value) {
      const steps = [...pendingSteps.value]
      const tokensUsed = steps.reduce((sum, s) => sum + (s.tokens_used || 0), 0)
      messages.value.push(reactive({
        role: 'assistant',
        content: streamingContent.value,
        tokens_used: tokensUsed || undefined,
        steps,
        _showSteps: false,
      }))
    }
    streamingContent.value = ''
    pendingSteps.value = []
    streaming.value = false
    scrollToBottom()
    loadConversations()
  }
}

async function handleFileUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = input.files
  if (!files || files.length === 0) return
  uploading.value = true
  for (const file of Array.from(files)) {
    try {
      const res: any = await fileApi.upload(file)
      const f = res.data as FileInfo
      pendingFiles.value.push({ uuid: f.uuid, filename: f.filename, file_type: f.file_type, file_size: f.file_size })
    } catch {
      ElMessage.error(`上传 ${file.name} 失败`)
    }
  }
  uploading.value = false
  input.value = ''
}

function removeFile(idx: number) {
  const f = pendingFiles.value[idx]
  if (!f) return
  pendingFiles.value.splice(idx, 1)
  fileApi.delete(f.uuid).catch(() => {})
}

function addURL() {
  const url = urlInput.value.trim()
  if (!url) return
  try { new URL(url) } catch { ElMessage.warning('请输入有效的 URL'); return }
  if (pendingURLs.value.includes(url)) { ElMessage.warning('该 URL 已添加'); return }
  pendingURLs.value.push(url)
  urlInput.value = ''
}

function removeURL(idx: number) { pendingURLs.value.splice(idx, 1) }

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / 1048576).toFixed(1) + ' MB'
}

function fileTypeIcon(type: string): string {
  switch (type) {
    case 'image': return '🖼'
    case 'document': return '📄'
    default: return '📝'
  }
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    sendMessage()
  }
}

function scrollToBottom() {
  nextTick(() => {
    if (messagesArea.value) messagesArea.value.scrollTop = messagesArea.value.scrollHeight
  })
}

function sendMessage() {
  const text = inputMessage.value.trim()
  if (!text || !currentAgent.value) return

  const chatFiles: ChatFile[] = [
    ...pendingFiles.value.map(f => ({ type: f.file_type as ChatFile['type'], transfer_method: 'local_file' as const, upload_file_id: f.uuid })),
    ...pendingURLs.value.map(u => ({ type: 'document' as const, transfer_method: 'remote_url' as const, url: u })),
  ]

  const displayFiles: FileInfo[] = [
    ...pendingFiles.value.map(f => ({ ...f, id: 0, conversation_id: 0, message_id: 0, content_type: '', created_at: '' }) as FileInfo),
    ...pendingURLs.value.map(u => ({ id: 0, uuid: u, conversation_id: 0, message_id: 0, filename: u.split('/').pop() || 'url', content_type: '', file_size: 0, file_type: 'text' as const, created_at: '' })),
  ]

  messages.value.push(reactive({ role: 'user', content: text, files: displayFiles.length > 0 ? displayFiles : undefined }))
  inputMessage.value = ''
  pendingFiles.value = []
  pendingURLs.value = []
  urlInput.value = ''
  showURLInput.value = false
  streaming.value = true
  streamingContent.value = ''
  pendingSteps.value = []
  scrollToBottom()

  _streamController = streamChat(
    { agent_uuid: currentAgent.value?.uuid, conversation_id: conversationId.value, message: text, user_id: 'default', files: chatFiles.length > 0 ? chatFiles : undefined },
    (chunk: StreamChunk) => {
      if (chunk.conversation_id) conversationId.value = chunk.conversation_id
      if (chunk.delta) { streamingContent.value += chunk.delta; scrollToBottom() }
      if (chunk.steps?.length) { for (const s of chunk.steps) pendingSteps.value.push(reactive({ ...s, _expanded: false })) }
      else if (chunk.step) pendingSteps.value.push(reactive({ ...chunk.step, _expanded: false }))
      if (chunk.done) {
        const steps = chunk.steps?.length ? chunk.steps : [...pendingSteps.value]
        const tokensUsed = chunk.tokens_used || steps.reduce((sum, s) => sum + (s.tokens_used || 0), 0)
        const content = chunk.content || streamingContent.value
        const msg: any = { role: 'assistant', content, tokens_used: tokensUsed || undefined, steps, _showSteps: false, id: chunk.message_id || undefined }
        if (chunk.files?.length) msg.files = chunk.files
        messages.value.push(reactive(msg))
        streamingContent.value = ''
        pendingSteps.value = []
        streaming.value = false
        _streamController = null
        scrollToBottom()
        loadConversations()
      }
    },
    () => {
      streamingContent.value = ''
      pendingSteps.value = []
      streaming.value = false
      _streamController = null
      reloadCurrentMessages()
    },
    (_err: string) => {
      streamingContent.value = ''
      pendingSteps.value = []
      streaming.value = false
      _streamController = null
      reloadCurrentMessages()
    },
  )
}

function copyMessage(msg: ChatMessage, idx: number) {
  navigator.clipboard.writeText(msg.content).then(() => {
    copiedMsgIdx.value = idx
    setTimeout(() => { if (copiedMsgIdx.value === idx) copiedMsgIdx.value = -1 }, 2000)
  })
}

function retryMessage(assistantIdx: number) {
  if (streaming.value) return
  const assistantMsg = messages.value[assistantIdx]
  if (!assistantMsg || assistantMsg.role !== 'assistant') return

  let userIdx = assistantIdx - 1
  while (userIdx >= 0 && messages.value[userIdx]?.role !== 'user') userIdx--
  const userMsg = messages.value[userIdx]
  if (!userMsg) return

  if (assistantMsg.id && conversationId.value) {
    messages.value.splice(userIdx)
    streaming.value = true
    streamingContent.value = ''
    pendingSteps.value = []
    scrollToBottom()

    _streamController = retryStream(
      { conversation_id: conversationId.value, message_id: assistantMsg.id },
      (chunk: StreamChunk) => {
        if (chunk.delta) { streamingContent.value += chunk.delta; scrollToBottom() }
        if (chunk.steps?.length) { for (const s of chunk.steps) pendingSteps.value.push(reactive({ ...s, _expanded: false })) }
        else if (chunk.step) pendingSteps.value.push(reactive({ ...chunk.step, _expanded: false }))
        if (chunk.done) {
          const steps = chunk.steps?.length ? chunk.steps : [...pendingSteps.value]
          const tokensUsed = chunk.tokens_used || steps.reduce((sum, s) => sum + (s.tokens_used || 0), 0)
          const content = chunk.content || streamingContent.value
          const msg: any = { role: 'assistant', content, tokens_used: tokensUsed || undefined, steps, _showSteps: false, id: chunk.message_id || undefined }
          if (chunk.files?.length) msg.files = chunk.files
          messages.value.push(reactive({ role: 'user', content: userMsg.content }))
          messages.value.push(reactive(msg))
          streamingContent.value = ''
          pendingSteps.value = []
          streaming.value = false
          _streamController = null
          scrollToBottom()
          loadConversations()
        }
      },
      () => {
        if (streaming.value && (streamingContent.value || pendingSteps.value.length > 0)) {
          const steps = [...pendingSteps.value]
          const tokensUsed = steps.reduce((sum, s) => sum + (s.tokens_used || 0), 0)
          messages.value.push(reactive({ role: 'user', content: userMsg.content }))
          messages.value.push(reactive({ role: 'assistant', content: streamingContent.value, tokens_used: tokensUsed || undefined, steps, _showSteps: false }))
          streamingContent.value = ''
          pendingSteps.value = []
        }
        streaming.value = false
        _streamController = null
        loadConversations()
      },
      (err: string) => {
        const steps = [...pendingSteps.value]
        messages.value.push(reactive({ role: 'user', content: userMsg.content }))
        messages.value.push(reactive({ role: 'assistant', content: `[错误] ${err}`, steps, _showSteps: steps.length > 0 }))
        streamingContent.value = ''
        pendingSteps.value = []
        streaming.value = false
        _streamController = null
        scrollToBottom()
      },
    )
  } else {
    const userText = userMsg.content
    messages.value.splice(userIdx, assistantIdx - userIdx + 1)
    inputMessage.value = userText
    nextTick(() => sendMessage())
  }
}

function formatMessage(text: string): string {
  let s = text
  s = s.replace(/<img\s+[^>]*src\s*=\s*["'](?!\/public\/files\/)[^"']*["'][^>]*\/?>/gi, '')
  s = s.replace(/!\[[^\]]*\]\((?!\/public\/files\/)[^)]*\)/g, '')
  return md.parse(s) as string
}

function stepTypeLabel(t: string, name?: string) {
  if (t === 'tool_call' && name === 'sub_agent') return 'Sub Agent'
  switch (t) {
    case 'llm_call': return 'LLM'
    case 'tool_call': return 'Tool'
    case 'agent_call': return 'Agent'
    case 'skill_match': return 'Skill'
    default: return t
  }
}

function stepBadgeClass(step: any) {
  if (step.step_type === 'tool_call' && step.name === 'sub_agent') return 'badge--sub_agent'
  return 'badge--' + step.step_type
}

function stepDotClass(step: any) {
  if (step.step_type === 'tool_call' && step.name === 'sub_agent') return 'dot--sub_agent'
  return 'dot--' + step.step_type
}

function subAgentDepthLabel(step: any): string {
  const depth = step.metadata?.sub_agent_depth
  return depth ? `L${depth}` : ''
}

function truncateText(text: string, maxLen: number): string {
  return text.length <= maxLen ? text : text.slice(0, maxLen) + '...[truncated]'
}
</script>
<style scoped>
/* 对话页铺满主工作区，无外层圆角卡片（参考 OpenClaw 式扁平工作区） */
.chat-page {
  display: flex;
  height: 100%;
  min-height: 0;
  overflow: hidden;
  background: var(--chat-page-bg);
}

/* Sidebar */
.chat-aside {
  width: 268px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  background: var(--chat-aside-bg);
  border-right: 1px solid var(--chat-aside-border);
}
.aside-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 18px 16px 14px;
  border-bottom: 1px solid var(--chat-aside-header-border);
}
.aside-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1;
}
.aside-brand-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: linear-gradient(135deg, #22d3ee, #06b6d4);
  box-shadow: 0 0 12px rgba(34, 211, 238, 0.55);
}
.aside-title {
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--chat-aside-title);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.aside-title--agent {
  text-transform: none;
  letter-spacing: 0.02em;
  font-size: 15px;
}
.aside-new-btn {
  background: var(--chat-aside-btn-bg) !important;
  border: 1px solid var(--chat-aside-btn-border) !important;
  color: var(--chat-aside-accent) !important;
}
.aside-new-btn:hover:not(:disabled) {
  background: var(--chat-aside-btn-hover-bg) !important;
}
.aside-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.aside-session-scroll {
  flex: 1;
  overflow-y: auto;
  padding: 8px 10px 14px;
}
/* Agent 下拉切换器（header 内）*/
.aside-agent-dropdown {
  flex: 1;
  min-width: 0;
}
.aside-agent-trigger {
  display: flex;
  align-items: center;
  gap: 3px;
  cursor: pointer;
  border-radius: 6px;
  padding: 2px 4px 2px 0;
  transition: background 0.15s;
  max-width: 100%;
  overflow: hidden;
}
.aside-agent-trigger:hover {
  background: var(--el-fill-color-light, rgba(0,0,0,0.05));
}
.aside-trigger-arrow {
  font-size: 11px;
  opacity: 0.5;
  flex-shrink: 0;
  transition: transform 0.2s;
}
.aside-agent-trigger:hover .aside-trigger-arrow {
  opacity: 0.8;
}
/* 下拉菜单项样式 */
.agent-item-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--el-border-color);
  margin-right: 8px;
  flex-shrink: 0;
  transition: background 0.15s;
}
.agent-item-dot.active {
  background: var(--el-color-primary);
}
:deep(.agent-item--active) {
  color: var(--el-color-primary);
  font-weight: 500;
}
.aside-agent-card {
  margin: 4px 2px 10px;
  padding: 14px;
  border-radius: 14px;
  background: var(--chat-agent-card-bg);
  border: 1px solid var(--chat-agent-card-border);
  display: flex;
  align-items: center;
  gap: 12px;
}
.aside-empty-agent {
  margin: 4px 2px 12px;
  padding: 16px;
  text-align: center;
  font-size: 12px;
  color: var(--chat-empty-text);
  border-radius: 12px;
  border: 1px dashed var(--chat-empty-border);
}
.aside-conv-hint {
  margin: 8px 4px 0;
  padding: 12px 10px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--chat-conv-time);
  text-align: center;
  border-radius: 10px;
  background: var(--chat-conv-hover);
}
.aside-empty-agent p {
  margin: 0 0 8px;
}
.aside-empty-link {
  color: var(--chat-link);
  font-size: 12px;
  font-weight: 500;
}
.settings-link {
  margin-left: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 10px;
  color: var(--chat-link);
  background: var(--chat-settings-link-bg);
  transition: background 0.15s;
}
.settings-link:hover {
  background: var(--chat-settings-link-hover);
}
.agent-icon {
  width: 40px;
  height: 40px;
  border-radius: 12px;
  background: linear-gradient(135deg, #6366f1, #22d3ee);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  box-shadow: 0 8px 20px rgba(99, 102, 241, 0.35);
}
.agent-info {
  min-width: 0;
  flex: 1;
}
.agent-model {
  font-size: 11px;
  color: var(--chat-agent-model);
  margin-top: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.agent-model--solo {
  margin-top: 0;
  font-size: 12px;
  font-weight: 500;
}
.aside-divider {
  padding: 6px 6px 8px;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.12em;
  color: var(--chat-divider-label);
  text-transform: uppercase;
}
.conv-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 12px;
  cursor: pointer;
  margin-bottom: 4px;
  border: 1px solid transparent;
  transition: background 0.15s, border-color 0.15s;
}
.conv-item:hover {
  background: var(--chat-conv-hover);
}
.conv-item.active {
  background: var(--chat-conv-active-bg);
  border-color: var(--chat-conv-active-border);
}
.conv-icon {
  color: var(--chat-conv-icon);
  flex-shrink: 0;
}
.conv-item.active .conv-icon {
  color: var(--chat-conv-icon-active);
}
.conv-info {
  flex: 1;
  min-width: 0;
}
.conv-title {
  font-size: 13px;
  color: var(--chat-conv-title);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.conv-time {
  font-size: 11px;
  color: var(--chat-conv-time);
  margin-top: 2px;
}
.conv-delete {
  color: transparent;
  flex-shrink: 0;
  transition: color 0.15s;
}
.conv-item:hover .conv-delete {
  color: var(--chat-conv-del);
}
.conv-delete:hover {
  color: var(--chat-conv-del-hover) !important;
}

/* Main */
.chat-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: var(--chat-main-bg);
}
.chat-main-head {
  flex-shrink: 0;
  padding: 16px 24px 12px;
  border-bottom: 1px solid var(--chat-main-head-border);
  background: var(--chat-main-head-bg);
}
.chat-main-title {
  margin: 0;
  font-size: 20px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--chat-main-title-color);
}
.chat-main-sub {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--chat-main-sub-color);
}
.chat-main-sub.muted {
  color: var(--chat-main-sub-muted);
}

.messages-area {
  flex: 1;
  overflow-y: auto;
  padding: 8px 12px 20px;
}
.messages-inner {
  max-width: 820px;
  margin: 0 auto;
  width: 100%;
  padding: 0 8px;
}

/* Empty */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 280px;
  padding: 48px 24px;
  position: relative;
}
.empty-glow {
  position: absolute;
  width: 200px;
  height: 200px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(34, 211, 238, 0.2) 0%, transparent 70%);
  filter: blur(40px);
  pointer-events: none;
}
.empty-icon-wrap {
  position: relative;
  width: 80px;
  height: 80px;
  border-radius: 24px;
  background: linear-gradient(135deg, #e0f2fe, #ddd6fe);
  display: flex;
  align-items: center;
  justify-content: center;
  color: #0369a1;
  margin-bottom: 20px;
  box-shadow: 0 12px 40px rgba(14, 165, 233, 0.2);
}
.empty-title {
  font-size: 18px;
  font-weight: 600;
  color: #0f172a;
}
.empty-desc {
  margin-top: 8px;
  font-size: 14px;
  color: #64748b;
  text-align: center;
  max-width: 360px;
  line-height: 1.5;
}
.empty-hint {
  margin-top: 10px;
  font-size: 13px;
  color: #94a3b8;
}

/* Messages */
.msg-row {
  display: flex;
  gap: 14px;
  margin-bottom: 28px;
  animation: msg-in 0.35s cubic-bezier(0.22, 1, 0.36, 1);
}
@keyframes msg-in {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
.msg-row.user {
  flex-direction: row-reverse;
}
.msg-row.user .msg-body {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}
.msg-row.user .msg-meta {
  justify-content: flex-end;
}
.msg-row.user .msg-actions {
  justify-content: flex-end;
}
.msg-row.user .bubble-attachments {
  justify-content: flex-end;
}

.msg-avatar {
  width: 36px;
  height: 36px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: #fff;
}
.msg-avatar.user {
  background: linear-gradient(135deg, #0ea5e9, #6366f1);
  box-shadow: 0 6px 16px rgba(14, 165, 233, 0.35);
}
.msg-avatar.assistant {
  background: linear-gradient(135deg, #10b981, #14b8a6);
  box-shadow: 0 6px 16px rgba(16, 185, 129, 0.3);
}
.msg-body {
  flex: 1;
  min-width: 0;
  max-width: min(100%, 640px);
}
.msg-row.user .msg-body {
  align-items: flex-end;
}
.msg-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.msg-sender {
  font-size: 12px;
  font-weight: 600;
  color: #64748b;
}
.msg-tokens {
  font-size: 11px;
  color: #cbd5e1;
  font-variant-numeric: tabular-nums;
}

.msg-bubble {
  display: inline-block;
  max-width: 100%;
  padding: 14px 18px;
  border-radius: 18px 18px 18px 6px;
  font-size: 15px;
  line-height: 1.65;
  word-break: break-word;
  color: #1e293b;
  background: #fff;
  box-shadow: 0 1px 3px rgba(15, 23, 42, 0.06), 0 8px 24px rgba(15, 23, 42, 0.06);
  border: 1px solid rgba(15, 23, 42, 0.06);
}
.msg-row.user .msg-bubble {
  background: linear-gradient(135deg, #0ea5e9 0%, #2563eb 100%);
  color: #fff;
  border: none;
  border-radius: 18px 18px 6px 18px;
  box-shadow: 0 8px 24px rgba(37, 99, 235, 0.35);
}

/* ── Markdown 渲染样式 ── */
.msg-bubble :deep(p) { margin: 0 0 0.6em; }
.msg-bubble :deep(p:last-child) { margin-bottom: 0; }
.msg-bubble :deep(h1),
.msg-bubble :deep(h2),
.msg-bubble :deep(h3),
.msg-bubble :deep(h4),
.msg-bubble :deep(h5),
.msg-bubble :deep(h6) {
  margin: 1em 0 0.4em;
  line-height: 1.3;
  font-weight: 700;
  color: #0f172a;
}
.msg-bubble :deep(h1) { font-size: 1.4em; }
.msg-bubble :deep(h2) { font-size: 1.25em; }
.msg-bubble :deep(h3) { font-size: 1.1em; }
.msg-bubble :deep(h1:first-child),
.msg-bubble :deep(h2:first-child),
.msg-bubble :deep(h3:first-child) { margin-top: 0; }

.msg-bubble :deep(ul),
.msg-bubble :deep(ol) {
  margin: 0.4em 0;
  padding-left: 1.6em;
}
.msg-bubble :deep(li) { margin: 0.2em 0; }
.msg-bubble :deep(li > p) { margin: 0; }

.msg-bubble :deep(blockquote) {
  margin: 0.6em 0;
  padding: 0.4em 0.8em;
  border-left: 3px solid #94a3b8;
  color: #64748b;
  background: rgba(148, 163, 184, 0.08);
  border-radius: 0 6px 6px 0;
}
.msg-bubble :deep(blockquote p) { margin: 0; }

.msg-bubble :deep(hr) {
  border: none;
  border-top: 1px solid #e2e8f0;
  margin: 0.8em 0;
}

.msg-bubble :deep(a) {
  color: #2563eb;
  text-decoration: underline;
  text-underline-offset: 2px;
}
.msg-row.user .msg-bubble :deep(a) { color: #bfdbfe; }

.msg-bubble :deep(.inline-code) {
  background: rgba(99, 102, 241, 0.1);
  color: #6366f1;
  padding: 1px 5px;
  border-radius: 4px;
  font-size: 0.88em;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
}
.msg-row.user .msg-bubble :deep(.inline-code) {
  background: rgba(255, 255, 255, 0.2);
  color: #e0e7ff;
}

.msg-bubble :deep(.code-block) {
  margin: 0.6em -4px;
  border-radius: 10px;
  overflow: hidden;
  border: 1px solid #e2e8f0;
  background: #1e293b;
}
.msg-bubble :deep(.code-header) {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 6px 12px;
  background: #334155;
  font-size: 11px;
}
.msg-bubble :deep(.code-lang) {
  color: #94a3b8;
  font-weight: 600;
  letter-spacing: 0.03em;
}
.msg-bubble :deep(.code-copy) {
  background: none;
  border: 1px solid #475569;
  color: #94a3b8;
  font-size: 11px;
  padding: 2px 10px;
  border-radius: 5px;
  cursor: pointer;
  transition: all 0.15s;
}
.msg-bubble :deep(.code-copy:hover) {
  border-color: #0ea5e9;
  color: #0ea5e9;
}
.msg-bubble :deep(.code-block pre) {
  margin: 0;
  padding: 12px 14px;
  overflow-x: auto;
  font-size: 13px;
  line-height: 1.55;
}
.msg-bubble :deep(.code-block code) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
  color: #e2e8f0;
  background: none;
}

.msg-bubble :deep(table) {
  border-collapse: collapse;
  margin: 0.6em 0;
  width: 100%;
  font-size: 0.9em;
}
.msg-bubble :deep(th),
.msg-bubble :deep(td) {
  border: 1px solid #e2e8f0;
  padding: 6px 10px;
  text-align: left;
}
.msg-bubble :deep(th) {
  background: #f8fafc;
  font-weight: 600;
  color: #334155;
}
.msg-bubble :deep(tr:nth-child(even)) {
  background: #f8fafc;
}

.msg-bubble :deep(strong) { font-weight: 700; }
.msg-bubble :deep(em) { font-style: italic; }
.msg-bubble :deep(del) { text-decoration: line-through; color: #94a3b8; }

.msg-bubble :deep(img) {
  max-width: 100%;
  border-radius: 8px;
  margin: 0.4em 0;
}
.msg-bubble :deep(img.md-img),
.msg-bubble :deep(img.attach-img) {
  max-width: 320px;
  max-height: 240px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  cursor: pointer;
  transition: transform 0.15s;
}
.msg-bubble :deep(img.md-img:hover),
.msg-bubble :deep(img.attach-img:hover) {
  transform: scale(1.02);
}

/* highlight.js 语法色 */
.msg-bubble :deep(.hljs-keyword) { color: #c084fc; }
.msg-bubble :deep(.hljs-string) { color: #86efac; }
.msg-bubble :deep(.hljs-number) { color: #fbbf24; }
.msg-bubble :deep(.hljs-comment) { color: #64748b; font-style: italic; }
.msg-bubble :deep(.hljs-function) { color: #60a5fa; }
.msg-bubble :deep(.hljs-title) { color: #60a5fa; }
.msg-bubble :deep(.hljs-params) { color: #e2e8f0; }
.msg-bubble :deep(.hljs-built_in) { color: #38bdf8; }
.msg-bubble :deep(.hljs-literal) { color: #fb923c; }
.msg-bubble :deep(.hljs-type) { color: #34d399; }
.msg-bubble :deep(.hljs-attr) { color: #38bdf8; }
.msg-bubble :deep(.hljs-variable) { color: #f472b6; }
.msg-bubble :deep(.hljs-meta) { color: #94a3b8; }
.msg-bubble :deep(.hljs-punctuation) { color: #94a3b8; }
.msg-bubble :deep(.hljs-addition) { color: #86efac; background: rgba(134,239,172,0.1); }
.msg-bubble :deep(.hljs-deletion) { color: #fca5a5; background: rgba(252,165,165,0.1); }
.msg-bubble :deep(.hljs-selector-class) { color: #38bdf8; }
.msg-bubble :deep(.hljs-selector-tag) { color: #c084fc; }
.msg-bubble :deep(.hljs-property) { color: #38bdf8; }

.msg-actions {
  display: flex;
  gap: 6px;
  margin-top: 8px;
  opacity: 0;
  transition: opacity 0.2s;
}
.msg-row:hover .msg-actions {
  opacity: 1;
}
.action-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: none;
  background: rgba(15, 23, 42, 0.04);
  color: #64748b;
  font-size: 12px;
  padding: 4px 10px;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.15s;
}
.action-btn:hover {
  color: #0ea5e9;
  background: rgba(14, 165, 233, 0.1);
}
.action-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.bubble-attachments {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 10px;
}
.attach-img {
  max-width: 220px;
  max-height: 160px;
  border-radius: 12px;
  border: 1px solid #e2e8f0;
}
.attach-file {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  padding: 8px 14px;
  font-size: 12px;
  color: #2563eb;
  text-decoration: none;
}
.attach-file:hover {
  border-color: #93c5fd;
  background: #eff6ff;
}

/* Steps */
.steps-panel {
  margin-top: 12px;
  width: 100%;
}
.steps-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-size: 12px;
  color: var(--chat-step-toggle-text);
  padding: 6px 12px;
  border-radius: 8px;
  background: var(--chat-step-toggle-bg);
  user-select: none;
  border: 1px solid transparent;
}
.steps-toggle:hover {
  background: var(--chat-step-toggle-hover);
  color: var(--chat-step-toggle-text-hover);
}
.toggle-icon {
  transition: transform 0.25s;
  font-size: 12px;
}
.toggle-icon.open {
  transform: rotate(180deg);
}
.steps-list {
  margin-top: 10px;
}
.step-row {
  display: flex;
  gap: 12px;
}
.step-indicator {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 16px;
  flex-shrink: 0;
}
.step-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  margin-top: 6px;
}
.dot--llm_call {
  background: #2563eb;
}
.dot--tool_call {
  background: #ea580c;
}
.dot--sub_agent {
  background: #8b5cf6;
}
.dot--agent_call {
  background: #059669;
}
.dot--skill_match {
  background: #7c3aed;
}
.step-line {
  width: 2px;
  flex: 1;
  background: var(--chat-step-line);
  margin: 4px 0;
  min-height: 8px;
}
.step-row:last-child .step-line {
  display: none;
}
.step-body {
  flex: 1;
  padding-bottom: 14px;
  min-width: 0;
}
.step-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.step-badge {
  font-size: 10px;
  font-weight: 700;
  color: #fff;
  padding: 2px 8px;
  border-radius: 6px;
  letter-spacing: 0.03em;
}
.badge--llm_call {
  background: #2563eb;
}
.badge--tool_call {
  background: #ea580c;
}
.badge--sub_agent {
  background: #8b5cf6;
}
.badge--agent_call {
  background: #059669;
}
.badge--skill_match {
  background: #7c3aed;
}
.step-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--chat-step-title);
}
.step-tokens {
  font-size: 11px;
  color: var(--chat-step-muted);
}
.step-depth {
  font-size: 10px;
  font-weight: 700;
  color: #8b5cf6;
  background: rgba(139, 92, 246, 0.12);
  padding: 1px 5px;
  border-radius: 4px;
  letter-spacing: 0.04em;
}

.step-detail,
.wf-node-body {
  margin-top: 8px;
}
.detail-label {
  font-size: 11px;
  color: var(--chat-step-label);
  font-weight: 600;
  margin-bottom: 4px;
  margin-top: 8px;
}
.detail-label:first-child {
  margin-top: 0;
}
.detail-label--err {
  color: #ef4444;
}
.detail-code {
  background: var(--chat-step-code-bg);
  border: 1px solid var(--chat-step-code-border);
  border-radius: 6px;
  padding: 8px 10px;
  font-size: 12px;
  line-height: 1.5;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 220px;
  overflow-y: auto;
  margin: 0;
  color: var(--chat-step-code-text);
}
.detail-code--err {
  background: var(--chat-step-err-bg);
  border-color: var(--chat-step-err-border);
  color: var(--chat-step-err-text);
}
.detail-meta {
  display: flex;
  gap: 10px;
  margin-top: 8px;
  font-size: 11px;
  color: var(--chat-step-muted);
  flex-wrap: wrap;
}

/* Stream timeline：左侧细轨 + 无大圆角盒子，减少「套一层」观感 */
.wf-timeline {
  margin-bottom: 12px;
  padding: 4px 0 8px 12px;
  border-left: 2px solid var(--chat-wf-rail);
  background: transparent;
  border-radius: 0;
  border-top: none;
  border-right: none;
  border-bottom: none;
}
.wf-node {
  margin-bottom: 2px;
  border-radius: 0;
  overflow: visible;
}
.wf-node-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 8px 6px 0;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.12s;
}
.wf-node-head:hover {
  background: var(--chat-wf-row-hover);
}
.wf-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}
.wf-dot--llm_call {
  background: #2563eb;
}
.wf-dot--tool_call {
  background: #ea580c;
}
.wf-dot--sub_agent {
  background: #8b5cf6;
}
.wf-dot--agent_call {
  background: #059669;
}
.wf-dot--skill_match {
  background: #7c3aed;
}
.wf-dot--thinking {
  width: 20px;
  height: 20px;
  background: #94a3b8;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
}
.wf-label {
  font-size: 10px;
  font-weight: 700;
  color: var(--chat-step-label);
  letter-spacing: 0.06em;
  flex-shrink: 0;
}
.wf-name {
  flex: 1;
  font-size: 13px;
  font-weight: 600;
  color: var(--chat-step-title);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.wf-tokens {
  font-size: 11px;
  color: var(--chat-step-muted);
}
.wf-depth {
  font-size: 10px;
  font-weight: 700;
  color: #8b5cf6;
  background: rgba(139, 92, 246, 0.12);
  padding: 1px 5px;
  border-radius: 4px;
}
.wf-label--sub_agent {
  color: #8b5cf6;
}
.wf-arrow {
  color: var(--chat-step-muted);
  transition: transform 0.2s;
  flex-shrink: 0;
}
.wf-arrow.open {
  transform: rotate(90deg);
}
.wf-node-body {
  padding: 2px 0 10px 20px;
}
.wf-node--thinking {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
}
.wf-thinking-text {
  font-size: 13px;
  color: var(--chat-step-muted);
}

/* Input */
.input-area {
  flex-shrink: 0;
  padding: 16px 24px 20px;
  background: rgba(255, 255, 255, 0.85);
  backdrop-filter: blur(12px);
  border-top: 1px solid rgba(15, 23, 42, 0.08);
}
.input-area.disabled {
  opacity: 0.45;
  pointer-events: none;
}

.attach-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 12px;
}
.attach-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: #f1f5f9;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  padding: 6px 12px;
  font-size: 12px;
  color: #475569;
}
.chip--url {
  color: #2563eb;
}
.chip-close {
  cursor: pointer;
  color: #94a3b8;
}
.chip-close:hover {
  color: #ef4444;
}

.url-bar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 12px;
}
.url-input {
  flex: 1;
}

.composer {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  padding: 8px 10px 8px 6px;
  box-shadow: 0 4px 24px rgba(15, 23, 42, 0.06);
  transition: border-color 0.2s, box-shadow 0.2s;
}
.composer:focus-within {
  border-color: rgba(34, 211, 238, 0.5);
  box-shadow: 0 0 0 3px rgba(34, 211, 238, 0.12), 0 8px 32px rgba(15, 23, 42, 0.08);
}
.composer-tools {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  padding-bottom: 2px;
}
.tool-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 10px;
  border: none;
  background: transparent;
  color: #64748b;
  cursor: pointer;
  transition: all 0.15s;
}
.tool-btn:hover:not(.off) {
  color: #0ea5e9;
  background: #f0f9ff;
}
.tool-btn.active {
  color: #0ea5e9;
  background: #e0f2fe;
}
.tool-btn.off {
  color: #cbd5e1;
  cursor: not-allowed;
}
.composer-input {
  flex: 1;
  min-width: 0;
}
.composer-input :deep(.el-textarea__inner) {
  background: transparent !important;
  border: none !important;
  box-shadow: none !important;
  padding: 8px 4px;
  font-size: 15px;
  line-height: 1.5;
}

.send-btn {
  width: 44px;
  height: 44px;
  border-radius: 14px;
  border: none;
  background: #e2e8f0;
  color: #cbd5e1;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: not-allowed;
  transition: all 0.2s;
  flex-shrink: 0;
  font-size: 18px;
}
.send-btn.ready {
  background: linear-gradient(135deg, #22d3ee, #0ea5e9);
  color: #fff;
  cursor: pointer;
  box-shadow: 0 8px 24px rgba(14, 165, 233, 0.4);
}
.send-btn.ready:hover {
  filter: brightness(1.05);
  transform: translateY(-1px);
}
.stop-btn {
  width: 44px;
  height: 44px;
  border-radius: 14px;
  border: none;
  background: linear-gradient(135deg, #f87171, #ef4444);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  box-shadow: 0 8px 24px rgba(239, 68, 68, 0.35);
}
.stop-btn:hover {
  filter: brightness(1.05);
}
.stop-square {
  width: 14px;
  height: 14px;
  background: #fff;
  border-radius: 3px;
}

.fold-enter-active,
.fold-leave-active {
  transition: all 0.25s ease;
  max-height: 2000px;
  overflow: hidden;
}
.fold-enter-from,
.fold-leave-to {
  max-height: 0;
  opacity: 0;
}

.typing-cursor {
  display: inline-block;
  width: 2px;
  height: 18px;
  background: #0ea5e9;
  margin-left: 3px;
  vertical-align: text-bottom;
  animation: blink 0.85s infinite;
}
@keyframes blink {
  0%,
  50% {
    opacity: 1;
  }
  51%,
  100% {
    opacity: 0;
  }
}

.messages-area::-webkit-scrollbar,
.aside-session-scroll::-webkit-scrollbar {
  width: 5px;
}
.messages-area::-webkit-scrollbar-thumb,
.aside-session-scroll::-webkit-scrollbar-thumb {
  background: var(--chat-scrollbar-thumb);
  border-radius: 6px;
}
.messages-area::-webkit-scrollbar-track,
.aside-session-scroll::-webkit-scrollbar-track {
  background: transparent;
}
</style>
