<template>
  <div class="aic-page">
    <div class="aic-page-head memory-head">
      <div>
        <h1 class="aic-title">{{ i18n.t('memories.title') }}</h1>
        <p class="aic-sub">{{ i18n.t('memories.subtitle') }}</p>
      </div>
      <div class="memory-head-actions">
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon>{{ i18n.t('memories.new') }}
        </el-button>
      </div>
    </div>

    <div class="aic-page-body">
      <el-tabs v-model="status" class="memory-tabs" @tab-change="resetAndLoad">
        <el-tab-pane :label="i18n.t('memories.active')" name="active" />
        <el-tab-pane :label="i18n.t('memories.candidates')" name="candidate" />
        <el-tab-pane :label="i18n.t('memories.all')" name="all" />
      </el-tabs>

      <div class="memory-toolbar">
        <el-input v-model="keyword" :placeholder="i18n.t('memories.searchPlaceholder')" clearable class="memory-search" @clear="resetAndLoad" @keyup.enter="resetAndLoad">
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-select v-model="scope" clearable :placeholder="i18n.t('memories.scope')" class="memory-filter" @change="resetAndLoad">
          <el-option value="user" :label="scopeLabel('user')" />
          <el-option value="agent_user" :label="scopeLabel('agent_user')" />
        </el-select>
        <el-select v-model="kind" clearable :placeholder="i18n.t('memories.kind')" class="memory-filter" @change="resetAndLoad">
          <el-option v-for="item in kinds" :key="item" :value="item" :label="kindLabel(item)" />
        </el-select>
        <el-button circle :title="i18n.t('common.refresh')" @click="loadData">
          <el-icon><Refresh /></el-icon>
        </el-button>
      </div>

      <el-table :data="items" v-loading="loading" row-key="uuid" table-layout="fixed">
        <el-table-column :label="i18n.t('memories.memory')" min-width="300">
          <template #default="{ row }">
            <div class="memory-main">
              <div class="memory-summary">
                <span>{{ row.summary || row.memory_key }}</span>
                <el-icon v-if="row.pinned" class="pin-icon" :title="i18n.t('memories.pinned')"><Pin /></el-icon>
              </div>
              <div class="memory-content">{{ row.content }}</div>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('memories.scope')" width="130">
          <template #default="{ row }"><el-tag size="small" effect="plain">{{ scopeLabel(row.scope) }}</el-tag></template>
        </el-table-column>
        <el-table-column :label="i18n.t('memories.kind')" width="120">
          <template #default="{ row }">{{ kindLabel(row.kind) }}</template>
        </el-table-column>
        <el-table-column :label="i18n.t('memories.priority')" width="110" align="center">
          <template #default="{ row }">{{ row.importance }} · {{ Math.round(row.confidence * 100) }}%</template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.status')" width="110">
          <template #default="{ row }"><el-tag :type="statusType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag></template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.updatedAt')" width="170">
          <template #default="{ row }">{{ formatDate(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.actions')" width="220" fixed="right">
          <template #default="{ row }">
            <el-button v-if="row.status === 'candidate'" link type="success" @click="approve(row)">{{ i18n.t('memories.approve') }}</el-button>
            <el-button v-if="row.status === 'candidate'" link type="warning" @click="dismiss(row)">{{ i18n.t('memories.dismiss') }}</el-button>
            <el-button link type="primary" @click="openEdit(row)">{{ i18n.t('common.edit') }}</el-button>
            <el-button link @click="openHistory(row)">{{ i18n.t('memories.history') }}</el-button>
            <el-button link type="danger" @click="remove(row)">{{ i18n.t('common.delete') }}</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        class="memory-pagination"
        @size-change="loadData"
        @current-change="loadData"
      />
    </div>

    <el-dialog v-model="editorOpen" :title="editing ? i18n.t('memories.edit') : i18n.t('memories.new')" width="620px" destroy-on-close>
      <el-form label-position="top" @submit.prevent>
        <div v-if="!editing" class="editor-grid">
          <el-form-item :label="i18n.t('memories.scope')">
            <el-select v-model="form.scope">
              <el-option value="user" :label="scopeLabel('user')" />
              <el-option value="agent_user" :label="scopeLabel('agent_user')" />
            </el-select>
          </el-form-item>
          <el-form-item :label="i18n.t('memories.kind')">
            <el-select v-model="form.kind">
              <el-option v-for="item in kinds" :key="item" :value="item" :label="kindLabel(item)" />
            </el-select>
          </el-form-item>
        </div>
        <el-form-item v-if="!editing && form.scope === 'agent_user'" :label="i18n.t('memories.agent')">
          <el-select v-model="form.agent_uuid" :placeholder="i18n.t('memories.agentPlaceholder')">
            <el-option v-for="agent in agents" :key="agent.uuid" :value="agent.uuid" :label="agent.name" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="!editing" :label="i18n.t('memories.key')">
          <el-input v-model="form.memory_key" :placeholder="i18n.t('memories.keyPlaceholder')" />
        </el-form-item>
        <el-form-item :label="i18n.t('memories.summary')">
          <el-input v-model="form.summary" :placeholder="i18n.t('memories.summaryPlaceholder')" />
        </el-form-item>
        <el-form-item :label="i18n.t('memories.content')">
          <el-input v-model="form.content" type="textarea" :rows="5" :placeholder="i18n.t('memories.contentPlaceholder')" />
        </el-form-item>
        <div class="editor-grid">
          <el-form-item :label="i18n.t('memories.importance')">
            <el-input-number v-model="form.importance" :min="1" :max="100" controls-position="right" />
          </el-form-item>
          <el-form-item :label="i18n.t('memories.confidence')">
            <el-input-number v-model="form.confidence" :min="0" :max="1" :step="0.1" :precision="1" controls-position="right" />
          </el-form-item>
        </div>
        <div class="editor-grid editor-grid--inline">
          <el-form-item :label="i18n.t('memories.sensitivity')">
            <el-select v-model="form.sensitivity">
              <el-option value="normal" :label="i18n.t('memories.normal')" />
              <el-option value="sensitive" :label="i18n.t('memories.sensitive')" />
            </el-select>
          </el-form-item>
          <el-form-item :label="i18n.t('memories.pinned')">
            <el-switch v-model="form.pinned" />
          </el-form-item>
        </div>
        <el-form-item :label="i18n.t('memories.expiresAt')">
          <el-date-picker v-model="form.expires_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" :placeholder="i18n.t('memories.expiresAtPlaceholder')" clearable />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editorOpen = false">{{ i18n.t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ i18n.t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="historyOpen" :title="i18n.t('memories.history')" width="680px" destroy-on-close>
      <div v-loading="historyLoading" class="history-content">
        <el-empty v-if="!historyLoading && revisions.length === 0" :description="i18n.t('common.noData')" />
        <div v-for="revision in revisions" :key="revision.id" class="history-row">
          <div class="history-meta"><strong>{{ revisionActionLabel(revision.action) }}</strong><span>{{ formatDate(revision.created_at) }}</span></div>
          <div class="history-summary">{{ revision.summary || revision.content }}</div>
        </div>
        <div v-if="evidence.length" class="history-evidence">
          <div class="history-evidence-title">{{ i18n.t('memories.evidence') }}</div>
          <div v-for="item in evidence" :key="item.id" class="history-evidence-row">
            <span>{{ evidenceLabel(item.relation) }}</span><span>{{ formatDate(item.created_at) }}</span>
          </div>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { memoryApi, type CreateMemoryRequest, type MemoryEvidence, type MemoryItem, type MemoryKind, type MemoryRevision, type MemoryScope, type MemoryStatus } from '@/api/memory'
