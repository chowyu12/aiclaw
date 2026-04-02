<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">执行日志</h1>
      <p class="aic-sub">按会话查看对话与执行步骤；工具消息与「仅发起工具调用、无正文」的中间轮次 Agent 不在时间线展示，详情见最终回复下的「执行步骤」。</p>
    </div>
    <div class="aic-page-body">
    <el-card class="aic-card" shadow="never">
      <template #header>
        <div class="aic-card-header">
          <span class="aic-card-title">会话记录</span>
          <div class="filter-bar">
            <el-checkbox v-model="includeChannels" @change="loadData">包含渠道会话</el-checkbox>
            <el-input v-model="filterUserId" placeholder="用户 ID" clearable style="width: 150px;" @clear="loadData" @keyup.enter="loadData" />
            <el-input v-model="filterUserPrefix" placeholder="用户前缀（如 channel:xxx:）" clearable style="width: 240px;" @clear="loadData" @keyup.enter="loadData" />
            <el-button @click="loadData">
              <el-icon><Search /></el-icon> 查询
            </el-button>
          </div>
        </div>
      </template>

      <el-table
        :data="conversations"
        v-loading="loading"
        stripe
        row-key="id"
        :expand-row-keys="expandedRows"
        @expand-change="onExpandChange"
      >
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="expand-content" v-loading="row._loading">
              <div v-if="!row._messages || row._messages.length === 0" class="empty-msg">暂无消息记录</div>
              <div v-else-if="timelineMessages(row).length === 0" class="empty-msg">暂无对话消息（工具结果请在 Agent 消息的「执行步骤」中查看）</div>
              <div v-else class="message-timeline">
                <div v-for="msg in timelineMessages(row)" :key="msg.id" class="msg-item">
                  <div class="msg-header">
                    <el-tag :type="msg.role === 'user' ? '' : msg.role === 'assistant' ? 'success' : 'info'" size="small" effect="dark">
                      {{ roleLabel(msg.role) }}
                    </el-tag>
                    <span class="msg-time">{{ formatTime(msg.created_at) }}</span>
                  </div>
                  <div v-if="(msg.content ?? '').trim()" class="msg-body">
                    <pre class="msg-content">{{ truncate(msg.content, 800) }}</pre>
                  </div>

                  <div v-if="msg.steps && msg.steps.length > 0" class="steps-section">
                    <div
                      class="steps-header"
                      @click="msg._showSteps = !msg._showSteps"
                    >
                      <el-icon size="14"><Operation /></el-icon>
                      <span>执行步骤 ({{ msg.steps.length }})</span>
                      <span class="steps-summary">
                        总耗时 {{ totalDuration(msg.steps) }}ms
                      </span>
                      <el-icon class="arrow" :class="{ expanded: msg._showSteps }"><ArrowDown /></el-icon>
                    </div>
                    <transition name="slide">
                      <div v-if="msg._showSteps" class="steps-body">
                        <el-timeline>
                          <el-timeline-item
                            v-for="node in groupSteps(msg.steps || [])"
                            :key="node.step.id"
                            :type="node.step.status === 'success' ? 'success' : 'danger'"
                            :timestamp="`${node.step.duration_ms}ms`"
                            placement="top"
                          >
                            <div class="step-card" :class="{ 'step-card--subagent': node.step.name === 'sub_agent' }">
                              <div class="step-title-row">
                                <el-tag :type="stepTagType(node.step.step_type, node.step.name)" size="small" effect="dark">
                                  {{ stepTypeLabel(node.step.step_type, node.step.name) }}
                                </el-tag>
                                <el-tag v-if="node.step.sub_agent_depth" size="small" effect="plain" class="depth-tag">
                                  L{{ node.step.sub_agent_depth }}
                                </el-tag>
                                <span class="step-name">{{ node.step.name }}</span>
                                <el-tag
                                  :type="node.step.status === 'success' ? 'success' : 'danger'"
                                  size="small" round
                                >{{ node.step.status }}</el-tag>
                                <el-tag
                                  v-if="node.children.length"
                                  size="small" effect="plain" class="depth-tag depth-tag--toggle"
                                  @click.stop="node.step._childrenOpen = node.step._childrenOpen === false ? true : false"
                                >
                                  {{ node.children.length }} 步
                                  <el-icon class="depth-tag-arrow" :class="{ open: node.step._childrenOpen !== false }"><ArrowRight /></el-icon>
                                </el-tag>
                              </div>

                              <div v-if="node.step.input" class="step-block">
                                <div class="step-block-label">输入</div>
                                <pre class="step-block-code">{{ truncate(node.step.input, 1000) }}</pre>
                              </div>
                              <div v-if="node.step.output" class="step-block">
                                <div class="step-block-label">输出</div>
                                <pre class="step-block-code">{{ truncate(node.step.output, 1000) }}</pre>
                              </div>
                              <div v-if="node.step.error" class="step-block">
                                <div class="step-block-label error-label">错误</div>
                                <pre class="step-block-code error-code">{{ node.step.error }}</pre>
                              </div>

                              <div v-if="node.step.metadata" class="step-meta-row">
                                <span v-if="node.step.metadata.channel_type">
                                  <el-icon size="12"><ChatDotRound /></el-icon>
                                  渠道 {{ node.step.metadata.channel_type }}<template v-if="node.step.metadata.channel_id"> #{{ node.step.metadata.channel_id }}</template>
                                  <template v-if="node.step.metadata.channel_thread_key"> · {{ node.step.metadata.channel_thread_key }}</template>
                                  <template v-if="node.step.metadata.channel_sender_id"> · {{ node.step.metadata.channel_sender_id }}</template>
                                </span>
                                <span v-if="node.step.metadata.provider">
                                  <el-icon size="12"><Connection /></el-icon> {{ node.step.metadata.provider }}
                                </span>
                                <span v-if="node.step.metadata.model">
                                  <el-icon size="12"><Cpu /></el-icon> {{ node.step.metadata.model }}
                                </span>
                                <span v-if="node.step.metadata.temperature !== undefined">
                                  Temp: {{ node.step.metadata.temperature }}
                                </span>
                                <span v-if="node.step.metadata.tool_name">
                                  Tool: {{ node.step.metadata.tool_name }}
                                </span>
                                <span v-if="node.step.metadata.skill_name">
                                  Skill: {{ node.step.metadata.skill_name }}
                                </span>
                                <span v-if="node.step.metadata.skill_tools?.length">
                                  Skill Tools: {{ node.step.metadata.skill_tools.join(', ') }}
                                </span>
                              </div>

                              <!-- sub_agent 内部步骤（可折叠） -->
                              <transition name="fold">
                              <div v-if="node.children.length && node.step._childrenOpen !== false" class="sub-agent-children">
                                <div v-for="child in node.children" :key="child.step.id" class="child-step-card">
                                  <div class="step-title-row">
                                    <el-tag :type="stepTagType(child.step.step_type, child.step.name)" size="small" effect="dark">
                                      {{ stepTypeLabel(child.step.step_type, child.step.name) }}
                                    </el-tag>
                                    <span class="step-name">{{ child.step.name }}</span>
                                    <el-tag :type="child.step.status === 'success' ? 'success' : 'danger'" size="small" round>{{ child.step.status }}</el-tag>
                                    <span class="child-duration">{{ child.step.duration_ms }}ms</span>
                                    <span v-if="child.step.tokens_used" class="child-tokens">{{ child.step.tokens_used }} tokens</span>
                                  </div>
                                  <div v-if="child.step.input" class="step-block">
                                    <div class="step-block-label">输入</div>
                                    <pre class="step-block-code">{{ truncate(child.step.input, 500) }}</pre>
                                  </div>
                                  <div v-if="child.step.output" class="step-block">
                                    <div class="step-block-label">输出</div>
                                    <pre class="step-block-code">{{ truncate(child.step.output, 500) }}</pre>
                                  </div>
                                  <div v-if="child.step.error" class="step-block">
                                    <div class="step-block-label error-label">错误</div>
                                    <pre class="step-block-code error-code">{{ child.step.error }}</pre>
                                  </div>
                                </div>
                              </div>
                              </transition>
                            </div>
                          </el-timeline-item>
                        </el-timeline>
                      </div>
                    </transition>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column label="Agent" min-width="120">
          <template #default>
            {{ defaultAgent?.name || '—' }}
          </template>
        </el-table-column>
        <el-table-column label="来源" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.user_id?.startsWith('channel:')" type="success" size="small" effect="plain">渠道</el-tag>
            <el-tag v-else size="small" effect="plain">Web</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="user_id" label="用户" width="160" show-overflow-tooltip />
        <el-table-column prop="title" label="标题" min-width="150" show-overflow-tooltip />
        <el-table-column prop="uuid" label="会话 UUID" width="140" show-overflow-tooltip />
        <el-table-column label="更新时间" width="180">
          <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="180">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-popconfirm title="确定删除此会话及全部记录？" @confirm="handleDelete(row.id)">
              <template #reference>
                <el-button link type="danger" size="small">删除</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        v-model:current-page="page" v-model:page-size="pageSize"
        :total="total" :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next" style="margin-top: 16px; justify-content: flex-end;"
        @size-change="loadData" @current-change="loadData"
      />
    </el-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { chatApi, type Conversation, type Message, type ExecutionStep, type StepNode } from '../../api/chat'
