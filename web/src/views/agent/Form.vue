<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <div style="display:flex;align-items:center;gap:12px">
        <el-button text @click="$router.back()"><el-icon><ArrowLeft /></el-icon></el-button>
        <div>
          <h1 class="aic-title">{{ isEdit ? '编辑 Agent' : '新建 Agent' }}</h1>
          <p class="aic-sub aic-sub-compact">{{ isEdit ? `编辑「${agentForm.name}」的配置` : '创建新 Agent，独立配置模型、提示词与工具' }}</p>
        </div>
      </div>
    </div>

    <div class="aic-page-body">
      <div class="af-layout" v-loading="agentLoading">
        <el-form :model="agentForm" label-position="top" class="af-layout-inner">
          <!-- ======== 左栏：核心编辑区 ======== -->
          <div class="af-main">
            <el-form-item label="名称" required>
              <el-input v-model="agentForm.name" placeholder="Agent 显示名称" />
            </el-form-item>
            <el-form-item label="描述">
              <el-input v-model="agentForm.description" placeholder="简要描述该 Agent 的用途" />
            </el-form-item>

            <el-row :gutter="16">
              <el-col :xs="24" :sm="12">
                <el-form-item label="供应商" required>
                  <el-select v-model="agentForm.provider_id" filterable style="width: 100%" @change="onProviderChange" placeholder="选择供应商">
                    <el-option v-for="p in providers" :key="p.id" :label="p.name" :value="p.id">
                      <span>{{ p.name }}</span>
                      <el-tag size="small" class="ml8" type="info">{{ p.type }}</el-tag>
                    </el-option>
                  </el-select>
                </el-form-item>
              </el-col>
              <el-col :xs="24" :sm="12">
                <el-form-item label="模型" required>
                  <el-select v-model="agentForm.model_name" filterable allow-create default-first-option style="width: 100%" :loading="modelLoading" :disabled="!agentForm.provider_id" @focus="onModelFocus" placeholder="选择或输入模型名">
                    <el-option-group v-if="remoteModels.length > 0" label="远程模型">
                      <el-option v-for="m in remoteModels" :key="'r-'+m" :label="m" :value="m" />
                    </el-option-group>
                    <el-option-group v-if="localOnlyModels.length > 0" :label="remoteModels.length ? '本地配置' : '模型列表'">
                      <el-option v-for="m in localOnlyModels" :key="'l-'+m" :label="m" :value="m" />
                    </el-option-group>
                  </el-select>
                  <div v-if="remoteFetchError" class="af-hint warn">{{ remoteFetchError }}</div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-form-item label="System Prompt">
              <el-input v-model="agentForm.system_prompt" type="textarea" :autosize="{ minRows: 8, maxRows: 24 }" placeholder="为 Agent 编写系统提示词，定义其角色、行为和约束" class="af-prompt-input" />
            </el-form-item>

            <!-- 操作栏 -->
            <div class="af-actions">
              <el-button type="primary" :loading="agentSaving" @click="saveAgent">{{ isEdit ? '保存配置' : '创建 Agent' }}</el-button>
              <el-button @click="$router.push('/agents')">取消</el-button>
            </div>
          </div>

          <!-- ======== 右栏：配置面板 ======== -->
          <div class="af-sidebar">
            <div class="af-sidebar-inner">
              <!-- 推理参数 -->
              <div class="af-panel">
                <h4 class="af-panel-title">推理参数</h4>
                <div class="af-field">
                  <span class="af-field-label">温度</span>
                  <div class="af-slider-row">
                    <el-slider v-model="agentForm.temperature" :min="0" :max="2" :step="0.1" :disabled="isTemperatureDisabled" class="af-slider" />
                    <span class="af-slider-val">{{ agentForm.temperature.toFixed(1) }}</span>
                  </div>
                </div>
                <div class="af-field-grid">
                  <div class="af-field">
                    <span class="af-field-label">Max Tokens</span>
                    <el-input-number v-model="agentForm.max_tokens" :min="1" :max="128000" controls-position="right" size="small" class="af-field-num" />
                  </div>
                  <div class="af-field">
                    <span class="af-field-label">超时 (s)</span>
                    <el-input-number v-model="agentForm.timeout" :min="0" controls-position="right" size="small" class="af-field-num" />
                  </div>
                  <div class="af-field">
                    <span class="af-field-label">历史条数</span>
                    <el-input-number v-model="agentForm.max_history" :min="1" :max="500" controls-position="right" size="small" class="af-field-num" />
                  </div>
                  <div class="af-field">
                    <span class="af-field-label">最大迭代</span>
                    <el-input-number v-model="agentForm.max_iterations" :min="1" :max="200" controls-position="right" size="small" class="af-field-num" />
                  </div>
                </div>
                <div class="af-field">
                  <span class="af-field-label">Token 预算 <span class="af-field-sub">0 = 不限制</span></span>
                  <el-input-number v-model="agentForm.token_budget" :min="0" :max="10000000" :step="10000" controls-position="right" size="small" class="af-field-num" />
                </div>
              </div>

              <!-- 工具 -->
              <div class="af-panel">
                <h4 class="af-panel-title">工具</h4>
                <div class="af-row">
                  <span class="af-row-label">Tool Search</span>
                  <el-switch v-model="agentForm.tool_search_enabled" @change="onToolSearchChange" size="small" />
                </div>
                <div v-if="agentForm.tool_search_enabled" class="af-row-hint">开启后自动加载全部已启用工具</div>
                <div v-if="!agentForm.tool_search_enabled" style="margin-top:8px">
                  <el-select v-model="agentForm.tool_ids" multiple filterable collapse-tags collapse-tags-tooltip style="width:100%" placeholder="选择工具" size="small">
                    <el-option v-for="t in allTools" :key="t.id" :label="t.name" :value="t.id" />
                  </el-select>
                </div>
              </div>

              <!-- MemOS -->
              <div class="af-panel">
                <h4 class="af-panel-title">MemOS 长期记忆</h4>
                <div class="af-row">
                  <span class="af-row-label">启用</span>
                  <el-switch v-model="agentForm.memos_enabled" size="small" />
                </div>
                <template v-if="agentForm.memos_enabled">
                  <div class="af-memos-fields">
                    <el-input v-model="agentForm.memos_config.api_key" show-password size="small" placeholder="API Key (mpg-...)" />
                    <el-input v-model="agentForm.memos_config.base_url" size="small" placeholder="Base URL（可选）" style="margin-top:8px" />
                  </div>
                </template>
              </div>

              <!-- API Token -->
              <div v-if="isEdit" class="af-panel">
                <h4 class="af-panel-title">API Token</h4>
                <div class="af-token-row">
                  <code v-if="agentForm.token" class="af-token-code">{{ agentForm.token }}</code>
                  <span v-else class="af-hint" style="margin:0">尚未生成</span>
                </div>
                <div class="af-token-actions">
                  <el-button v-if="agentForm.token" link type="primary" size="small" @click="copyToken(agentForm.token)">复制</el-button>
                  <el-popconfirm title="重置后旧 Token 将失效" @confirm="doResetToken">
                    <template #reference>
                      <el-button type="warning" link size="small">重置</el-button>
                    </template>
                  </el-popconfirm>
                </div>
              </div>

              <!-- 其他 -->
              <div class="af-panel">
                <h4 class="af-panel-title">其他</h4>
                <div class="af-row">
                  <span class="af-row-label">设为默认</span>
                  <el-switch v-model="agentForm.is_default" size="small" />
                </div>
                <div class="af-row-hint">未指定 Agent 的请求将使用此 Agent</div>
              </div>
            </div>
          </div>
        </el-form>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { agentApi, type Agent } from '@/api/agent'
