<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">{{ i18n.t('logs.title') }}</h1>
      <p class="aic-sub">{{ i18n.t('logs.subtitle') }}</p>
    </div>
    <div class="aic-page-body">
    <el-card class="aic-card" shadow="never">
      <template #header>
        <div class="aic-card-header">
          <span class="aic-card-title">{{ i18n.t('logs.conversations') }}</span>
          <div class="filter-bar">
            <el-checkbox v-model="includeChannels" @change="loadData">{{ i18n.t('logs.includeChannels') }}</el-checkbox>
            <el-input v-model="filterUserId" :placeholder="i18n.t('logs.userId')" clearable style="width: 150px;" @clear="loadData" @keyup.enter="loadData" />
            <el-input v-model="filterUserPrefix" :placeholder="i18n.t('logs.userPrefix')" clearable style="width: 240px;" @clear="loadData" @keyup.enter="loadData" />
            <el-button @click="loadData">
              <el-icon><Search /></el-icon> {{ i18n.t('common.search') }}
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
              <div v-if="!row._messages || row._messages.length === 0" class="empty-msg">{{ i18n.t('logs.noMessages') }}</div>
              <div v-else-if="timelineMessages(row).length === 0" class="empty-msg">{{ i18n.t('logs.noTimelineMessages') }}</div>
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

                  <div v-if="msg.role === 'assistant' && msg.plan?.items?.length" class="plan-section">
                    <div class="plan-header">
                      <div>
                        <div class="plan-label">{{ i18n.t('chat.plan') }}</div>
                        <div class="plan-goal">{{ msg.plan.goal || i18n.t('chat.defaultPlanGoal') }}</div>
                      </div>
                      <span class="plan-progress">{{ planProgress(msg.plan) }}</span>
                    </div>
                    <div v-if="msg.plan.revision_reason" class="plan-reason">{{ msg.plan.revision_reason }}</div>
                    <div class="plan-list">
                      <div v-for="item in msg.plan.items" :key="item.id" class="plan-row" :class="'plan-row--' + item.status">
                        <span class="plan-dot" />
                        <span class="plan-title">{{ item.title }}</span>
                        <span class="plan-status">{{ planItemStatusLabel(item.status) }}</span>
                      </div>
                    </div>
                  </div>

                  <div v-if="msg.role === 'assistant' && msg.memory?.items?.length" class="memory-section">
                    <div class="memory-label"><el-icon size="14"><Collection /></el-icon>{{ i18n.t('chat.memoryUsed', { count: msg.memory.items.length }) }}</div>
                    <div v-for="memory in msg.memory.items" :key="memory.uuid" class="memory-row">
                      <span>{{ memoryKindLabel(memory.kind) }}</span><span>{{ memory.summary || memory.content }}</span>
                    </div>
                  </div>

                  <div v-if="msg.steps && msg.steps.length > 0" class="steps-section">
                    <div
                      class="steps-header"
                      @click="msg._showSteps = !msg._showSteps"
                    >
                      <el-icon size="14"><Operation /></el-icon>
                      <span>{{ i18n.t('chat.executionSteps', { count: msg.steps.length }) }}</span>
                      <span class="steps-summary">
                        {{ i18n.t('chat.totalDuration', { duration: totalDuration(msg.steps) }) }}
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
                                <span class="step-name">{{ stepDisplayName(node.step) }}</span>
                                <el-tag
                                  :type="node.step.status === 'success' ? 'success' : 'danger'"
                                  size="small" round
                                >{{ node.step.status }}</el-tag>
                                <el-tag
                                  v-if="node.children.length"
                                  size="small" effect="plain" class="depth-tag depth-tag--toggle"
                                  @click.stop="node.step._childrenOpen = node.step._childrenOpen === false ? true : false"
                                >
                                  {{ i18n.t('chat.childSteps', { count: node.children.length }) }}
                                  <el-icon class="depth-tag-arrow" :class="{ open: node.step._childrenOpen !== false }"><ArrowRight /></el-icon>
                                </el-tag>
                              </div>

                              <div v-if="node.step.input" class="step-block">
                                <div class="step-block-label">{{ i18n.t('chat.inputLabel') }}</div>
                                <pre class="step-block-code">{{ truncate(node.step.input, 1000) }}</pre>
                              </div>
                              <div v-if="node.step.output" class="step-block">
                                <div class="step-block-label">{{ i18n.t('chat.outputLabel') }}</div>
                                <pre class="step-block-code">{{ truncate(node.step.output, 1000) }}</pre>
                              </div>
                              <div v-if="node.step.error" class="step-block">
                                <div class="step-block-label error-label">{{ i18n.t('chat.errorLabel') }}</div>
                                <pre class="step-block-code error-code">{{ node.step.error }}</pre>
                              </div>

                              <div v-if="node.step.metadata" class="step-meta-row">
                                <span v-if="node.step.metadata.channel_type">
                                  <el-icon size="12"><ChatDotRound /></el-icon>
                                  {{ i18n.t('chat.channelLabel', { type: node.step.metadata.channel_type }) }}<template v-if="node.step.metadata.channel_id"> #{{ node.step.metadata.channel_id }}</template>
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
                                    <span class="step-name">{{ stepDisplayName(child.step) }}</span>
                                    <el-tag :type="child.step.status === 'success' ? 'success' : 'danger'" size="small" round>{{ child.step.status }}</el-tag>
                                    <span class="child-duration">{{ child.step.duration_ms }}ms</span>
                                    <span v-if="child.step.tokens_used" class="child-tokens">{{ child.step.tokens_used }} tokens</span>
                                  </div>
                                  <div v-if="child.step.input" class="step-block">
                                    <div class="step-block-label">{{ i18n.t('chat.inputLabel') }}</div>
                                    <pre class="step-block-code">{{ truncate(child.step.input, 500) }}</pre>
                                  </div>
                                  <div v-if="child.step.output" class="step-block">
                                    <div class="step-block-label">{{ i18n.t('chat.outputLabel') }}</div>
                                    <pre class="step-block-code">{{ truncate(child.step.output, 500) }}</pre>
                                  </div>
                                  <div v-if="child.step.error" class="step-block">
                                    <div class="step-block-label error-label">{{ i18n.t('chat.errorLabel') }}</div>
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
        <el-table-column :label="i18n.t('logs.source')" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.user_id?.startsWith('channel:')" type="success" size="small" effect="plain">{{ i18n.t('logs.channel') }}</el-tag>
            <el-tag v-else size="small" effect="plain">Web</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="user_id" :label="i18n.t('logs.user')" width="160" show-overflow-tooltip />
        <el-table-column prop="title" :label="i18n.t('logs.titleColumn')" min-width="150" show-overflow-tooltip />
        <el-table-column prop="uuid" :label="i18n.t('logs.conversationUUID')" width="140" show-overflow-tooltip />
        <el-table-column :label="i18n.t('common.updatedAt')" width="180">
          <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.createdAt')" width="180">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.actions')" width="100" fixed="right">
          <template #default="{ row }">
            <el-popconfirm :title="i18n.t('logs.deleteConversationConfirm')" @confirm="handleDelete(row.id)">
              <template #reference>
                <el-button link type="danger" size="small">{{ i18n.t('common.delete') }}</el-button>
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
import { useI18nStore } from '../../stores/i18n'

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
const i18n = useI18nStore()

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
    ElMessage.success(i18n.t('common.deleteSuccess'))
    loadData()
  } catch {
    ElMessage.error(i18n.t('common.deleteFailed'))
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
    case 'user': return i18n.t('chat.user')
    case 'assistant': return 'Agent'
    case 'system': return 'System'
    case 'tool': return i18n.t('app.tools')
    default: return role
  }
}