import { agentApi, type Agent } from '../../api/agent'
import { useRoute } from 'vue-router'

interface ConvRow extends Conversation {
  _loading?: boolean
  _messages?: MsgRow[]
}
interface MsgRow extends Message {
  _showSteps?: boolean
}

const conversations = ref<ConvRow[]>([])
const defaultAgent = ref<Agent | null>(null)
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const filterUserId = ref('')
const filterUserPrefix = ref('')
const includeChannels = ref(true)
const expandedRows = ref<number[]>([])
const route = useRoute()

onMounted(async () => {
  try {
    const res = await agentApi.list({ page: 1, page_size: 100 })
    const list = res.data?.list ?? []
    defaultAgent.value = list.find((a) => a.is_default) ?? list[0] ?? null
  } catch {
    defaultAgent.value = null
  }
  if (typeof route.query.user_id === 'string') {
    filterUserId.value = route.query.user_id
  }
  if (typeof route.query.user_prefix === 'string') {
    filterUserPrefix.value = route.query.user_prefix
  }
  loadData()
})

async function loadData() {
  loading.value = true
  expandedRows.value = []
  try {
    const params: any = { page: page.value, page_size: pageSize.value }
    if (filterUserId.value) params.user_id = filterUserId.value
    if (filterUserPrefix.value) params.user_prefix = filterUserPrefix.value
    if (includeChannels.value && !filterUserId.value && !filterUserPrefix.value) params.include_channels = 'true'
    const res: any = await chatApi.conversations(params)
    conversations.value = (res.data?.list || []).map((c: Conversation) => reactive({ ...c, _loading: false, _messages: undefined }))
    total.value = res.data?.total || 0
  } finally {
    loading.value = false
  }
}

