<template>
  <div class="aic-page">
    <div class="aic-page-head runtime-head">
      <div>
        <h1 class="aic-title">{{ i18n.t('runtimes.title') }}</h1>
        <p class="aic-sub runtime-sub">{{ i18n.t('runtimes.subtitle') }}</p>
      </div>
      <el-button type="primary" @click="openCreate">{{ i18n.t('runtimes.createRemote') }}</el-button>
    </div>

    <div class="aic-page-body">
      <el-table :data="runtimes" v-loading="loading" row-key="id" table-layout="auto">
        <el-table-column type="expand" width="52">
          <template #default="{ row }">
            <div v-if="runtimeAgentConfigs(row).length" class="runtime-agent-list">
              <div v-for="agent in runtimeAgentConfigs(row)" :key="agent.agent_type" class="runtime-agent-row">
                <div class="runtime-agent-info">
                  <span class="runtime-agent-title">{{ agentTypeName(agent.agent_type) }}</span>
                  <span class="runtime-agent-model">{{ agentModelLabel(agent) }}</span>
                </div>
                <div class="runtime-agent-actions">
                  <el-switch
                    :model-value="agent.enabled"
                    :loading="updatingAgentKeys.has(runtimeAgentKey(row.id, agent.agent_type))"
                    :active-text="i18n.t('runtimes.agentEnabled')"
                    :inactive-text="i18n.t('runtimes.agentDisabled')"
                    @change="(enabled: string | number | boolean) => setRuntimeAgentEnabled(row, agent, Boolean(enabled))"
                  />
                  <el-button size="small" @click="openRuntimeAgentConfig(row, agent)">{{ i18n.t('runtimes.agentConfig') }}</el-button>
                </div>
              </div>
            </div>
            <span v-else class="runtime-empty">{{ i18n.t('runtimes.noAgents') }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.name')" min-width="150">
          <template #default="{ row }">
            <div class="runtime-name">
              <span class="status-dot" :class="row.status" />
              <span>{{ row.name }}</span>
              <el-tag v-if="row.builtin" size="small" type="success" effect="plain">{{ i18n.t('runtimes.builtin') }}</el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.status')" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'online' ? 'success' : 'info'" effect="plain" size="small">
              {{ row.status === 'online' ? i18n.t('runtimes.online') : i18n.t('runtimes.offline') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('runtimes.detectedAgents')" min-width="260">
          <template #default="{ row }">
            <div v-if="row.detected_agents?.length" class="detected-agents">
              <el-tag v-for="agentType in row.detected_agents" :key="agentType" size="small" effect="plain">{{ agentTypeName(agentType) }}</el-tag>
            </div>
            <span v-else class="runtime-empty">{{ i18n.t('runtimes.noAgents') }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('runtimes.lastSeen')" min-width="150">
          <template #default="{ row }">{{ row.last_seen_at ? formatTime(row.last_seen_at) : '-' }}</template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.actions')" width="270" fixed="right">
          <template #default="{ row }">
            <div v-if="!row.builtin" class="runtime-actions">
              <el-button size="small" @click="copyConnectCommand(row)">{{ i18n.t('runtimes.copyConnect') }}</el-button>
              <el-button size="small" @click="openEdit(row)">{{ i18n.t('common.edit') }}</el-button>
              <el-dropdown trigger="click">
                <el-button size="small">•••</el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item @click="resetToken(row)">{{ i18n.t('runtimes.resetToken') }}</el-dropdown-item>
                    <el-dropdown-item divided @click="removeRuntime(row)">{{ i18n.t('common.delete') }}</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" :title="editingId ? i18n.t('runtimes.edit') : i18n.t('runtimes.createRemote')" width="600px">
      <el-form label-position="top">
        <el-form-item :label="i18n.t('common.name')" required>
          <el-input v-model="form.name" placeholder="Office MacBook" />
        </el-form-item>
        <el-form-item :label="i18n.t('common.description')">
          <el-input v-model="form.description" />
        </el-form-item>
        <div class="runtime-hint runtime-hint--warning">{{ i18n.t('runtimes.remoteHint') }}</div>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ i18n.t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="saveRuntime">{{ i18n.t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="connectVisible" :title="i18n.t('runtimes.connectTitle')" width="680px">
      <p class="connect-desc">{{ i18n.t('runtimes.connectHint') }}</p>
      <div class="connect-command">
        <code>{{ connectCommand }}</code>
        <el-button size="small" @click="copyText(connectCommand)">{{ i18n.t('chat.copy') }}</el-button>
      </div>
    </el-dialog>

    <el-dialog v-model="agentConfigVisible" :title="`${agentTypeName(agentConfigForm.agentType)} · ${i18n.t('runtimes.agentConfig')}`" width="520px">
      <el-form label-position="top">
        <el-form-item :label="i18n.t('runtimes.agentStatus')">
          <el-switch v-model="agentConfigForm.enabled" :active-text="i18n.t('runtimes.agentEnabled')" :inactive-text="i18n.t('runtimes.agentDisabled')" />
        </el-form-item>
        <el-form-item :label="agentConfigModelLabel(agentConfigForm.agentType)">
          <el-input v-model="agentConfigForm.modelName" :placeholder="agentConfigModelPlaceholder(agentConfigForm.agentType)" clearable />
          <div class="runtime-hint">{{ agentConfigModelHint(agentConfigForm.agentType) }}</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="agentConfigVisible = false">{{ i18n.t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="agentConfigSaving" @click="saveRuntimeAgentConfig">{{ i18n.t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { runtimeApi, type Runtime, type RuntimeAgentConfig } from '@/api/runtime'
import { useI18nStore } from '@/stores/i18n'

const i18n = useI18nStore()
const runtimes = ref<Runtime[]>([])
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const connectVisible = ref(false)
const connectCommand = ref('')
const editingId = ref(0)
const agentConfigVisible = ref(false)
const agentConfigSaving = ref(false)
const updatingAgentKeys = ref(new Set<string>())
const agentTypeNames: Record<string, string> = {
  codex: 'Codex', cursor: 'Cursor', 'claude-code': 'Claude Code', codebuddy: 'Tencent CodeBuddy', openclaw: 'OpenClaw', hermes: 'Hermes Agent',
}
const form = reactive({ name: '', description: '' })
const agentConfigForm = reactive({
  runtimeId: 0,
  agentType: 'codex' as RuntimeAgentConfig['agent_type'],
  enabled: true,
  modelName: '',
})
let refreshTimer: ReturnType<typeof setInterval> | undefined

async function loadRuntimes(silent = false) {
  if (!silent) loading.value = true
  try {
    const res: any = await runtimeApi.list({ page: 1, page_size: 100 })
    runtimes.value = res.data?.list || []
  } catch {
    if (!silent) ElMessage.error(i18n.t('common.loadingFailed'))
  } finally {
    if (!silent) loading.value = false
  }
}

function agentTypeName(agentType: string) { return agentTypeNames[agentType] || agentType }

function runtimeAgentKey(runtimeId: number, agentType: string) {
  return `${runtimeId}:${agentType}`
}

function runtimeAgentConfigs(runtime: Runtime): RuntimeAgentConfig[] {
  return runtime.detected_agents.map((agentType) =>
    runtime.agent_configs?.find((config) => config.agent_type === agentType) || {
      id: 0,
      runtime_id: runtime.id,
      agent_type: agentType,
      enabled: true,
      model_name: '',
      created_at: '',
      updated_at: '',
    },
  )
}

function agentModelLabel(agent: RuntimeAgentConfig) {
  if (agent.agent_type === 'openclaw') return agent.model_name || i18n.t('runtimes.agentTargetDefault')
  return agent.model_name || i18n.t('runtimes.agentModelDefault')
}

function agentConfigModelLabel(agentType: RuntimeAgentConfig['agent_type']) {
  return agentType === 'openclaw' ? i18n.t('runtimes.agentTarget') : i18n.t('runtimes.agentModel')
}

function agentConfigModelPlaceholder(agentType: RuntimeAgentConfig['agent_type']) {
  return agentType === 'openclaw' ? i18n.t('runtimes.agentTargetPlaceholder') : i18n.t('runtimes.agentModelPlaceholder')
}

function agentConfigModelHint(agentType: RuntimeAgentConfig['agent_type']) {
  return agentType === 'openclaw' ? i18n.t('runtimes.agentTargetHint') : i18n.t('runtimes.agentModelHint')
}

function updateRuntimeAgent(runtime: Runtime, updated: RuntimeAgentConfig) {
  const index = runtime.agent_configs.findIndex((item) => item.agent_type === updated.agent_type)
  if (index >= 0) runtime.agent_configs[index] = updated
  else runtime.agent_configs.push(updated)
}

async function setRuntimeAgentEnabled(runtime: Runtime, agent: RuntimeAgentConfig, enabled: boolean) {
  const key = runtimeAgentKey(runtime.id, agent.agent_type)
  updatingAgentKeys.value.add(key)
  try {
    const res: any = await runtimeApi.updateAgent(runtime.id, agent.agent_type, { enabled })
    updateRuntimeAgent(runtime, res.data as RuntimeAgentConfig)
    ElMessage.success(i18n.t('common.saveSuccess'))
  } catch {
    ElMessage.error(i18n.t('common.operationFailed'))
  } finally {
    updatingAgentKeys.value.delete(key)
    updatingAgentKeys.value = new Set(updatingAgentKeys.value)
  }
}

function openRuntimeAgentConfig(runtime: Runtime, agent: RuntimeAgentConfig) {
  Object.assign(agentConfigForm, {
    runtimeId: runtime.id,
    agentType: agent.agent_type,
    enabled: agent.enabled,
    modelName: agent.model_name || '',
  })
  agentConfigVisible.value = true
}

async function saveRuntimeAgentConfig() {
  agentConfigSaving.value = true
  try {
    const res: any = await runtimeApi.updateAgent(agentConfigForm.runtimeId, agentConfigForm.agentType, {
      enabled: agentConfigForm.enabled,
      model_name: agentConfigForm.modelName.trim(),
    })
    const runtime = runtimes.value.find((item) => item.id === agentConfigForm.runtimeId)
    if (runtime) updateRuntimeAgent(runtime, res.data as RuntimeAgentConfig)
    agentConfigVisible.value = false
    ElMessage.success(i18n.t('common.saveSuccess'))
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || i18n.t('common.operationFailed'))
  } finally {
    agentConfigSaving.value = false
  }
}

function openCreate() {
  editingId.value = 0
  Object.assign(form, { name: '', description: '' })
  dialogVisible.value = true
}

function openEdit(runtime: Runtime) {
  editingId.value = runtime.id
  Object.assign(form, { name: runtime.name, description: runtime.description || '' })
  dialogVisible.value = true
}

async function saveRuntime() {
  if (!form.name.trim()) {
    ElMessage.warning(i18n.t('runtimes.required'))
    return
  }
  saving.value = true
  try {
    const payload = { name: form.name.trim(), description: form.description.trim() }
    const res: any = editingId.value
      ? await runtimeApi.update(editingId.value, payload)
      : await runtimeApi.create(payload)
    dialogVisible.value = false
    await loadRuntimes()
    if (editingId.value) ElMessage.success(i18n.t('common.saveSuccess'))
    else {
      const runtime = res.data as Runtime
      if (runtime) showConnect(runtime)
    }
  } finally {
    saving.value = false
  }
}

function runtimeConnectCommand(runtime: Runtime) {
  return `aiclaw runtime connect --server ${window.location.origin} --token ${runtime.token}`
}

function showConnect(runtime: Runtime) {
  connectCommand.value = runtimeConnectCommand(runtime)
  connectVisible.value = true
}

function copyConnectCommand(runtime: Runtime) {
  showConnect(runtime)
  copyText(connectCommand.value)
}

function copyText(text: string) {
  navigator.clipboard.writeText(text).then(() => ElMessage.success(i18n.t('chat.copied')))
}

async function resetToken(runtime: Runtime) {
  try {
    await ElMessageBox.confirm(i18n.t('runtimes.resetConfirm'), i18n.t('runtimes.resetToken'), { type: 'warning' })
  } catch { return }
  const res: any = await runtimeApi.resetToken(runtime.id)
  runtime.token = res.data?.token || runtime.token
  showConnect(runtime)
  ElMessage.success(i18n.t('common.saveSuccess'))
}

async function removeRuntime(runtime: Runtime) {
  try {
    await ElMessageBox.confirm(i18n.t('runtimes.deleteConfirm'), i18n.t('common.delete'), { type: 'warning' })
  } catch { return }
  try {
    await runtimeApi.delete(runtime.id)
    await loadRuntimes()
    ElMessage.success(i18n.t('common.deleteSuccess'))
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || i18n.t('common.deleteFailed'))
  }
}

function formatTime(value: string) {
  return new Date(value).toLocaleString(i18n.language)
}

onMounted(() => {
  loadRuntimes()
  refreshTimer = setInterval(() => loadRuntimes(true), 15000)
})
onUnmounted(() => { if (refreshTimer) clearInterval(refreshTimer) })
</script>

<style scoped>
.runtime-head { display: flex; align-items: center; justify-content: space-between; }
.runtime-sub { margin-top: 4px; font-size: 13px; color: var(--el-text-color-secondary); }
.runtime-name { display: flex; align-items: center; gap: 9px; font-weight: 500; }
.status-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--el-text-color-placeholder); }
.status-dot.online { background: var(--el-color-success); box-shadow: 0 0 8px color-mix(in srgb, var(--el-color-success) 50%, transparent); }
.runtime-actions { display: flex; align-items: center; gap: 6px; }
.detected-agents { display: flex; flex-wrap: wrap; gap: 6px; }
.runtime-agent-list { display: grid; gap: 8px; padding: 4px 16px 10px 56px; }
.runtime-agent-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px 14px; border: 1px solid var(--el-border-color-lighter); border-radius: 8px; background: var(--el-fill-color-lighter); }
.runtime-agent-info { display: flex; min-width: 0; flex-direction: column; gap: 3px; }
.runtime-agent-title { font-weight: 500; }
.runtime-agent-model { overflow: hidden; color: var(--el-text-color-secondary); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.runtime-agent-actions { display: flex; flex-shrink: 0; align-items: center; gap: 12px; }
.runtime-empty { color: var(--el-text-color-secondary); font-size: 13px; }
.runtime-hint { margin-top: 5px; font-size: 12px; color: var(--el-text-color-secondary); }
.connect-desc { color: var(--el-text-color-secondary); margin-bottom: 12px; }
.connect-command { display: flex; align-items: flex-start; gap: 10px; padding: 12px; border-radius: 8px; background: var(--el-fill-color-light); }
.connect-command code { flex: 1; white-space: pre-wrap; word-break: break-all; line-height: 1.6; }
</style>