function stepTypeLabel(t: string, name?: string) {
  if (t === 'tool_call' && name === 'web_search') return i18n.t('chat.webSearch')
  if (t === 'tool_call' && name === 'sub_agent') return 'Sub Agent'
  switch (t) {
    case 'llm_call': return 'LLM'
    case 'tool_call': return 'Tool'
    case 'agent_call': return 'Agent'
    case 'skill_match': return 'Skill'
    default: return t
  }
}

function stepDisplayName(step: ExecutionStep) {
  if (step.step_type === 'tool_call' && step.name === 'web_search') return i18n.t('chat.webSearch')
  return step.name
}

function stepTagType(t: string, name?: string): '' | 'success' | 'warning' | 'danger' | 'info' {
  if (t === 'tool_call' && name === 'web_search') return 'info'
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

function planItemStatusLabel(status: string): string {
  switch (status) {
    case 'pending': return i18n.t('plan.pending')
    case 'running': return i18n.t('plan.running')
    case 'completed': return i18n.t('plan.completed')
    case 'blocked': return i18n.t('plan.blocked')
    case 'failed': return i18n.t('plan.failed')
    case 'skipped': return i18n.t('plan.skipped')
    default: return status
  }
}

function planProgress(plan?: Message['plan']): string {
  const items = plan?.items || []
  if (!items.length) return ''
  const done = items.filter(i => i.status === 'completed' || i.status === 'skipped').length
  return `${done}/${items.length}`
}

function memoryKindLabel(kind: string): string {
  return i18n.t(`memories.kind.${kind}`)
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

.plan-section {
  border-top: 1px solid var(--el-border-color-extra-light);
  padding: 10px 14px 12px;
  background: #f8fafc;
}
.memory-section {
  margin-top: 12px;
  padding: 10px 12px;
  border-left: 2px solid #5eead4;
  background: #f0fdfa;
}
.memory-label { display: flex; align-items: center; gap: 6px; color: #0f766e; font-size: 12px; font-weight: 700; }
.memory-row { display: flex; gap: 8px; margin-top: 6px; color: #475569; font-size: 12px; line-height: 1.45; }
.memory-row > span:first-child { flex: 0 0 auto; color: #0f766e; font-weight: 600; }
.plan-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.plan-label {
  font-size: 11px;
  font-weight: 700;
  color: #0f766e;
}
.plan-goal {
  margin-top: 2px;
  font-size: 13px;
  font-weight: 700;
  line-height: 1.4;
  color: #0f172a;
}
.plan-progress {
  flex-shrink: 0;
  font-size: 12px;
  font-weight: 700;
  color: #0f766e;
  font-variant-numeric: tabular-nums;
}
.plan-reason {
  margin-top: 6px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.plan-list {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.plan-row {
  display: grid;
  grid-template-columns: 9px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
.plan-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #94a3b8;
}
.plan-row--running .plan-dot {
  background: #0ea5e9;
  box-shadow: 0 0 0 4px rgba(14, 165, 233, 0.12);
}
.plan-row--completed .plan-dot {
  background: #10b981;
}
.plan-row--blocked .plan-dot,
.plan-row--failed .plan-dot {
  background: #ef4444;
}
.plan-row--skipped .plan-dot {
  background: #cbd5e1;
}
.plan-title {
  min-width: 0;
  color: #1e293b;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.plan-status {
  font-size: 11px;
  font-weight: 700;
  color: #64748b;
}
.plan-row--running .plan-status {
  color: #0284c7;
}
.plan-row--completed .plan-status {
  color: #059669;
}
.plan-row--blocked .plan-status,
.plan-row--failed .plan-status {
  color: #dc2626;
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
