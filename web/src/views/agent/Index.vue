<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <div style="display:flex;align-items:center;justify-content:space-between;">
        <div>
          <h1 class="aic-title">Agents</h1>
          <p class="aic-sub" style="margin-top:4px;font-size:13px;color:var(--el-text-color-secondary)">
            {{ i18n.t('agents.subtitle') }}
          </p>
        </div>
        <el-button type="primary" @click="$router.push('/agents/create')">
          <el-icon><Plus /></el-icon>&nbsp;{{ i18n.t('agents.create') }}
        </el-button>
      </div>
    </div>

    <div class="aic-page-body">
      <el-table :data="agents" v-loading="loading" row-key="id" style="width:100%" table-layout="auto">
        <el-table-column :label="i18n.t('common.name')" prop="name" min-width="160">
          <template #default="{ row }">
            <span class="agent-name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.type')" width="90">
          <template #default="{ row }">
            <el-tag :type="row.execution_mode === 'local' ? 'success' : 'info'" effect="plain" size="small">
              {{ row.execution_mode === 'local' ? '本地' : '内置' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('agents.provider')" min-width="120" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.execution_mode === 'local' ? runtimeName(row.runtime_id) : providerName(row.provider_id) }}
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('agents.model')" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">{{ row.execution_mode === 'local' ? 'Local CLI' : row.model_name }}</template>
        </el-table-column>
        <el-table-column :label="i18n.t('agents.maxIterations')" prop="max_iterations" width="110" align="center" />
        <el-table-column :label="i18n.t('agents.maxHistory')" prop="max_history" width="90" align="center" />
        <el-table-column :label="i18n.t('common.default')" width="70" align="center">
          <template #default="{ row }">
            <el-switch
              :model-value="row.is_default"
              :disabled="row.is_default"
              :loading="switchingId === row.id"
              @change="setDefault(row)"
            />
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.actions')" width="140" fixed="right">
          <template #default="{ row }">
            <div style="display:flex;gap:6px;flex-wrap:nowrap;">
              <el-button size="small" @click="$router.push(`/agents/${row.id}/edit`)">{{ i18n.t('common.edit') }}</el-button>
              <el-button size="small" type="danger" plain @click="handleDelete(row)">{{ i18n.t('common.delete') }}</el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { agentApi, type Agent } from '../../api/agent'
import { providerApi, type Provider } from '../../api/provider'
import { useAgentStore } from '../../stores/agent'
import { useI18nStore } from '../../stores/i18n'
import { runtimeApi, type Runtime } from '@/api/runtime'

const agentStore = useAgentStore()
const i18n = useI18nStore()
const agents = ref<Agent[]>([])
const providers = ref<Provider[]>([])
const runtimes = ref<Runtime[]>([])
const loading = ref(false)
const switchingId = ref<number | null>(null)

function providerName(id: number): string {
  return providers.value.find((p) => p.id === id)?.name ?? '-'
}

function runtimeName(id: number): string {
  return runtimes.value.find((runtime) => runtime.id === id)?.name ?? '-'
}

async function loadAgents() {
  loading.value = true
  try {
    const res: any = await agentApi.list({ page: 1, page_size: 100 })
    agents.value = res.data?.list ?? []
  } catch {
    ElMessage.error(i18n.t('common.loadingFailed'))
  } finally {
    loading.value = false
  }
}

async function loadProviders() {
  try {
    const res: any = await providerApi.list({ page: 1, page_size: 200 })
    providers.value = res.data?.list ?? []
  } catch {
    providers.value = []
  }
}

async function loadRuntimes() {
  try {
    const res: any = await runtimeApi.list({ page: 1, page_size: 100 })
    runtimes.value = res.data?.list ?? []
  } catch {
    runtimes.value = []
  }
}

async function setDefault(row: Agent) {
  if (row.is_default) return
  switchingId.value = row.id
  try {
    const isDefault = true
    await agentApi.updateById(row.id, { is_default: isDefault })
    agents.value.forEach((a) => { a.is_default = a.id === row.id })
    await agentStore.loadAgents()
    ElMessage.success(i18n.t('agents.setDefaultSuccess'))
  } catch {
    ElMessage.error(i18n.t('agents.setDefaultFailed'))
  } finally {
    switchingId.value = null
  }
}

async function handleDelete(row: Agent) {
  try {
    await ElMessageBox.confirm(`${i18n.t('agents.deleteConfirm')} ${row.name}`, i18n.t('common.delete'), {
      type: 'warning',
      confirmButtonText: i18n.t('common.delete'),
      cancelButtonText: i18n.t('common.cancel'),
    })
  } catch {
    return
  }
  try {
    await agentApi.deleteById(row.id)
    ElMessage.success(i18n.t('common.deleteSuccess'))
    await loadAgents()
    await agentStore.loadAgents()
  } catch {
    ElMessage.error(i18n.t('common.deleteFailed'))
  }
}

onMounted(() => {
  loadAgents()
  loadProviders()
  loadRuntimes()
})
</script>

<style scoped>
.agent-name {
  font-weight: 500;
}
</style>