import { providerApi, type Provider } from '@/api/provider'
import { toolApi, type Tool } from '@/api/tool'
import { useAgentStore } from '@/stores/agent'

const route = useRoute()
const router = useRouter()
const agentStore = useAgentStore()

const agentId = computed(() => {
  const id = route.params.id
  return id && id !== 'create' ? Number(id) : 0
})
const isEdit = computed(() => agentId.value > 0)

const agentLoading = ref(false)
const agentSaving = ref(false)
const providers = ref<Provider[]>([])
const allTools = ref<Tool[]>([])
const providerModels = ref<string[]>([])
const remoteModels = ref<string[]>([])
const remoteFetched = ref(false)
const remoteFetchError = ref('')
const modelLoading = ref(false)

const agentForm = ref({
  id: 0,
  name: '',
  description: '',
  system_prompt: '',
  provider_id: null as number | null,
  model_name: '',
  temperature: 0.7,
  max_tokens: 4096,
  timeout: 0,
  max_history: 30,
  max_iterations: 50,
  token_budget: 0,
  tool_search_enabled: false,
  memos_enabled: false,
  memos_config: { base_url: '', api_key: '', user_id: '', top_k: 10, async: true },
  tool_ids: [] as number[],
  token: '',
  is_default: false,
})

const noTemperaturePrefixes = ['o1-', 'o3-', 'o4-mini', 'gpt-5.', 'deepseek-reasoner']
const noTemperatureExact = new Set(['o1', 'o3', 'o4-mini'])