import { agentApi, type Agent } from '@/api/agent'
import { useI18nStore } from '@/stores/i18n'

const i18n = useI18nStore()
const kinds: MemoryKind[] = ['preference', 'profile', 'fact', 'decision', 'procedure', 'constraint']
const items = ref<MemoryItem[]>([])
const agents = ref<Agent[]>([])
const total = ref(0)
const loading = ref(false)
const saving = ref(false)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')
const scope = ref<MemoryScope | ''>('')
const kind = ref<MemoryKind | ''>('')
const status = ref<'active' | 'candidate' | 'all'>('active')
const editorOpen = ref(false)
const editing = ref<MemoryItem | null>(null)
const historyOpen = ref(false)
const historyLoading = ref(false)
const revisions = ref<MemoryRevision[]>([])
const evidence = ref<MemoryEvidence[]>([])

const blankForm = (): CreateMemoryRequest => ({
  agent_uuid: '', scope: 'user', kind: 'preference', memory_key: '', content: '', summary: '', importance: 50, confidence: 0.8, sensitivity: 'normal', pinned: false, expires_at: '',
})
const form = reactive<CreateMemoryRequest>(blankForm())

function resetForm() { Object.assign(form, blankForm()) }
function scopeLabel(value: MemoryScope) { return i18n.t(`memories.scope.${value}`) }
function kindLabel(value: MemoryKind) { return i18n.t(`memories.kind.${value}`) }
function statusLabel(value: MemoryStatus) { return i18n.t(`memories.status.${value}`) }
function statusType(value: MemoryStatus): 'success' | 'warning' | 'info' | 'danger' {
  if (value === 'active') return 'success'
  if (value === 'candidate') return 'warning'
  if (value === 'dismissed' || value === 'superseded') return 'info'
  return 'danger'
}
function formatDate(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString(i18n.language)
}
function revisionActionLabel(value: string) { return i18n.t(`memories.revision.${value}`) }
function evidenceLabel(value: string) { return i18n.t(`memories.evidence.${value}`) }

