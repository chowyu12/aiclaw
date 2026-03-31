<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <div style="display:flex;align-items:center;justify-content:space-between;">
        <div>
          <h1 class="aic-title">Agents</h1>
          <p class="aic-sub" style="margin-top:4px;font-size:13px;color:var(--el-text-color-secondary)">
            管理多个 Agent，每个 Agent 独立配置模型、提示词与工具，对话互相隔离。
          </p>
        </div>
        <el-button type="primary" @click="$router.push('/agents/create')">
          <el-icon><Plus /></el-icon>&nbsp;新建 Agent
        </el-button>
      </div>
    </div>

    <div class="aic-page-body">
      <el-table :data="agents" v-loading="loading" row-key="id" style="width:100%" table-layout="auto">
        <el-table-column label="名称" prop="name" min-width="160">
          <template #default="{ row }">
            <span class="agent-name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="供应商" min-width="100" show-overflow-tooltip>
          <template #default="{ row }">
            {{ providerName(row.provider_id) }}
          </template>
        </el-table-column>
        <el-table-column label="模型" prop="model_name" min-width="160" show-overflow-tooltip />
        <el-table-column label="迭代上限" prop="max_iterations" width="80" align="center" />
        <el-table-column label="历史条数" prop="max_history" width="80" align="center" />
        <el-table-column label="默认" width="70" align="center">
          <template #default="{ row }">
            <el-switch
              :model-value="row.is_default"
              :disabled="row.is_default"
              :loading="switchingId === row.id"
              @change="setDefault(row)"
            />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row }">
            <div style="display:flex;gap:6px;flex-wrap:nowrap;">
              <el-button size="small" @click="$router.push(`/agents/${row.id}/edit`)">编辑</el-button>
              <el-button size="small" type="danger" plain @click="handleDelete(row)">删除</el-button>
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

const agentStore = useAgentStore()
const agents = ref<Agent[]>([])
const providers = ref<Provider[]>([])
const loading = ref(false)
const switchingId = ref<number | null>(null)

function providerName(id: number): string {
  return providers.value.find((p) => p.id === id)?.name ?? '-'
}

async function loadAgents() {
  loading.value = true
  try {
    const res: any = await agentApi.list({ page: 1, page_size: 100 })
    agents.value = res.data?.list ?? []
  } catch {
    ElMessage.error('加载失败')
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

async function setDefault(row: Agent) {
  if (row.is_default) return
  switchingId.value = row.id
  try {
    const isDefault = true
    await agentApi.updateById(row.id, { is_default: isDefault })
    agents.value.forEach((a) => { a.is_default = a.id === row.id })
    await agentStore.loadAgents()
    ElMessage.success(`已将「${row.name}」设为默认 Agent`)
  } catch {
    ElMessage.error('设置失败')
  } finally {
    switchingId.value = null
  }
}

async function handleDelete(row: Agent) {
  try {
    await ElMessageBox.confirm(`确定删除 Agent「${row.name}」？`, '删除', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await agentApi.deleteById(row.id)
    ElMessage.success('已删除')
    await loadAgents()
    await agentStore.loadAgents()
  } catch {
    ElMessage.error('删除失败')
  }
}

onMounted(() => {
  loadAgents()
  loadProviders()
})
</script>

<style scoped>
.agent-name {
  font-weight: 500;
}
</style>