const isTemperatureDisabled = computed(() => {
  const m = agentForm.value.model_name
  if (!m) return false
  if (noTemperatureExact.has(m)) return true
  return noTemperaturePrefixes.some((p) => m.startsWith(p))
})

const localOnlyModels = computed(() => {
  const remoteSet = new Set(remoteModels.value)
  return providerModels.value.filter((m) => !remoteSet.has(m))
})

async function loadProviderModels(providerId: number) {
  if (!providerId) { providerModels.value = []; return }
  modelLoading.value = true
  try {
    const res: any = await providerApi.models(providerId)
    providerModels.value = res.data || []
  } catch {
    providerModels.value = []
  } finally {
    modelLoading.value = false
  }
}

async function onProviderChange(providerId: number) {
  agentForm.value.model_name = ''
  remoteModels.value = []
  remoteFetched.value = false
  remoteFetchError.value = ''
  await loadProviderModels(providerId)
}

async function onModelFocus() {
  if (!agentForm.value.provider_id || remoteFetched.value || modelLoading.value) return
  modelLoading.value = true
  remoteFetchError.value = ''
  try {
    const res: any = await providerApi.remoteModels(agentForm.value.provider_id)
    remoteModels.value = res.data || []
    remoteFetched.value = true
  } catch {
    remoteFetchError.value = '远程模型拉取失败，可手动输入模型名'
    remoteFetched.value = true
  } finally {
    modelLoading.value = false
  }
}

function applyAgentDetail(detail: Agent) {
  agentForm.value = {
    id: detail.id,
    name: detail.name || '',
    description: detail.description || '',
    system_prompt: detail.system_prompt || '',
    provider_id: detail.provider_id,
    model_name: detail.model_name || '',
    temperature: detail.temperature ?? 0.7,
    max_tokens: detail.max_tokens ?? 4096,
    timeout: detail.timeout ?? 0,
    max_history: detail.max_history ?? 30,
    max_iterations: detail.max_iterations ?? 50,
    token_budget: detail.token_budget ?? 0,
    tool_search_enabled: !!detail.tool_search_enabled,
    memos_enabled: !!detail.memos_enabled,
    memos_config: {
      base_url: detail.memos_config?.base_url || '',
      api_key: detail.memos_config?.api_key || '',
      user_id: detail.memos_config?.user_id || '',
      top_k: detail.memos_config?.top_k || 10,
      async: detail.memos_config?.async !== false,
    },
    tool_ids: detail.tool_ids || detail.tools?.map((t: any) => t.id) || [],
    token: detail.token || '',
    is_default: !!detail.is_default,
  }
}

async function reloadAgent() {
  agentLoading.value = true
  try {
    const [p, t] = await Promise.all([
      providerApi.list({ page: 1, page_size: 100 }),
      toolApi.list({ page: 1, page_size: 500 }),
    ])
    providers.value = (p as any).data?.list || []
    allTools.value = (t as any).data?.list || []
    if (isEdit.value) {
      const res = await agentApi.getById(agentId.value)
      const detail = (res as any).data as Agent
      if (detail) {
        applyAgentDetail(detail)
        if (detail.provider_id) await loadProviderModels(detail.provider_id)
      }
    }
  } catch {
    ElMessage.error('加载失败')
  } finally {
    agentLoading.value = false
  }
}

async function saveAgent() {
  if (!agentForm.value.name) {
    ElMessage.warning('请填写名称')
    return
  }
  agentSaving.value = true
  try {
    const payload = {
      name: agentForm.value.name,
      description: agentForm.value.description,
      system_prompt: agentForm.value.system_prompt,
      provider_id: agentForm.value.provider_id ?? undefined,
      model_name: agentForm.value.model_name,
      temperature: agentForm.value.temperature,
      max_tokens: agentForm.value.max_tokens,
      timeout: agentForm.value.timeout,
      max_history: agentForm.value.max_history,
      max_iterations: agentForm.value.max_iterations,
      token_budget: agentForm.value.token_budget,
      tool_search_enabled: agentForm.value.tool_search_enabled,
      memos_enabled: agentForm.value.memos_enabled,
      memos_config: agentForm.value.memos_config,
      tool_ids: agentForm.value.tool_search_enabled ? [] : agentForm.value.tool_ids,
      is_default: agentForm.value.is_default,
    }
    if (isEdit.value) {
      await agentApi.updateById(agentId.value, payload)
      ElMessage.success('已保存')
    } else {
      await agentApi.create(payload)
      ElMessage.success('已创建')
    }
    await agentStore.loadAgents()
    router.push('/agents')
  } finally {
    agentSaving.value = false
  }
}