async function loadData() {
  loading.value = true
  try {
    const res: any = await memoryApi.list({
      page: page.value,
      page_size: pageSize.value,
      keyword: keyword.value || undefined,
      scope: scope.value || undefined,
      kind: kind.value || undefined,
      status: status.value === 'all' ? undefined : status.value,
      include_all: status.value === 'all',
    })
    items.value = res.data?.list || []
    total.value = res.data?.total || 0
  } catch {
    ElMessage.error(i18n.t('common.loadingFailed'))
  } finally {
    loading.value = false
  }
}

async function loadAgents() {
  try {
    const res: any = await agentApi.list({ page: 1, page_size: 100 })
    agents.value = res.data?.list || []
  } catch {
    agents.value = []
  }
}

function resetAndLoad() { page.value = 1; loadData() }
function openCreate() { editing.value = null; resetForm(); editorOpen.value = true }
function openEdit(item: MemoryItem) {
  editing.value = item
  Object.assign(form, {
    agent_uuid: item.agent_uuid || '', scope: item.scope, kind: item.kind, memory_key: item.memory_key, content: item.content, summary: item.summary,
    importance: item.importance, confidence: item.confidence, sensitivity: item.sensitivity, pinned: item.pinned, expires_at: item.expires_at || '',
  })
  editorOpen.value = true
}

async function save() {
  if (!form.content?.trim() || (!editing.value && !form.memory_key?.trim())) {
    ElMessage.warning(i18n.t('memories.required'))
    return
  }
  if (!editing.value && form.scope === 'agent_user' && !form.agent_uuid) {
    ElMessage.warning(i18n.t('memories.agentRequired'))
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await memoryApi.update(editing.value.uuid, {
        content: form.content.trim(), summary: form.summary?.trim(), importance: form.importance,
        confidence: form.confidence, sensitivity: form.sensitivity, pinned: form.pinned, expires_at: form.expires_at || null,
      })
    } else {
      await memoryApi.create({ ...form, agent_uuid: form.agent_uuid || undefined, expires_at: form.expires_at || undefined, memory_key: form.memory_key.trim(), content: form.content.trim(), summary: form.summary?.trim() })
    }
    ElMessage.success(i18n.t('common.saveSuccess'))
    editorOpen.value = false
    loadData()
  } catch {
    ElMessage.error(i18n.t('common.operationFailed'))
  } finally {
    saving.value = false
  }
}

