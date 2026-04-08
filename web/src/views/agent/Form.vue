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
      <el-form :model="agentForm" label-position="top" class="af-root" v-loading="agentLoading">
        <div class="af-cols">
          <!-- ====== 左栏：主编辑区 ====== -->
          <div class="af-left">
            <div class="af-row-inline">
              <el-form-item label="名称" required class="af-cell af-cell--name">
                <el-input v-model="agentForm.name" placeholder="显示名称" />
              </el-form-item>
              <el-form-item label="描述" class="af-cell af-cell--desc">
                <el-input v-model="agentForm.description" placeholder="简要描述用途（可选）" />
              </el-form-item>
            </div>

            <div class="af-row-inline">
              <el-form-item label="供应商" required class="af-cell">
                <el-select v-model="agentForm.provider_id" filterable style="width:100%" @change="onProviderChange" placeholder="选择">
                  <el-option v-for="p in providers" :key="p.id" :label="p.name" :value="p.id">
                    <span>{{ p.name }}</span>
                    <el-tag size="small" style="margin-left:8px" type="info">{{ p.type }}</el-tag>
                  </el-option>
                </el-select>
              </el-form-item>
              <el-form-item label="模型" required class="af-cell">
                <el-select v-model="agentForm.model_name" filterable allow-create default-first-option style="width:100%" :loading="modelLoading" :disabled="!agentForm.provider_id" @focus="onModelFocus" placeholder="选择或输入">
                  <el-option-group v-if="remoteModels.length > 0" label="远程模型">
                    <el-option v-for="m in remoteModels" :key="'r-'+m" :label="m" :value="m" />
                  </el-option-group>
                  <el-option-group v-if="localOnlyModels.length > 0" :label="remoteModels.length ? '本地配置' : '模型列表'">
                    <el-option v-for="m in localOnlyModels" :key="'l-'+m" :label="m" :value="m" />
                  </el-option-group>
                </el-select>
                <div v-if="remoteFetchError" class="af-hint warn">{{ remoteFetchError }}</div>
              </el-form-item>
            </div>

            <el-form-item label="System Prompt">
              <el-input v-model="agentForm.system_prompt" type="textarea" :autosize="{ minRows: 10, maxRows: 28 }" placeholder="为 Agent 编写系统提示词" class="af-prompt" />
            </el-form-item>

            <div class="af-actions">
              <el-button type="primary" :loading="agentSaving" @click="saveAgent">{{ isEdit ? '保存' : '创建' }}</el-button>
              <el-button @click="$router.push('/agents')">取消</el-button>
            </div>
          </div>

          <!-- ====== 右栏：配置面板 ====== -->
          <div class="af-right">
            <div class="af-right-inner">
              <!-- 模型参数 -->
              <section class="af-card">
                <h4 class="af-card-head">模型参数</h4>
                <label class="af-kv">
                  <span class="af-kv-k">温度</span>
                  <div class="af-kv-v af-temp">
                    <el-slider v-model="agentForm.temperature" :min="0" :max="2" :step="0.1" :disabled="isTemperatureDisabled" class="af-temp-slider" />
                    <span class="af-temp-val">{{ agentForm.temperature.toFixed(1) }}</span>
                  </div>
                </label>
                <label class="af-kv">
                  <span class="af-kv-k">Max Tokens</span>
                  <el-input-number v-model="agentForm.max_tokens" :min="1" :max="128000" controls-position="right" size="small" class="af-kv-num" />
                </label>
                <div class="af-switch-line" style="margin-top:4px">
                  <span>深度思考</span>
                  <el-switch v-model="thinkingEnabled" size="small" :disabled="isAlwaysThinking" />
                </div>
                <div v-if="isAlwaysThinking" class="af-hint warn">推理模型，始终开启</div>
                <label v-if="thinkingEnabled || isAlwaysThinking" class="af-kv" style="margin-top:6px">
                  <span class="af-kv-k">推理强度</span>
                  <el-select v-model="agentForm.reasoning_effort" size="small" style="width:100%">
                    <el-option label="低 (Low)" value="low" />
                    <el-option label="中 (Medium)" value="medium" />
                    <el-option label="高 (High)" value="high" />
                  </el-select>
                </label>
                <div class="af-switch-line" style="margin-top:4px">
                  <span>联网搜索</span>
                  <el-switch v-model="agentForm.enable_web_search" size="small" :disabled="!supportsWebSearch" />
                </div>
                <div v-if="!supportsWebSearch" class="af-hint">当前模型不支持</div>
              </section>

              <!-- 工具 -->
              <section class="af-card">
                <h4 class="af-card-head">工具</h4>
                <div class="af-switch-line">
                  <span>Tool Search</span>
                  <el-switch v-model="agentForm.tool_search_enabled" @change="onToolSearchChange" size="small" />
                </div>
                <div v-if="agentForm.tool_search_enabled" class="af-hint">开启后自动加载全部已启用工具</div>
                <el-select v-if="!agentForm.tool_search_enabled" v-model="agentForm.tool_ids" multiple filterable collapse-tags collapse-tags-tooltip style="width:100%;margin-top:8px" placeholder="选择工具" size="small">
                  <el-option v-for="t in allTools" :key="t.id" :label="t.name" :value="t.id" />
                </el-select>
              </section>

              <!-- 高级设置（可折叠） -->
              <section class="af-card af-card--fold">
                <h4 class="af-card-head af-card-head--toggle" @click="openSections.advanced = !openSections.advanced">
                  <span>高级设置</span>
                  <el-icon class="af-fold-arrow" :class="{ open: openSections.advanced }"><ArrowRight /></el-icon>
                </h4>
                <div v-show="openSections.advanced" class="af-card-body">
                  <div class="af-grid2">
                    <label class="af-kv">
                      <span class="af-kv-k">超时 (s)</span>
                      <el-input-number v-model="agentForm.timeout" :min="0" controls-position="right" size="small" class="af-kv-num" />
                    </label>
                    <label class="af-kv">
                      <span class="af-kv-k">历史条数</span>
                      <el-input-number v-model="agentForm.max_history" :min="1" :max="500" controls-position="right" size="small" class="af-kv-num" />
                    </label>
                    <label class="af-kv">
                      <span class="af-kv-k">最大迭代</span>
                      <el-input-number v-model="agentForm.max_iterations" :min="1" :max="200" controls-position="right" size="small" class="af-kv-num" />
                    </label>
                    <label class="af-kv">
                      <span class="af-kv-k">Token 预算 <span class="af-kv-sub">0=不限</span></span>
                      <el-input-number v-model="agentForm.token_budget" :min="0" :max="10000000" :step="10000" controls-position="right" size="small" class="af-kv-num" />
                    </label>
                  </div>
                  <div class="af-switch-line">
                    <span>设为默认 Agent</span>
                    <el-switch v-model="agentForm.is_default" size="small" />
                  </div>
                  <div class="af-hint">未指定 Agent 的请求将使用此 Agent</div>
                </div>
              </section>

              <!-- MemOS（可折叠） -->
              <section class="af-card af-card--fold">
                <h4 class="af-card-head af-card-head--toggle" @click="openSections.memos = !openSections.memos">
                  <span>MemOS 长期记忆</span>
                  <div style="display:flex;align-items:center;gap:6px">
                    <el-switch v-model="agentForm.memos_enabled" size="small" @click.stop />
                    <el-icon class="af-fold-arrow" :class="{ open: openSections.memos }"><ArrowRight /></el-icon>
                  </div>
                </h4>
                <div v-show="openSections.memos && agentForm.memos_enabled" class="af-card-body">
                  <el-input v-model="agentForm.memos_config.api_key" show-password size="small" placeholder="API Key (mpg-...)" />
                  <el-input v-model="agentForm.memos_config.base_url" size="small" placeholder="Base URL（可选）" style="margin-top:6px" />
                </div>
              </section>

              <!-- API Token（可折叠，仅编辑模式） -->
              <section v-if="isEdit" class="af-card af-card--fold">
                <h4 class="af-card-head af-card-head--toggle" @click="openSections.token = !openSections.token">
                  <span>API Token</span>
                  <el-icon class="af-fold-arrow" :class="{ open: openSections.token }"><ArrowRight /></el-icon>
                </h4>
                <div v-show="openSections.token" class="af-card-body">
                  <code v-if="agentForm.token" class="af-token">{{ agentForm.token }}</code>
                  <span v-else class="af-hint" style="margin:0">尚未生成</span>
                  <div class="af-token-btns">
                    <el-button v-if="agentForm.token" link type="primary" size="small" @click="copyToken(agentForm.token)">复制</el-button>
                    <el-popconfirm title="重置后旧 Token 将失效" @confirm="doResetToken">
                      <template #reference>
                        <el-button type="warning" link size="small">重置</el-button>
                      </template>
                    </el-popconfirm>
                  </div>
                </div>
              </section>
            </div>
          </div>
        </div>
      </el-form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { agentApi, defaultModelCaps, type Agent, type ModelCaps } from '@/api/agent'
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
  enable_thinking: true,
  reasoning_effort: 'medium',
  enable_web_search: false,
  tool_search_enabled: false,
  memos_enabled: false,
  memos_config: { base_url: '', api_key: '', user_id: '', top_k: 10, async: true },
  tool_ids: [] as number[],
  token: '',
  is_default: false,
})