async function doResetToken() {
  try {
    const res: any = await agentApi.resetTokenById(agentId.value)
    agentForm.value.token = res.data?.token || ''
    ElMessage.success('Token 已重置')
  } catch {
    ElMessage.error('重置失败')
  }
}

function copyToken(t: string) {
  navigator.clipboard.writeText(t).then(() => ElMessage.success('已复制'))
}

function onToolSearchChange(en: boolean) {
  if (en) agentForm.value.tool_ids = []
}

onMounted(() => reloadAgent())
</script>

<style scoped>
.aic-sub-compact { margin-top: 6px; font-size: 13px; line-height: 1.45; }

/* ===== 双栏布局 ===== */
.af-layout { min-height: 120px; }

.af-layout-inner {
  display: flex;
  gap: 24px;
  align-items: flex-start;
}

.af-main {
  flex: 1;
  min-width: 0;
}

.af-sidebar {
  width: 340px;
  flex-shrink: 0;
}

.af-sidebar-inner {
  position: sticky;
  top: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* ===== 右栏面板 ===== */
.af-panel {
  padding: 14px 16px;
  border-radius: 10px;
  border: 1px solid var(--el-border-color-lighter);
  background: var(--el-fill-color-blank);
}

.af-panel-title {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--el-text-color-secondary);
  margin: 0 0 12px;
}

/* ===== 参数字段 ===== */
.af-field {
  margin-bottom: 10px;
}

.af-field:last-child { margin-bottom: 0; }

.af-field-label {
  display: block;
  font-size: 12px;
  color: var(--el-text-color-regular);
  margin-bottom: 4px;
}

.af-field-sub {
  color: var(--el-text-color-secondary);
  font-weight: normal;
}

.af-field-num { width: 100%; }

.af-field-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  margin-bottom: 10px;
}

/* ===== 滑块行 ===== */
.af-slider-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.af-slider { flex: 1; min-width: 0; }

.af-slider-val {
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--el-text-color-primary);
  min-width: 28px;
  text-align: right;
}

/* ===== Switch 行 ===== */
.af-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 28px;
}

.af-row-label {
  font-size: 13px;
  color: var(--el-text-color-regular);
}

.af-row-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 4px;
  line-height: 1.35;
}

/* ===== MemOS ===== */
.af-memos-fields { margin-top: 10px; }

/* ===== Token ===== */
.af-token-row { margin-bottom: 6px; }

.af-token-code {
  display: block;
  font-family: ui-monospace, 'SF Mono', monospace;
  font-size: 11px;
  background: var(--aic-sub-code-bg, var(--el-fill-color-light));
  color: var(--aic-sub-code-color, var(--el-text-color-primary));
  padding: 6px 8px;
  border-radius: 6px;
  word-break: break-all;
  line-height: 1.5;
}

.af-token-actions {
  display: flex;
  gap: 8px;
}

/* ===== System Prompt ===== */
.af-prompt-input :deep(.el-textarea__inner) {
  font-size: 13px;
  line-height: 1.6;
}

/* ===== 操作栏 ===== */
.af-actions {
  position: sticky;
  bottom: -1px;
  z-index: 3;
  display: flex;
  gap: 12px;
  margin-top: 4px;
  padding: 14px 0 4px;
  background: linear-gradient(to top, var(--aic-app-main-bg, var(--el-bg-color)) 70%, transparent);
  border-top: 1px solid var(--el-border-color-lighter);
}

/* ===== 提示文字 ===== */
.af-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 4px;
  line-height: 1.4;
}

.af-hint.warn { color: #e6a23c; }

/* ===== 表单微调 ===== */
.af-main :deep(.el-form-item) { margin-bottom: 16px; }
.af-main :deep(.el-form-item__label) { font-size: 13px; font-weight: 500; padding-bottom: 4px; }

.ml8 { margin-left: 8px; }

/* ===== 响应式 ===== */
@media (max-width: 920px) {
  .af-layout-inner {
    flex-direction: column;
  }
  .af-sidebar {
    width: 100%;
  }
  .af-sidebar-inner {
    position: static;
    flex-direction: row;
    flex-wrap: wrap;
  }
  .af-panel {
    flex: 1;
    min-width: 260px;
  }
}
</style>