async function updateStatus(item: MemoryItem, next: MemoryStatus) {
  try {
    await memoryApi.update(item.uuid, { status: next })
    ElMessage.success(i18n.t('common.saveSuccess'))
    loadData()
  } catch { ElMessage.error(i18n.t('common.operationFailed')) }
}
function approve(item: MemoryItem) { return updateStatus(item, 'active') }
function dismiss(item: MemoryItem) { return updateStatus(item, 'dismissed') }
async function remove(item: MemoryItem) {
  try { await ElMessageBox.confirm(i18n.t('memories.deleteConfirm'), i18n.t('common.delete'), { type: 'warning' }) } catch { return }
  try { await memoryApi.delete(item.uuid); ElMessage.success(i18n.t('common.deleteSuccess')); loadData() } catch { ElMessage.error(i18n.t('common.deleteFailed')) }
}
async function openHistory(item: MemoryItem) {
  historyOpen.value = true
  historyLoading.value = true
  revisions.value = []
  evidence.value = []
  try {
    const [revisionRes, evidenceRes]: any[] = await Promise.all([memoryApi.revisions(item.uuid), memoryApi.evidence(item.uuid)])
    revisions.value = revisionRes.data || []
    evidence.value = evidenceRes.data || []
  } catch { ElMessage.error(i18n.t('common.loadingFailed')) } finally { historyLoading.value = false }
}

onMounted(() => { loadData(); loadAgents() })
</script>

<style scoped>
.memory-head, .memory-head-actions, .memory-toolbar, .editor-grid, .history-meta, .history-evidence-row { display: flex; align-items: center; }
.memory-head { justify-content: space-between; gap: 16px; }
.memory-head-actions, .memory-toolbar { gap: 10px; }
.memory-toolbar { margin: 0 0 18px; flex-wrap: wrap; }
.memory-search { width: min(360px, 100%); }
.memory-filter { width: 150px; }
.memory-main { min-width: 0; }
.memory-summary { display: flex; align-items: center; gap: 6px; font-weight: 600; color: var(--el-text-color-primary); }
.memory-content { margin-top: 5px; color: var(--el-text-color-secondary); font-size: 13px; line-height: 1.45; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.pin-icon { color: #d97706; flex: 0 0 auto; }
.memory-pagination { margin-top: 16px; justify-content: flex-end; }
.editor-grid { align-items: flex-start; gap: 16px; }
.editor-grid > .el-form-item { flex: 1; min-width: 0; }
.editor-grid--inline { align-items: center; }
.history-content { min-height: 120px; }
.history-row { padding: 12px 0; border-bottom: 1px solid var(--el-border-color-lighter); }
.history-meta { justify-content: space-between; gap: 16px; color: var(--el-text-color-secondary); font-size: 12px; }
.history-meta strong { color: var(--el-text-color-primary); }
.history-summary { margin-top: 6px; font-size: 13px; line-height: 1.5; }
.history-evidence { margin-top: 20px; }
.history-evidence-title { margin-bottom: 8px; font-size: 13px; font-weight: 600; }
.history-evidence-row { justify-content: space-between; padding: 6px 0; color: var(--el-text-color-secondary); font-size: 12px; }
@media (max-width: 720px) {
  .memory-head { align-items: flex-start; flex-direction: column; }
  .memory-head-actions { width: 100%; }
  .editor-grid { display: block; }
}
</style>
