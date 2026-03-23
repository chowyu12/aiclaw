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
            <el-input v-model="filterUserId" placeholder="用户 ID" clearable style="width: 150px;" @clear="loadData" @keyup.enter="loadData" />
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
                            v-for="step in msg.steps"
                            :key="step.id"
                            :type="step.status === 'success' ? 'success' : 'danger'"
                            :timestamp="`${step.duration_ms}ms`"
                            placement="top"
                          >
                            <div class="step-card">
                              <div class="step-title-row">
                                <el-tag :type="stepTagType(step.step_type)" size="small" effect="dark">
                                  {{ stepTypeLabel(step.step_type) }}
                                </el-tag>
                                <span class="step-name">{{ step.name }}</span>
                                <el-tag
                                  :type="step.status === 'success' ? 'success' : 'danger'"
                                  size="small" round
                                >{{ step.status }}</el-tag>
                              </div>

                              <div v-if="step.input" class="step-block">
                                <div class="step-block-label">输入</div>
                                <pre class="step-block-code">{{ truncate(step.input, 1000) }}</pre>
                              </div>
                              <div v-if="step.output" class="step-block">
                                <div class="step-block-label">输出</div>
                                <pre class="step-block-code">{{ truncate(step.output, 1000) }}</pre>
                              </div>
                              <div v-if="step.error" class="step-block">
                                <div class="step-block-label error-label">错误</div>
                                <pre class="step-block-code error-code">{{ step.error }}</pre>
                              </div>

                              <div v-if="step.metadata" class="step-meta-row">
                                <span v-if="step.metadata.provider">
                                  <el-icon size="12"><Connection /></el-icon> {{ step.metadata.provider }}
                                </span>
                                <span v-if="step.metadata.model">
                                  <el-icon size="12"><Cpu /></el-icon> {{ step.metadata.model }}
                                </span>
                                <span v-if="step.metadata.temperature !== undefined">
                                  Temp: {{ step.metadata.temperature }}
                                </span>
                                <span v-if="step.metadata.tool_name">
                                  Tool: {{ step.metadata.tool_name }}
                                </span>
                                <span v-if="step.metadata.skill_name">
                                  Skill: {{ step.metadata.skill_name }}
                                </span>
                                <span v-if="step.metadata.skill_tools?.length">
                                  Skill Tools: {{ step.metadata.skill_tools.join(', ') }}
                                </span>
                              </div>
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
        <el-table-column prop="user_id" label="用户" width="120" show-overflow-tooltip />
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
import { chatApi, type Conversation, type Message, type ExecutionStep } from '../../api/chat'
import { agentApi, type Agent } from '../../api/agent'

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
const expandedRows = ref<number[]>([])

onMounted(async () => {
  try {
    const res: any = await agentApi.get()
    defaultAgent.value = res.data || null
  } catch {
    defaultAgent.value = null
  }
  loadData()
})

async function loadData() {
  loading.value = true
  expandedRows.value = []
  try {
    const params: any = { page: page.value, page_size: pageSize.value }
    if (filterUserId.value) params.user_id = filterUserId.value
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

function stepTypeLabel(t: string) {
  switch (t) {
    case 'llm_call': return 'LLM'
    case 'tool_call': return 'Tool'
    case 'agent_call': return 'Agent'
    case 'skill_match': return 'Skill'
    default: return t
  }
}

function stepTagType(t: string): '' | 'success' | 'warning' | 'danger' | 'info' {
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
  color: #909399;
  padding: 20px;
}

.message-timeline {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.msg-item {
  border: 1px solid #ebeef5;
  border-radius: 8px;
  overflow: hidden;
}
.msg-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  background: #fafafa;
  border-bottom: 1px solid #f0f0f0;
}
.msg-time {
  font-size: 12px;
  color: #909399;
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
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  color: #303133;
  max-height: 300px;
  overflow-y: auto;
}

.steps-section {
  border-top: 1px solid #f0f0f0;
}
.steps-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  cursor: pointer;
  font-size: 13px;
  color: #606266;
  transition: background-color 0.2s;
}
.steps-header:hover {
  background: #f5f7fa;
}
.steps-summary {
  margin-left: auto;
  font-size: 12px;
  color: #909399;
}
.arrow {
  transition: transform 0.3s;
  margin-left: 4px;
}
.arrow.expanded {
  transform: rotate(180deg);
}
.steps-body {
  padding: 16px 20px 8px;
  background: #fafbfc;
}

.step-card {
  background: #fff;
  border: 1px solid #ebeef5;
  border-radius: 6px;
  padding: 12px 14px;
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
  color: #303133;
}

.step-block {
  margin-bottom: 8px;
}
.step-block-label {
  font-size: 12px;
  font-weight: 500;
  color: #909399;
  margin-bottom: 2px;
}
.error-label {
  color: #f56c6c;
}
.step-block-code {
  background: #f5f7fa;
  border: 1px solid #ebeef5;
  border-radius: 4px;
  padding: 8px 10px;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 200px;
  overflow-y: auto;
  font-family: 'SF Mono', 'Monaco', 'Menlo', 'Consolas', monospace;
  font-size: 12px;
  line-height: 1.5;
  color: #303133;
}
.error-code {
  background: #fef0f0;
  border-color: #fde2e2;
  color: #f56c6c;
}

.step-meta-row {
  display: flex;
  gap: 16px;
  font-size: 11px;
  color: #909399;
  margin-top: 4px;
}
.step-meta-row span {
  display: flex;
  align-items: center;
  gap: 3px;
}

.slide-enter-active, .slide-leave-active {
  transition: all 0.3s ease;
  max-height: 3000px;
  overflow: hidden;
}
.slide-enter-from, .slide-leave-to {
  max-height: 0;
  opacity: 0;
}
</style>
