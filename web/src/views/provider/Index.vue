<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">{{ i18n.t('providers.title') }}</h1>
      <p class="aic-sub">{{ i18n.t('providers.subtitle') }}</p>
    </div>
    <div class="aic-page-body">
    <el-card class="aic-card" shadow="never">
      <template #header>
        <div class="aic-card-header">
          <span class="aic-card-title">{{ i18n.t('providers.list') }}</span>
          <div>
            <el-input
              v-model="keyword"
              :placeholder="i18n.t('common.search')"
              clearable
              style="width: 200px; margin-right: 12px"
              @clear="loadData"
              @keyup.enter="loadData"
            >
              <template #prefix
                ><el-icon><Search /></el-icon
              ></template>
            </el-input>
            <el-button
              type="primary"
              @click="openDialog()"
            >
              <el-icon><Plus /></el-icon> {{ i18n.t('common.add') }}
            </el-button>
          </div>
        </div>
      </template>

      <el-table :data="list" v-loading="loading" stripe>
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="name" :label="i18n.t('common.name')" min-width="120" />
        <el-table-column prop="type" :label="i18n.t('common.type')" width="120">
          <template #default="{ row }">
            <el-tag>{{ row.type }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="base_url"
          label="Base URL"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column :label="i18n.t('providers.modelCount')" width="80">
          <template #default="{ row }">
            <el-tag type="info" size="small">{{
              (row.models || []).length
            }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="enabled" :label="i18n.t('common.status')" width="80">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'danger'" size="small">{{
              row.enabled ? i18n.t('common.enabled') : i18n.t('common.disabled')
            }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" :label="i18n.t('common.createdAt')" width="180" />
        <el-table-column
          :label="i18n.t('common.actions')"
          width="160"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button link type="primary" @click="openDialog(row)"
              >{{ i18n.t('common.edit') }}</el-button
            >
            <el-popconfirm :title="i18n.isEnglish ? 'Delete this provider?' : '确定删除？'" @confirm="handleDelete(row.id)">
              <template #reference>
                <el-button link type="danger">{{ i18n.t('common.delete') }}</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        style="margin-top: 16px; justify-content: flex-end"
        @size-change="loadData"
        @current-change="loadData"
      />
    </el-card>
    </div>

    <el-dialog
      v-model="dialogVisible"
      :title="form.id ? (i18n.isEnglish ? 'Edit Provider' : '编辑供应商') : (i18n.isEnglish ? 'Add Provider' : '新增供应商')"
      width="600px"
      destroy-on-close
    >
      <el-form :model="form" label-width="100px">
        <el-form-item :label="i18n.t('common.name')" required>
          <el-input v-model="form.name" placeholder="如：OpenAI Production" />
        </el-form-item>
        <el-form-item :label="i18n.t('common.type')" required>
          <el-select
            v-model="form.type"
            :placeholder="i18n.isEnglish ? 'Select type' : '选择类型'"
            style="width: 100%"
            @change="onTypeChange"
          >
            <el-option
              v-for="t in providerTypes"
              :key="t.value"
              :label="t.label"
              :value="t.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="Base URL">
          <el-input v-model="form.base_url" placeholder="留空则使用默认地址" />
        </el-form-item>
        <el-form-item label="API Key" required>
          <el-input
            v-model="form.api_key"
            type="password"
            show-password
            placeholder="sk-..."
          />
        </el-form-item>
        <el-form-item :label="i18n.isEnglish ? 'Models' : '模型列表'">
          <div style="width: 100%">
            <div class="model-tags">
              <el-tag
                v-for="m in form.models"
                :key="m"
                closable
                size="default"
                @close="removeModel(m)"
                style="margin: 0 4px 4px 0"
                >{{ m }}</el-tag
              >
            </div>
            <div style="display: flex; gap: 8px; margin-top: 4px">
              <el-autocomplete
                v-model="newModelName"
                :fetch-suggestions="suggestModels"
                :placeholder="i18n.isEnglish ? 'Enter model name, press Enter to add' : '输入模型名称, 回车添加'"
                clearable
                style="flex: 1"
                @keyup.enter="addModel"
                @select="handleModelSelect"
                @focus="onAutocompleteFocus"
              />
              <el-button @click="addModel" :disabled="!newModelName.trim()"
                >{{ i18n.t('common.add') }}</el-button
              >
              <el-button
                @click="fetchRemoteModelsForProvider"
                :loading="remoteFetching"
                :disabled="!form.api_key"
                title="从 API 拉取模型列表"
              >
                <el-icon><Refresh /></el-icon>
              </el-button>
            </div>
            <div style="margin-top: 4px; font-size: 12px; color: #909399">
              {{ i18n.isEnglish ? 'Enter a model name and press Enter or click Add. Use refresh to fetch remote models from the API.' : '输入模型名称后按回车或点击添加；点击刷新按钮从 API 拉取远程模型列表' }}
            </div>
            <div
              v-if="remoteFetchMsg"
              style="margin-top: 2px; font-size: 12px"
              :style="{ color: remoteFetchOk ? '#67c23a' : '#E6A23C' }"
            >
              {{ remoteFetchMsg }}
            </div>
          </div>
        </el-form-item>
        <el-form-item :label="i18n.t('common.enabled')">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ i18n.t('common.cancel') }}</el-button>
        <el-button type="primary" @click="handleSubmit" :loading="submitting"
          >{{ i18n.t('common.confirm') }}</el-button
        >
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from "vue";
import { ElMessage } from "element-plus";
import { providerApi, type Provider } from "../../api/provider";
import { useI18nStore } from "../../stores/i18n";