async function onExpandChange(row: ConvRow, expanded: ConvRow[]) {
  const isExpanded = expanded.some(r => r.id === row.id)
  if (isExpanded && !row._messages) {
    row._loading = true
    try {
      const res: any = await chatApi.messages(row.id, 100, true)
      const msgs: Message[] = res.data || []
      row._messages = msgs.map(m => reactive({ ...m, _showSteps: false }))
    } catch {
      row._messages = []
    } finally {
      row._loading = false
    }
  }
}

async function handleDelete(id: number) {
  try {
    await chatApi.deleteConversation(id)
    ElMessage.success('删除成功')
    loadData()
  } catch {
    ElMessage.error('删除失败')
  }
}

/**
 * 时间线展示规则：
 * - 不展示 role=tool（工具输出在执行步骤里）
 * - 不展示无正文的 assistant（多轮工具调用时中间几轮只有 tool_calls、无 content，步骤挂在最终那条 assistant 上）
 */
function timelineMessages(row: ConvRow): MsgRow[] {
  if (!row._messages?.length) return []
  return row._messages.filter(m => {
    if (m.role === 'tool') return false
    if (m.role === 'assistant' && !(m.content ?? '').trim()) {
      // 若该条已单独挂了执行步骤（例如以后按轮次落库），仍展示为「仅步骤」卡片
      return !!(m.steps && m.steps.length > 0)
    }
    return true
  })
}

