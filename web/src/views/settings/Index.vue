<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">设置</h1>
      <p class="aic-sub aic-sub-compact">
        Agent：模型、提示词、上下文与关联工具等配置。
      </p>
    </div>

    <div class="aic-page-body">
      <div class="agent-pane" v-loading="agentLoading">
        <el-form
          :model="agentForm"
          label-width="100px"
          class="agent-form agent-form-grid"
          label-position="right"
        >
          <el-row :gutter="20">
            <el-col :xs="24" :lg="15">
              <el-form-item label="名称">
                <el-input v-model="agentForm.name" placeholder="显示名称" />
              </el-form-item>
              <el-form-item label="描述">
                <el-input
                  v-model="agentForm.description"
                  type="textarea"
                  :rows="2"
                  :autosize="{ minRows: 2, maxRows: 4 }"
                />
              </el-form-item>
              <el-form-item label="供应商" required>
                <el-select
                  v-model="agentForm.provider_id"
                  filterable
                  style="width: 100%"
                  @change="onProviderChange"
                >
                  <el-option
                    v-for="p in providers"
                    :key="p.id"
                    :label="p.name"
                    :value="p.id"
                  >
                    <span>{{ p.name }}</span>
                    <el-tag size="small" class="ml8" type="info">{{
                      p.type
                    }}</el-tag>
                  </el-option>
                </el-select>
              </el-form-item>
              <el-form-item label="模型" required>
                <el-select
                  v-model="agentForm.model_name"
                  filterable
                  allow-create
                  default-first-option
                  style="width: 100%"
                  :loading="modelLoading"
                  :disabled="!agentForm.provider_id"
                  @focus="onModelFocus"
                >
                  <el-option-group
                    v-if="remoteModels.length > 0"
                    label="远程模型"
                  >
                    <el-option
                      v-for="m in remoteModels"
                      :key="'r-' + m"
                      :label="m"
                      :value="m"
                    />
                  </el-option-group>
                  <el-option-group
                    v-if="localOnlyModels.length > 0"
                    :label="remoteModels.length ? '本地配置' : '模型列表'"
                  >
                    <el-option
                      v-for="m in localOnlyModels"
                      :key="'l-' + m"
                      :label="m"
                      :value="m"
                    />
                  </el-option-group>
                </el-select>
                <div v-if="remoteFetchError" class="hint warn">
                  {{ remoteFetchError }}
                </div>
              </el-form-item>
              <el-form-item
                label="System Prompt"
                class="agent-form-item--prompt"
              >
                <el-input
                  v-model="agentForm.system_prompt"
                  type="textarea"
                  :autosize="{ minRows: 4, maxRows: 14 }"
                  placeholder="系统提示词"
                />
              </el-form-item>
            </el-col>
            <el-col :xs="24" :lg="9">
              <div class="agent-side-block">
                <div class="agent-side-title">推理与上下文</div>
                <el-form-item label="温度">
                  <el-slider
                    v-model="agentForm.temperature"
                    :min="0"
                    :max="2"
                    :step="0.1"
                    show-input
                    size="small"
                    :disabled="isTemperatureDisabled"
                  />
                  <div v-if="isTemperatureDisabled" class="hint warn">
                    当前模型可能不支持温度
                  </div>
                </el-form-item>
                <el-row :gutter="12">
                  <el-col :span="12">
                    <el-form-item label="Max Tokens" label-width="92px">
                      <el-input-number
                        v-model="agentForm.max_tokens"
                        :min="1"
                        :max="128000"
                        controls-position="right"
                        style="width: 100%"
                      />
                    </el-form-item>
                  </el-col>
                  <el-col :span="12">
                    <el-form-item label="超时(s)" label-width="72px">
                      <el-input-number
                        v-model="agentForm.timeout"
                        :min="0"
                        controls-position="right"
                        style="width: 100%"
                      />
                      <div class="hint">0 不限制</div>
                    </el-form-item>
                  </el-col>
                </el-row>
                <el-row :gutter="12">
                  <el-col :span="12">
                    <el-form-item label="历史条数" label-width="92px">
                      <el-input-number
                        v-model="agentForm.max_history"
                        :min="1"
                        :max="500"
                        controls-position="right"
                        style="width: 100%"
                      />
                    </el-form-item>
                  </el-col>
                  <el-col :span="12">
                    <el-form-item label="最大迭代" label-width="72px">
                      <el-input-number
                        v-model="agentForm.max_iterations"
                        :min="1"
                        :max="200"
                        controls-position="right"
                        style="width: 100%"
                      />
                    </el-form-item>
                  </el-col>
                </el-row>
              </div>
              <div class="agent-side-block">
                <div class="agent-side-title">API Token</div>
                <el-form-item label="Token" label-width="52px">
                  <div class="token-row token-row--compact">
                    <code v-if="agentForm.token" class="token-code">{{
                      agentForm.token
                    }}</code>
                    <span v-else class="hint">尚未生成</span>
                    <el-button
                      v-if="agentForm.token"
                      link
                      type="primary"
                      size="small"
                      @click="copyToken(agentForm.token)"
                      >复制</el-button
                    >
                    <el-popconfirm
                      title="重置后旧 Token 将失效"
                      @confirm="doResetToken"
                    >
                      <template #reference>
                        <el-button type="warning" link size="small"
                          >重置</el-button
                        >
                      </template>
                    </el-popconfirm>
                  </div>
                  <div class="hint">Bearer <code>ag-…</code></div>
                </el-form-item>
              </div>
              <div class="agent-side-block">
                <div class="agent-side-title">MemOS</div>
                <el-form-item label="启用" label-width="52px">
                  <el-switch v-model="agentForm.memos_enabled" />
                </el-form-item>
                <template v-if="agentForm.memos_enabled">
                  <el-form-item label="API Key" label-width="72px">
                    <el-input
                      v-model="agentForm.memos_config.api_key"
                      show-password
                      size="small"
                      placeholder="mpg-..."
                    />
                  </el-form-item>
                  <el-form-item label="Base URL" label-width="72px">
                    <el-input
                      v-model="agentForm.memos_config.base_url"
                      size="small"
                      placeholder="可选"
                    />
                  </el-form-item>
                </template>
              </div>
              <div class="agent-side-block">
                <div class="agent-side-title">工具</div>
                <el-form-item label="Tool Search" label-width="92px">
                  <el-switch
                    v-model="agentForm.tool_search_enabled"
                    @change="onToolSearchChange"
                  />
                  <div class="hint">开则加载全部已启用工具</div>
                </el-form-item>
                <el-form-item
                  v-if="!agentForm.tool_search_enabled"
                  label="关联"
                  label-width="52px"
                >
                  <el-select
                    v-model="agentForm.tool_ids"
                    multiple
                    filterable
                    collapse-tags
                    collapse-tags-tooltip
                    style="width: 100%"
                    placeholder="选择工具"
                    size="small"
                  >
                    <el-option
                      v-for="t in allTools"
                      :key="t.id"
                      :label="t.name"
                      :value="t.id"
                    />
                  </el-select>
                </el-form-item>
              </div>
            </el-col>
          </el-row>
          <div class="agent-form-actions">
            <el-button type="primary" :loading="agentSaving" @click="saveAgent"
              >保存 Agent</el-button
            >
            <el-button @click="reloadAgent">重新加载</el-button>
          </div>
        </el-form>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from "vue";