const modelCaps = ref<ModelCaps>({ ...defaultModelCaps })

let capsAbort: AbortController | null = null
async function fetchModelCaps(model: string) {
  capsAbort?.abort()
  if (!model) { modelCaps.value = { ...defaultModelCaps }; return }
  capsAbort = new AbortController()
  try {
    const res: any = await agentApi.getModelCaps(model)
    modelCaps.value = res.data ?? { ...defaultModelCaps }
  } catch {
    modelCaps.value = { ...defaultModelCaps }
  }
}

watch(() => agentForm.value.model_name, (m) => fetchModelCaps(m))

const isTemperatureDisabled = computed(() => modelCaps.value.no_temperature)
const isAlwaysThinking = computed(() => modelCaps.value.always_thinking)
const supportsWebSearch = computed(() => modelCaps.value.web_search)

const thinkingEnabled = computed({
  get: () => agentForm.value.enable_thinking,
  set: (v: boolean) => { agentForm.value.enable_thinking = v },
})

const openSections = ref({ advanced: false, memos: false, token: false })

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
    enable_thinking: detail.enable_thinking !== false,
    reasoning_effort: detail.reasoning_effort || 'medium',
    enable_web_search: !!detail.enable_web_search,
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
  if (!agentForm.value.name) { ElMessage.warning('请填写名称'); return }
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
      enable_thinking: agentForm.value.enable_thinking,
      reasoning_effort: agentForm.value.reasoning_effort,
      enable_web_search: agentForm.value.enable_web_search,
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