function roleLabel(role: string) {
  switch (role) {
    case 'user': return '用户'
    case 'assistant': return 'Agent'
    case 'system': return '系统'
    case 'tool': return '工具'
    default: return role
  }
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

function stepTagType(t: string, name?: string): '' | 'success' | 'warning' | 'danger' | 'info' {
  if (t === 'tool_call' && name === 'sub_agent') return 'success'
  switch (t) {
    case 'llm_call': return ''
    case 'tool_call': return 'warning'
    case 'agent_call': return 'success'
    case 'skill_match': return 'info'
    default: return 'info'
  }
}

function totalDuration(steps: ExecutionStep[]) {
  return steps.reduce((sum, s) => sum + s.duration_ms, 0)
}

function groupSteps(steps: ExecutionStep[]): StepNode[] {
  // Phase 1: group children by sub_agent_call_id (order-independent)
  const childrenByCall = new Map<string, ExecutionStep[]>()
  const parentCalls = new Map<string, ExecutionStep>()

  for (const s of steps) {
    const cid = s.sub_agent_call_id
    if (!cid) continue
    if (s.name === 'sub_agent' && s.step_type === 'tool_call') {
      parentCalls.set(cid, s)
    } else {
      if (!childrenByCall.has(cid)) childrenByCall.set(cid, [])
      childrenByCall.get(cid)!.push(s)
    }
  }

  const consumed = new Set<number>()
  for (const [cid] of parentCalls) {
    for (const c of childrenByCall.get(cid) || []) consumed.add(c.step_order)
  }

  // Phase 2: build result, grouping matched children under parents
  const result: StepNode[] = []
  for (const s of steps) {
    if (consumed.has(s.step_order)) continue
    const cid = s.sub_agent_call_id
    if (s.name === 'sub_agent' && s.step_type === 'tool_call' && cid && parentCalls.has(cid)) {
      const children = (childrenByCall.get(cid) || []).map(c => ({ step: c, children: [] as StepNode[] }))
      result.push({ step: s, children })
    } else {
      result.push({ step: s, children: [] })
    }
  }
  return result
}

function truncate(text: string, maxLen: number) {
  if (!text) return ''
  if (text.length <= maxLen) return text
  return text.slice(0, maxLen) + '...[truncated]'
}

function formatTime(t: string) {
  if (!t) return ''
  return new Date(t).toLocaleString('zh-CN', { hour12: false })
}
</script>

<style scoped>
.filter-bar {
  display: flex;
  gap: 8px;
  align-items: center;
}

.expand-content {
  padding: 12px 20px;
}
.empty-msg {
  text-align: center;
  color: var(--el-text-color-secondary);
  padding: 20px;
}

.message-timeline {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.msg-item {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  overflow: hidden;
}
.msg-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  background: var(--el-fill-color-lighter);
  border-bottom: 1px solid var(--el-border-color-extra-light);
}
.msg-time {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-left: auto;
}
.msg-body {
  padding: 10px 14px;
}
.msg-content {
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  color: var(--el-text-color-primary);
  max-height: 300px;
  overflow-y: auto;
}

.steps-section {
  border-top: 1px solid var(--el-border-color-extra-light);
}
.steps-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  cursor: pointer;
  font-size: 13px;
  color: var(--el-text-color-regular);
  transition: background-color 0.15s;
}
.steps-header:hover {
  background: var(--el-fill-color-light);
}
.steps-summary {
  margin-left: auto;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.arrow {
  transition: transform 0.2s;
  margin-left: 4px;
}
.arrow.expanded {
  transform: rotate(180deg);
}
.steps-body {
  padding: 16px 20px 8px;
  background: var(--el-fill-color-lighter);
}

.step-card {
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
  padding: 12px 14px;
}
.step-card--subagent {
  border-left: 3px solid #8b5cf6;
}
.depth-tag {
  font-weight: 700;
  color: #8b5cf6 !important;
  border-color: rgba(139, 92, 246, 0.3) !important;
}
.depth-tag--toggle {
  cursor: pointer;
  user-select: none;
}
.depth-tag--toggle:hover {
  background: rgba(139, 92, 246, 0.15) !important;
}
.depth-tag-arrow {
  font-size: 10px;
  margin-left: 2px;
  transition: transform 0.2s ease;
}
.depth-tag-arrow.open {
  transform: rotate(90deg);
}
.fold-enter-active, .fold-leave-active {
  transition: all 0.2s ease;
  overflow: hidden;
}
.fold-enter-from, .fold-leave-to {
  opacity: 0;
  max-height: 0;
}
.fold-enter-to, .fold-leave-from {
  opacity: 1;
  max-height: 2000px;
}
.step-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.step-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--el-text-color-primary);
}

.step-block {
  margin-bottom: 8px;
}
.step-block-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--el-text-color-secondary);
  margin-bottom: 2px;
}
.error-label {
  color: var(--el-color-danger);
}
.step-block-code {
  background: var(--el-fill-color-light);
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
  padding: 8px 10px;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 200px;
  overflow-y: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-primary);
}
.error-code {
  background: var(--el-color-danger-light-9);
  border-color: var(--el-color-danger-light-8);
  color: var(--el-color-danger);
}

.step-meta-row {
  display: flex;
  gap: 16px;
  font-size: 11px;
  color: var(--el-text-color-secondary);
  margin-top: 4px;
}
.step-meta-row span {
  display: flex;
  align-items: center;
  gap: 3px;
}

.sub-agent-children {
  margin-top: 10px;
  padding-left: 12px;
  border-left: 2px solid rgba(139, 92, 246, 0.3);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.child-step-card {
  background: var(--el-fill-color-lighter);
  border: 1px solid var(--el-border-color-extra-light);
  border-radius: 6px;
  padding: 10px 12px;
}
.child-duration {
  font-size: 11px;
  color: var(--el-text-color-secondary);
}
.child-tokens {
  font-size: 11px;
  color: var(--el-text-color-secondary);
}

.slide-enter-active, .slide-leave-active {
  transition: all 0.25s ease;
  max-height: 3000px;
  overflow: hidden;
}
.slide-enter-from, .slide-leave-to {
  max-height: 0;
  opacity: 0;
}
</style>