import { ElMessage } from "element-plus";
import { agentApi, type Agent } from "@/api/agent";
import { providerApi, type Provider } from "@/api/provider";
import { toolApi, type Tool } from "@/api/tool";

const agentLoading = ref(false);
const agentSaving = ref(false);
const providers = ref<Provider[]>([]);
const allTools = ref<Tool[]>([]);
const providerModels = ref<string[]>([]);
const remoteModels = ref<string[]>([]);
const remoteFetched = ref(false);
const remoteFetchError = ref("");
const modelLoading = ref(false);

const agentForm = ref({
  id: 0,
  name: "",
  description: "",
  system_prompt: "",
  provider_id: null as number | null,
  model_name: "",
  temperature: 0.7,
  max_tokens: 4096,
  timeout: 0,
  max_history: 30,
  max_iterations: 50,
  tool_search_enabled: false,
  memos_enabled: false,
  memos_config: {
    base_url: "",
    api_key: "",
    user_id: "",
    top_k: 10,
    async: true,
  },
  tool_ids: [] as number[],
  token: "",
});

const noTemperaturePrefixes = [
  "o1-",
  "o3-",
  "o4-mini",
  "gpt-5.",
  "deepseek-reasoner",
];
const noTemperatureExact = new Set([
  "o1",
  "o3",
  "o4-mini",
  "gpt-5.4",
  "gpt-5.2",
]);

const isTemperatureDisabled = computed(() => {
  const m = agentForm.value.model_name;
  if (!m) return false;
  if (noTemperatureExact.has(m)) return true;
  return noTemperaturePrefixes.some((p) => m.startsWith(p));
});

const localOnlyModels = computed(() => {
  const remoteSet = new Set(remoteModels.value);
  return providerModels.value.filter((m) => !remoteSet.has(m));
});

async function loadProviderModels(providerId: number) {
  if (!providerId) {
    providerModels.value = [];
    return;
  }
  modelLoading.value = true;
  try {
    const res: any = await providerApi.models(providerId);
    providerModels.value = res.data || [];
  } catch {
    providerModels.value = [];
  } finally {
    modelLoading.value = false;
  }
}

async function onProviderChange(providerId: number) {
  agentForm.value.model_name = "";
  remoteModels.value = [];
  remoteFetched.value = false;
  remoteFetchError.value = "";
  await loadProviderModels(providerId);
}

async function onModelFocus() {
  if (!agentForm.value.provider_id || remoteFetched.value || modelLoading.value)
    return;
  modelLoading.value = true;
  remoteFetchError.value = "";
  try {
    const res: any = await providerApi.remoteModels(
      agentForm.value.provider_id,
    );
    remoteModels.value = res.data || [];
    remoteFetched.value = true;
  } catch {
    remoteFetchError.value = "远程模型拉取失败，可手动输入模型名";
    remoteFetched.value = true;
  } finally {
    modelLoading.value = false;
  }
}