/* ===== 双栏骨架 ===== */
.af-root { min-height: 120px; }

.af-cols {
  display: flex;
  gap: 28px;
  align-items: flex-start;
}

.af-left {
  flex: 1;
  min-width: 0;
}

.af-right {
  width: 380px;
  flex-shrink: 0;
}

.af-right-inner {
  position: sticky;
  top: 16px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* ===== 左栏：行内排列 ===== */
.af-row-inline {
  display: flex;
  gap: 14px;
  margin-bottom: 16px;
}

.af-cell { flex: 1; min-width: 0; margin-bottom: 0 !important; }
.af-cell--name { max-width: 220px; flex: 0 0 220px; }
.af-cell--desc { flex: 1; }

.af-left :deep(.el-form-item) { margin-bottom: 16px; }
.af-left :deep(.el-form-item__label) { font-size: 13px; font-weight: 500; padding-bottom: 4px; }

.af-prompt :deep(.el-textarea__inner) { font-size: 13px; line-height: 1.6; }

/* ===== 操作栏 ===== */
.af-actions {
  position: sticky; bottom: -1px; z-index: 3;
  display: flex; gap: 12px;
  padding: 14px 0 4px;
  background: linear-gradient(to top, var(--aic-app-main-bg, var(--el-bg-color)) 70%, transparent);
  border-top: 1px solid var(--el-border-color-lighter);
}

/* ===== 右栏卡片 ===== */
.af-card {
  padding: 14px 16px;
  border-radius: 10px;
  border: 1px solid var(--el-border-color-lighter);
  background: var(--el-fill-color-blank);
}

.af-card-head {
  font-size: 11px; font-weight: 600;
  letter-spacing: 0.06em; text-transform: uppercase;
  color: var(--el-text-color-secondary);
  margin: 0 0 12px;
}

.af-card--fold > .af-card-head { margin-bottom: 0; }
.af-card--fold > .af-card-body { margin-top: 12px; }

.af-card-head--toggle {
  display: flex; align-items: center; justify-content: space-between;
  cursor: pointer; user-select: none;
}

.af-fold-arrow {
  font-size: 12px; transition: transform 0.2s;
  color: var(--el-text-color-placeholder);
}
.af-fold-arrow.open { transform: rotate(90deg); }

/* ===== KV 字段 ===== */
.af-kv { display: block; margin-bottom: 10px; }
.af-kv:last-child { margin-bottom: 0; }

.af-kv-k {
  display: block; font-size: 12px;
  color: var(--el-text-color-regular);
  margin-bottom: 4px;
}

.af-kv-sub { color: var(--el-text-color-secondary); font-weight: normal; }
.af-kv-num { width: 100%; }

.af-grid2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  margin-bottom: 10px;
}

.af-grid2 .af-kv { margin-bottom: 0; }

/* ===== 温度 ===== */
.af-temp { display: flex; align-items: center; gap: 10px; }
.af-temp-slider { flex: 1; min-width: 0; }
.af-temp-val {
  font-size: 13px; font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--el-text-color-primary);
  min-width: 28px; text-align: right;
}

/* ===== Switch 行 ===== */
.af-switch-line {
  display: flex; align-items: center;
  justify-content: space-between;
  font-size: 13px; color: var(--el-text-color-regular);
  min-height: 28px;
}

/* ===== Token ===== */
.af-token {
  display: block;
  font-family: ui-monospace, 'SF Mono', monospace;
  font-size: 11px;
  background: var(--aic-sub-code-bg, var(--el-fill-color-light));
  color: var(--aic-sub-code-color, var(--el-text-color-primary));
  padding: 6px 8px; border-radius: 6px;
  word-break: break-all; line-height: 1.5;
  margin-bottom: 6px;
}

.af-token-btns { display: flex; gap: 8px; }

/* ===== 提示 ===== */
.af-hint {
  font-size: 12px; color: var(--el-text-color-secondary);
  margin-top: 4px; line-height: 1.35;
}
.af-hint.warn { color: #e6a23c; }

/* ===== 响应式 ===== */
@media (max-width: 960px) {
  .af-cols { flex-direction: column; }
  .af-right { width: 100%; }
  .af-right-inner {
    position: static;
    flex-direction: row; flex-wrap: wrap;
  }
  .af-card { flex: 1; min-width: 260px; }
  .af-cell--name { max-width: none; flex: 1; }
}
</style>
