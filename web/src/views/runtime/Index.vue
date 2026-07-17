<template>
  <div class="aic-page">
    <div class="aic-page-head runtime-head">
      <div>
        <h1 class="aic-title">{{ i18n.t('runtimes.title') }}</h1>
        <p class="aic-sub runtime-sub">{{ i18n.t('runtimes.subtitle') }}</p>
      </div>
      <el-button type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>&nbsp;{{ i18n.t('runtimes.create') }}
      </el-button>
    </div>

    <div class="aic-page-body">
      <el-table :data="runtimes" v-loading="loading" row-key="id" table-layout="auto">
        <el-table-column :label="i18n.t('common.name')" min-width="150">
          <template #default="{ row }">
            <div class="runtime-name">
              <span class="status-dot" :class="row.status" />
              <span>{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('runtimes.agentType')" min-width="130">
          <template #default="{ row }">{{ agentProfileName(row.agent_type) }}</template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.status')" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'online' ? 'success' : 'info'" effect="plain" size="small">
              {{ row.status === 'online' ? i18n.t('runtimes.online') : i18n.t('runtimes.offline') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="i18n.t('runtimes.command')" min-width="260" show-overflow-tooltip>
          <template #default="{ row }"><code>{{ commandPreview(row) }}</code></template>
        </el-table-column>
        <el-table-column :label="i18n.t('runtimes.lastSeen')" min-width="150">
          <template #default="{ row }">{{ row.last_seen_at ? formatTime(row.last_seen_at) : '-' }}</template>
        </el-table-column>
        <el-table-column :label="i18n.t('common.actions')" width="270" fixed="right">
          <template #default="{ row }">
            <div class="runtime-actions">
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

    <el-dialog v-model="dialogVisible" :title="editingId ? i18n.t('runtimes.edit') : i18n.t('runtimes.create')" width="600px">
      <el-form label-position="top">
        <el-form-item :label="i18n.t('common.name')" required>
          <el-input v-model="form.name" placeholder="MacBook Codex" />
        </el-form-item>
        <el-form-item :label="i18n.t('common.description')">
          <el-input v-model="form.description" />
        </el-form-item>
        <el-form-item :label="i18n.t('runtimes.agentType')">
          <el-select v-model="form.agentType" style="width:100%" @change="applyAgentProfile">
            <el-option v-for="profile in agentProfiles" :key="profile.type" :label="profile.name" :value="profile.type" />
          </el-select>
          <div class="runtime-hint">{{ i18n.t('runtimes.profileHint') }}</div>
        </el-form-item>
        <el-form-item :label="i18n.t('runtimes.executable')" required>
          <el-input v-model="form.command" placeholder="codex" />
          <div class="runtime-hint">{{ i18n.t('runtimes.commandHint') }}</div>
        </el-form-item>
        <el-form-item :label="i18n.t('runtimes.args')">
          <el-input v-model="form.argsText" type="textarea" :rows="3" placeholder='["exec", "-"]' />
          <div class="runtime-hint">{{ i18n.t('runtimes.argsHint') }}</div>
        </el-form-item>
        <el-form-item v-if="form.agentType === 'custom'" :label="i18n.t('runtimes.promptMode')">
          <el-radio-group v-model="form.promptMode">
            <el-radio-button label="stdin">{{ i18n.t('runtimes.promptStdin') }}</el-radio-button>
            <el-radio-button label="argument">{{ i18n.t('runtimes.promptArgument') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <div class="runtime-hint runtime-hint--warning">{{ i18n.t('runtimes.profileWarning') }}</div>
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
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { runtimeApi, type Runtime } from '@/api/runtime'
import { useI18nStore } from '@/stores/i18n'

const i18n = useI18nStore()
const runtimes = ref<Runtime[]>([])
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const connectVisible = ref(false)
const connectCommand = ref('')
const editingId = ref(0)
type AgentProfile = {
  type: Runtime['agent_type']
  name: string
  command: string
  args: string[]
  promptMode: Runtime['prompt_mode']
}

const agentProfiles: AgentProfile[] = [
  { type: 'codex', name: 'Codex', command: 'codex', args: ['exec', '-'], promptMode: 'stdin' },
  { type: 'cursor', name: 'Cursor', command: 'cursor-agent', args: ['-p', '--output-format', 'text'], promptMode: 'argument' },
  { type: 'claude-code', name: 'Claude Code', command: 'claude', args: ['-p', '--output-format', 'text'], promptMode: 'argument' },
  { type: 'codebuddy', name: 'Tencent CodeBuddy', command: 'codebuddy', args: ['-p', '--output-format', 'text'], promptMode: 'argument' },
  { type: 'openclaw', name: 'OpenClaw', command: 'openclaw', args: ['agent', '--local', '--message'], promptMode: 'argument' },
  { type: 'hermes', name: 'Hermes Agent', command: 'hermes', args: ['chat', '-q'], promptMode: 'argument' },
  { type: 'custom', name: 'Custom CLI', command: '', args: [], promptMode: 'stdin' },
]

const form = reactive({
  name: '', description: '', agentType: 'codex' as Runtime['agent_type'], command: 'codex', argsText: '["exec", "-"]', promptMode: 'stdin' as Runtime['prompt_mode'],
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

function openCreate() {
  editingId.value = 0
  Object.assign(form, { name: '', description: '', agentType: 'codex', command: 'codex', argsText: '["exec", "-"]', promptMode: 'stdin' })
  dialogVisible.value = true
}

function openEdit(runtime: Runtime) {
  editingId.value = runtime.id
  Object.assign(form, {
    name: runtime.name,
    description: runtime.description || '',
    agentType: runtime.agent_type || 'custom',
    command: runtime.command,
    argsText: JSON.stringify(runtime.args || [], null, 2),
    promptMode: runtime.prompt_mode || 'stdin',
  })
  dialogVisible.value = true
}

function applyAgentProfile() {
  const profile = agentProfiles.find((item) => item.type === form.agentType)
  if (!profile) return
  form.command = profile.command
  form.argsText = JSON.stringify(profile.args, null, 2)
  form.promptMode = profile.promptMode
}

function agentProfileName(agentType: Runtime['agent_type']) {
  return agentProfiles.find((item) => item.type === agentType)?.name || 'Custom CLI'
}

function parseArgs(): string[] | null {
  try {
    const args = JSON.parse(form.argsText || '[]')
    if (!Array.isArray(args) || args.some((arg) => typeof arg !== 'string')) throw new Error()
    return args
  } catch {
    ElMessage.warning(i18n.t('runtimes.argsInvalid'))
    return null
  }
}

async function saveRuntime() {
  if (!form.name.trim() || !form.command.trim()) {
    ElMessage.warning(i18n.t('runtimes.required'))
    return
  }
  const args = parseArgs()
  if (!args) return
  saving.value = true
  try {
    const payload = {
      name: form.name.trim(), description: form.description.trim(), agent_type: form.agentType,
      command: form.command.trim(), args, prompt_mode: form.promptMode,
    }
    if (editingId.value) {
      await runtimeApi.update(editingId.value, payload)
      ElMessage.success(i18n.t('common.saveSuccess'))
    } else {
      const res: any = await runtimeApi.create(payload)
      const runtime = res.data as Runtime
      if (runtime) showConnect(runtime)
    }
    dialogVisible.value = false
    await loadRuntimes()
  } finally {
    saving.value = false
  }
}

function commandPreview(runtime: Runtime) {
  return [runtime.command, ...(runtime.args || [])].join(' ')
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
.runtime-actions { display: flex; gap: 6px; align-items: center; }
.runtime-hint { margin-top: 5px; font-size: 12px; color: var(--el-text-color-secondary); }
.connect-desc { color: var(--el-text-color-secondary); margin-bottom: 12px; }
.connect-command { display: flex; align-items: flex-start; gap: 10px; padding: 12px; border-radius: 8px; background: var(--el-fill-color-light); }
.connect-command code { flex: 1; white-space: pre-wrap; word-break: break-all; line-height: 1.6; }
</style>