function applyAgentDetail(detail: Agent) {
  agentForm.value = {
    id: detail.id,
    name: detail.name || "",
    description: detail.description || "",
    system_prompt: detail.system_prompt || "",
    provider_id: detail.provider_id,
    model_name: detail.model_name || "",
    temperature: detail.temperature ?? 0.7,
    max_tokens: detail.max_tokens ?? 4096,
    timeout: detail.timeout ?? 0,
    max_history: detail.max_history ?? 30,
    max_iterations: detail.max_iterations ?? 50,
    tool_search_enabled: !!detail.tool_search_enabled,
    memos_enabled: !!detail.memos_enabled,
    memos_config: {
      base_url: detail.memos_config?.base_url || "",
      api_key: detail.memos_config?.api_key || "",
      user_id: detail.memos_config?.user_id || "",
      top_k: detail.memos_config?.top_k || 10,
      async: detail.memos_config?.async !== false,
    },
    tool_ids: detail.tools?.map((t: any) => t.id) || [],
    token: detail.token || "",
  };
}

async function reloadAgent() {
  agentLoading.value = true;
  try {
    const [p, t, res] = await Promise.all([
      providerApi.list({ page: 1, page_size: 100 }),
      toolApi.list({ page: 1, page_size: 500 }),
      agentApi.get(),
    ]);
    providers.value = (p as any).data?.list || [];
    allTools.value = (t as any).data?.list || [];
    const detail = (res as any).data as Agent;
    if (detail) {
      applyAgentDetail(detail);
      if (detail.provider_id) await loadProviderModels(detail.provider_id);
    }
  } catch {
    ElMessage.error("加载 Agent 失败");
  } finally {
    agentLoading.value = false;
  }
}

async function saveAgent() {
  agentSaving.value = true;
  try {
    await agentApi.update({
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
      tool_search_enabled: agentForm.value.tool_search_enabled,
      memos_enabled: agentForm.value.memos_enabled,
      memos_config: agentForm.value.memos_config,
      tool_ids: agentForm.value.tool_search_enabled
        ? []
        : agentForm.value.tool_ids,
    });
    ElMessage.success("已保存");
    await reloadAgent();
  } finally {
    agentSaving.value = false;
  }
}

async function doResetToken() {
  try {
    const res: any = await agentApi.resetToken();
    agentForm.value.token = res.data?.token || "";
    ElMessage.success("Token 已重置");
  } catch {
    ElMessage.error("重置失败");
  }
}

function copyToken(t: string) {
  navigator.clipboard.writeText(t).then(() => ElMessage.success("已复制"));
}

function onToolSearchChange(en: boolean) {
  if (en) agentForm.value.tool_ids = [];
}

onMounted(() => reloadAgent());
</script>

<style scoped>
.aic-sub-compact {
  margin-top: 6px;
  font-size: 13px;
  line-height: 1.45;
  max-width: 960px;
}
.agent-pane {
  padding: 0;
  min-height: 120px;
}
.agent-form {
  max-width: 100%;
}
.agent-form-grid :deep(.el-form-item) {
  margin-bottom: 14px;
}
.agent-form-grid :deep(.el-form-item__label) {
  padding-right: 8px;
}
.agent-form-item--prompt :deep(.el-textarea__inner) {
  font-size: 13px;
  line-height: 1.5;
}
.agent-side-block {
  padding: 12px 14px;
  margin-bottom: 12px;
  border-radius: 10px;
  border: 1px solid var(--el-border-color-lighter);
  background: var(--el-fill-color-blank);
}
.agent-side-title {
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--el-text-color-secondary);
  margin: 0 0 10px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.agent-form-actions {
  position: sticky;
  bottom: -1px;
  z-index: 3;
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin: 8px -4px 0;
  padding: 14px 4px 4px;
  background: linear-gradient(
    to top,
    var(--aic-app-main-bg, var(--el-bg-color)) 70%,
    transparent
  );
  border-top: 1px solid var(--el-border-color-lighter);
}
.hint {
  font-size: 12px;
  color: var(--aic-sidebar-muted, var(--el-text-color-secondary));
  margin-top: 4px;
  line-height: 1.35;
}
.hint.warn {
  color: #e6a23c;
}
.ml8 {
  margin-left: 8px;
}
.token-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.token-row--compact {
  gap: 6px;
}
.token-code {
  font-family: ui-monospace, monospace;
  font-size: 11px;
  background: var(--aic-sub-code-bg, var(--el-fill-color-light));
  color: var(--aic-sub-code-color, var(--el-text-color-primary));
  padding: 5px 8px;
  border-radius: 6px;
  word-break: break-all;
  flex: 1;
  min-width: 0;
  max-width: 100%;
}
@media (max-width: 991px) {
  .agent-side-block {
    margin-top: 8px;
  }
}
</style>
