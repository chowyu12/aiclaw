<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">MCP</h1>
      <p class="aic-sub">
        {{ i18n.t('mcp.subtitle') }}
      </p>
    </div>
    <div class="aic-page-body">
      <el-card class="aic-card" shadow="never" v-loading="mcpLoading">
        <div class="toolbar">
          <el-button type="primary" @click="addMcpRow">{{ i18n.t('mcp.addServer') }}</el-button>
          <el-button @click="loadMcp">{{ i18n.t('common.refresh') }}</el-button>
          <el-button type="success" :loading="mcpSaving" @click="saveMcp">{{ i18n.t('mcp.saveWorkspace') }}</el-button>
        </div>
        <el-table :data="mcpServers" stripe style="width: 100%; margin-top: 16px">
          <el-table-column prop="name" :label="i18n.t('common.name')" min-width="120">
            <template #default="{ row }">
              <el-input v-model="row.name" size="small" />
            </template>
          </el-table-column>
          <el-table-column :label="i18n.t('mcp.transport')" width="100">
            <template #default="{ row }">
              <el-select v-model="row.transport" size="small" style="width: 100%">
                <el-option label="stdio" value="stdio" />
                <el-option label="sse" value="sse" />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="Endpoint" min-width="160">
            <template #default="{ row }">
              <el-input v-model="row.endpoint" size="small" :placeholder="i18n.t('mcp.endpointPlaceholder')" />
            </template>
          </el-table-column>
          <el-table-column :label="i18n.t('mcp.args')" min-width="140">
            <template #default="{ row }">
              <el-input v-model="row._argsStr" size="small" placeholder='["-y","pkg"]' @blur="parseArgsRow(row)" />
            </template>
          </el-table-column>
          <el-table-column :label="i18n.t('common.enabled')" width="72" align="center">
            <template #default="{ row }">
              <el-switch v-model="row.enabled" />
            </template>
          </el-table-column>
          <el-table-column :label="i18n.t('common.actions')" width="80" fixed="right">
            <template #default="{ $index }">
              <el-button link type="danger" size="small" @click="mcpServers.splice($index, 1)">{{ i18n.t('common.delete') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { runtimeMcpApi, type McpServer } from '@/api/mcp'
import { useI18nStore } from '@/stores/i18n'

type McpRow = McpServer & { _argsStr?: string }

const i18n = useI18nStore()
const mcpServers = ref<McpRow[]>([])
const mcpLoading = ref(false)
const mcpSaving = ref(false)

function rowToMcpRow(s: McpServer): McpRow {
  const args = s.args && Array.isArray(s.args) ? s.args : []
  return {
    ...s,
    uuid: s.uuid || '',
    enabled: s.enabled !== false,
    _argsStr: JSON.stringify(args),
  }
}

function parseArgsRow(row: McpRow) {
  try {
    const v = row._argsStr?.trim()
    if (!v) {
      row.args = []
      return
    }
    row.args = JSON.parse(v) as string[]
  } catch {
    ElMessage.error(i18n.t('mcp.argsError'))
  }
}

async function loadMcp() {
  mcpLoading.value = true
  try {
    const res: any = await runtimeMcpApi.list()
    const list = (res.data?.list || []) as McpServer[]
    mcpServers.value = list.map(rowToMcpRow)
  } catch {
    mcpServers.value = []
  } finally {
    mcpLoading.value = false
  }
}

function addMcpRow() {
  mcpServers.value.push({
    uuid: '',
    name: 'mcp-server',
    description: '',
    transport: 'stdio',
    endpoint: 'npx',
    args: [],
    env: null,
    headers: null,
    enabled: true,
    _argsStr: '[]',
  })
}

async function saveMcp() {
  for (const row of mcpServers.value) parseArgsRow(row)
  mcpSaving.value = true
  try {
    const payload: McpServer[] = mcpServers.value.map(({ _argsStr, ...rest }) => rest)
    await runtimeMcpApi.save(payload)
    ElMessage.success(i18n.t('mcp.saved'))
    await loadMcp()
  } finally {
    mcpSaving.value = false
  }
}

onMounted(() => loadMcp())
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