const i18n = useI18nStore();
const list = ref<Provider[]>([]);
const loading = ref(false);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);
const keyword = ref("");

const dialogVisible = ref(false);
const submitting = ref(false);
const form = ref<any>({});
const newModelName = ref("");
const remoteModelsList = ref<string[]>([]);
const remoteFetching = ref(false);
const remoteFetchedDone = ref(false);
const remoteFetchMsg = ref("");
const remoteFetchOk = ref(false);

const providerTypes = [
  { label: "OpenAI", value: "openai" },
  { label: "Claude (Anthropic)", value: "claude" },
  { label: "Gemini (Google)", value: "gemini" },
  { label: "通义千问 (Qwen)", value: "qwen" },
  { label: "Kimi (Moonshot)", value: "kimi" },
  { label: "OpenRouter", value: "openrouter" },
  { label: "OpenAI API Compatible", value: "openai-compatible" },
];

const defaultModels: Record<string, string[]> = {
  openai: [
    "gpt-4o",
    "gpt-4o-mini",
    "gpt-4-turbo",
    "gpt-4",
    "gpt-5.2",
    "o1",
    "o1-mini",
    "o3-mini",
  ],
  claude: [
    "claude-sonnet-4-20250514",
    "claude-3-7-sonnet-20250219",
    "claude-3-5-haiku-20241022",
    "claude-3-5-sonnet-20241022",
    "claude-3-opus-20240229",
  ],
  gemini: [
    "gemini-2.5-pro-preview-06-05",
    "gemini-2.5-flash-preview-05-20",
    "gemini-2.0-flash",
    "gemini-2.0-flash-lite",
    "gemini-1.5-pro",
    "gemini-1.5-flash",
  ],
  qwen: [
    "qwen-max",
    "qwen-plus",
    "qwen-turbo",
    "qwen-long",
    "qwen-vl-max",
    "qwen-vl-plus",
  ],
  kimi: ["moonshot-v1-128k", "moonshot-v1-32k", "moonshot-v1-8k"],
  openrouter: [
    "anthropic/claude-sonnet-4-20250514",
    "openai/gpt-4o",
    "openai/gpt-4o-mini",
    "google/gemini-2.0-flash-001",
    "google/gemini-2.5-pro-preview",
    "deepseek/deepseek-chat-v3-0324",
    "deepseek/deepseek-r1",
    "meta-llama/llama-3.3-70b-instruct",
  ],
  "openai-compatible": [],
};

async function loadData() {
  loading.value = true;
  try {
    const res: any = await providerApi.list({
      page: page.value,
      page_size: pageSize.value,
      keyword: keyword.value,
    });
    list.value = res.data?.list || [];
    total.value = res.data?.total || 0;
  } finally {
    loading.value = false;
  }
}

function openDialog(row?: Provider) {
  if (row) {
    const existingModels =
      Array.isArray(row.models) && row.models.length > 0
        ? [...row.models]
        : [...(defaultModels[row.type as string] || [])];
    form.value = { ...row, models: existingModels };
  } else {
    form.value = {
      name: "",
      type: "openai",
      base_url: "",
      api_key: "",
      models: [...(defaultModels.openai || [])],
      enabled: true,
    };
  }
  newModelName.value = "";
  remoteModelsList.value = [];
  remoteFetchedDone.value = false;
  remoteFetchMsg.value = "";
  dialogVisible.value = true;
}

function onTypeChange(type: string) {
  form.value.models = [...(defaultModels[type] || [])];
}

function suggestModels(
  query: string,
  cb: (results: { value: string }[]) => void,
) {
  const type = form.value.type || "openai";
  const existing = new Set(form.value.models || []);
  const localList = defaultModels[type] || [];
  const merged = [...new Set([...remoteModelsList.value, ...localList])];
  const filtered = merged
    .filter(
      (m: string) =>
        !existing.has(m) &&
        (!query || m.toLowerCase().includes(query.toLowerCase())),
    )
    .map((m: string) => ({ value: m }));
  cb(filtered);
}

async function onAutocompleteFocus() {
  if (remoteFetchedDone.value || remoteFetching.value || !form.value.api_key)
    return;
  await fetchRemoteModelsForProvider();
}

async function fetchRemoteModelsForProvider() {
  if (!form.value.api_key || remoteFetching.value) return;
  remoteFetching.value = true;
  remoteFetchMsg.value = "";
  try {
    let res: any;
    if (form.value.id) {
      res = await providerApi.remoteModels(form.value.id);
    } else {
      res = await providerApi.remoteModelsByConfig({
        type: form.value.type,
        base_url: form.value.base_url,
        api_key: form.value.api_key,
      });
    }
    remoteModelsList.value = res.data || [];
    remoteFetchedDone.value = true;
    remoteFetchOk.value = true;
    remoteFetchMsg.value = i18n.isEnglish
      ? `Fetched ${remoteModelsList.value.length} remote models`
      : `成功拉取 ${remoteModelsList.value.length} 个远程模型`;
  } catch {
    remoteFetchOk.value = false;
    remoteFetchMsg.value = i18n.isEnglish
      ? "Failed to fetch remote models. Check API Key and Base URL."
      : "远程模型拉取失败，请检查 API Key 和 Base URL";
    remoteFetchedDone.value = true;
  } finally {
    remoteFetching.value = false;
  }
}

function handleModelSelect(item: { value: string }) {
  if (item.value && !form.value.models.includes(item.value)) {
    form.value.models.push(item.value);
  }
  newModelName.value = "";
}

function addModel() {
  const name = newModelName.value.trim();
  if (name && !form.value.models.includes(name)) {
    form.value.models.push(name);
  }
  newModelName.value = "";
}

function removeModel(name: string) {
  form.value.models = form.value.models.filter((m: string) => m !== name);
}

async function handleSubmit() {
  submitting.value = true;
  try {
    if (form.value.id) {
      await providerApi.update(form.value.id, form.value);
      ElMessage.success(i18n.isEnglish ? "Updated" : "更新成功");
    } else {
      await providerApi.create(form.value);
      ElMessage.success(i18n.isEnglish ? "Created" : "创建成功");
    }
    dialogVisible.value = false;
    loadData();
  } finally {
    submitting.value = false;
  }
}

async function handleDelete(id: number) {
  try {
    await providerApi.delete(id);
    ElMessage.success(i18n.t('common.deleteSuccess'));
    loadData();
  } catch {
    ElMessage.error(i18n.t('common.deleteFailed'));
  }
}

onMounted(loadData);
</script>

<style scoped>
.model-tags {
  display: flex;
  flex-wrap: wrap;
  min-height: 32px;
}
</style>
