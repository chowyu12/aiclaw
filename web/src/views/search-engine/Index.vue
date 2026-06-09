<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">{{ i18n.t('searchEngine.title') }}</h1>
      <p class="aic-sub">{{ i18n.t('searchEngine.subtitle') }}</p>
    </div>

    <div class="aic-page-body">
      <el-card class="aic-card" shadow="never" v-loading="loading">
        <template #header>
          <div class="search-card-header">
            <span class="aic-card-title">{{ i18n.t('searchEngine.config') }}</span>
            <el-button type="primary" @click="openCreateDialog">
              <el-icon><Plus /></el-icon>
              {{ i18n.t('common.add') }}
            </el-button>
          </div>
        </template>

        <el-table :data="configs" stripe>
          <el-table-column prop="name" :label="i18n.t('common.name')" min-width="160" />
          <el-table-column :label="i18n.t('searchEngine.provider')" min-width="140">
            <template #default="{ row }">
              {{ providerLabel(row.provider) }}
            </template>
          </el-table-column>
          <el-table-column :label="i18n.t('common.status')" width="110">
            <template #default="{ row }">
              <el-switch
                :model-value="row.enabled"
                size="small"
                :loading="!!toggling[row.id]"
                @click.stop
                @change="onToggleEnabled(row, $event)"
              />
            </template>
          </el-table-column>
          <el-table-column :label="i18n.t('searchEngine.apiKey')" width="128">
            <template #default="{ row }">
              <el-tag :type="row.api_key_set ? 'success' : 'warning'" size="small">
                {{ row.api_key_set ? i18n.t('searchEngine.apiKeySet') : i18n.t('searchEngine.apiKeyUnset') }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="i18n.t('common.actions')" width="190" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openEditDialog(row)">
                {{ i18n.t('common.edit') }}
              </el-button>
              <el-button link type="primary" size="small" @click="openTestDialog(row)">
                {{ i18n.t('searchEngine.test') }}
              </el-button>
              <el-popconfirm :title="i18n.t('searchEngine.deleteConfirm')" @confirm="deleteConfig(row.id)">
                <template #reference>
                  <el-button link type="danger" size="small">
                    {{ i18n.t('common.delete') }}
                  </el-button>
                </template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-dialog
        v-model="configDialogVisible"
        :title="form.id > 0 ? i18n.t('common.edit') : i18n.t('common.add')"
        width="560px"
        destroy-on-close
      >
        <el-form :model="form" label-width="104px">
          <el-form-item :label="i18n.t('common.name')" required>
            <el-input v-model="form.name" :placeholder="providerLabel(form.provider)" />
          </el-form-item>
          <el-form-item :label="i18n.t('common.enabled')">
            <el-switch v-model="form.enabled" />
          </el-form-item>
          <el-form-item :label="i18n.t('searchEngine.provider')" required>
            <el-select v-model="form.provider" style="width: 100%" @change="onProviderChange">
              <el-option label="Tavily" value="tavily" />
              <el-option label="SerpAPI" value="serpapi" />
              <el-option label="Aliyun IQS" value="aliyun-iqs" />
            </el-select>
          </el-form-item>
          <el-form-item :label="i18n.t('searchEngine.baseURL')">
            <el-input v-model="form.base_url" :placeholder="baseURLPlaceholder" />
          </el-form-item>
          <el-form-item :label="i18n.t('searchEngine.apiKey')">
            <el-input
              v-model="form.api_key"
              type="password"
              show-password
              :placeholder="apiKeyPlaceholder"
              autocomplete="off"
            />
          </el-form-item>
        </el-form>
        <div class="config-test">
          <div class="config-test-title">{{ i18n.t('searchEngine.test') }}</div>
          <div class="test-actions">
            <el-input
              v-model="configTestQuery"
              class="test-input"
              :placeholder="i18n.t('searchEngine.testPlaceholder')"
              clearable
              @keyup.enter="testConfigForm"
            />
            <el-button :loading="configTesting" @click="testConfigForm">
              <el-icon><Search /></el-icon>
              {{ i18n.t('searchEngine.test') }}
            </el-button>
          </div>
          <el-table v-if="configTestResults.length > 0" :data="configTestResults" stripe class="config-results">
            <el-table-column prop="title" :label="i18n.t('common.name')" min-width="180">
              <template #default="{ row }">
                <a :href="row.url" target="_blank" rel="noreferrer" class="result-link">
                  {{ row.title || row.url }}
                </a>
              </template>
            </el-table-column>
            <el-table-column prop="snippet" :label="i18n.t('common.description')" min-width="260" show-overflow-tooltip />
          </el-table>
        </div>
        <template #footer>
          <el-button @click="configDialogVisible = false">{{ i18n.t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="saving" @click="saveConfig()">
            <el-icon><Check /></el-icon>
            {{ i18n.t('common.save') }}
          </el-button>
        </template>
      </el-dialog>

      <el-dialog
        v-model="testDialogVisible"
        :title="i18n.t('searchEngine.test')"
        width="720px"
        destroy-on-close
      >
        <div class="test-head">
          <span class="test-engine">{{ testTarget?.name }}</span>
          <el-tag v-if="testTarget" size="small" type="info">{{ providerLabel(testTarget.provider) }}</el-tag>
        </div>
        <div class="test-actions">
          <el-input
            v-model="testQuery"
            class="test-input"
            :placeholder="i18n.t('searchEngine.testPlaceholder')"
            clearable
            @keyup.enter="testSearch"
          />
          <el-button type="primary" :loading="testing" @click="testSearch">
            <el-icon><Search /></el-icon>
            {{ i18n.t('searchEngine.test') }}
          </el-button>
        </div>

        <div v-if="results.length > 0" class="results-block">
          <div class="results-title">{{ i18n.t('searchEngine.results') }}</div>
          <el-table :data="results" stripe>
            <el-table-column prop="title" :label="i18n.t('common.name')" min-width="180">
              <template #default="{ row }">
                <a :href="row.url" target="_blank" rel="noreferrer" class="result-link">
                  {{ row.title || row.url }}
                </a>
              </template>
            </el-table-column>
            <el-table-column prop="snippet" :label="i18n.t('common.description')" min-width="260" show-overflow-tooltip />
            <el-table-column prop="url" label="URL" min-width="220" show-overflow-tooltip />
          </el-table>
        </div>
      </el-dialog>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  searchEngineApi,
  type SearchEngineConfig,
  type SearchEngineProvider,
  type SearchEngineResult,
} from '@/api/search_engine'
import { useI18nStore } from '@/stores/i18n'

const i18n = useI18nStore()

const defaultBaseURLs: Record<SearchEngineProvider, string> = {
  tavily: 'https://api.tavily.com/search',
  serpapi: 'https://serpapi.com/search.json',
  'aliyun-iqs': 'https://cloud-iqs.aliyuncs.com/search/unified',
}

const providerLabels: Record<SearchEngineProvider, string> = {
  tavily: 'Tavily',
  serpapi: 'SerpAPI',
  'aliyun-iqs': 'Aliyun IQS',
}

const configs = ref<SearchEngineConfig[]>([])
const form = reactive({
  id: 0,
  name: '',
  provider: 'tavily' as SearchEngineProvider,
  base_url: defaultBaseURLs.tavily,
  api_key: '',
  api_key_set: false,
  enabled: false,
})

const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const configTesting = ref(false)
const toggling = ref<Record<number, boolean>>({})
const configDialogVisible = ref(false)
const testDialogVisible = ref(false)
const testTarget = ref<SearchEngineConfig | null>(null)
const testQuery = ref('')
const configTestQuery = ref('')
const results = ref<SearchEngineResult[]>([])
const configTestResults = ref<SearchEngineResult[]>([])

const baseURLPlaceholder = computed(() => defaultBaseURLs[form.provider])
const apiKeyPlaceholder = computed(() => form.provider === 'aliyun-iqs' ? 'API_KEY' : 'sk-...')

function providerLabel(provider: SearchEngineProvider) {
  return providerLabels[provider] || provider
}

function resetForm(provider: SearchEngineProvider = 'tavily') {
  form.id = 0
  form.provider = provider
  form.name = providerLabel(provider)
  form.base_url = defaultBaseURLs[provider]
  form.api_key = ''
  form.api_key_set = false
  form.enabled = false
}

function applyConfig(cfg: SearchEngineConfig) {
  form.id = cfg.id
  form.name = cfg.name || providerLabel(cfg.provider)
  form.provider = cfg.provider
  form.base_url = cfg.base_url || defaultBaseURLs[cfg.provider]
  form.api_key = ''
  form.api_key_set = cfg.api_key_set
  form.enabled = cfg.enabled
}

function openCreateDialog() {
  resetForm()
  resetConfigTest()
  configDialogVisible.value = true
}

function openEditDialog(row: SearchEngineConfig) {
  applyConfig(row)
  resetConfigTest()
  configDialogVisible.value = true
}

function openTestDialog(row: SearchEngineConfig) {
  testTarget.value = row
  testQuery.value = ''
  results.value = []
  testDialogVisible.value = true
}

async function loadConfigs() {
  loading.value = true
  try {
    const res: any = await searchEngineApi.list({ page: 1, page_size: 100 })
    configs.value = res.data?.list || []
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  if (!form.name.trim()) {
    ElMessage.warning(i18n.t('searchEngine.nameRequired'))
    return null
  }
  saving.value = true
  try {
    const payload = {
      name: form.name.trim(),
      provider: form.provider,
      base_url: form.base_url.trim(),
      enabled: form.enabled,
      api_key: form.api_key.trim() || undefined,
    }
    const res: any = form.id > 0
      ? await searchEngineApi.update(form.id, payload)
      : await searchEngineApi.create(payload)
    const saved = res.data as SearchEngineConfig
    upsertConfig(saved)
    configDialogVisible.value = false
    ElMessage.success(i18n.t('common.saveSuccess'))
    return saved
  } finally {
    saving.value = false
  }
}

async function deleteConfig(id: number) {
  try {
    await searchEngineApi.delete(id)
    configs.value = configs.value.filter((item) => item.id !== id)
    ElMessage.success(i18n.t('common.deleteSuccess'))
  } catch {
    ElMessage.error(i18n.t('common.deleteFailed'))
  }
}

async function toggleEnabled(row: SearchEngineConfig, enabled: boolean) {
  toggling.value = { ...toggling.value, [row.id]: true }
  try {
    const res: any = await searchEngineApi.update(row.id, { enabled })
    const saved = res.data as SearchEngineConfig
    upsertConfig(saved)
    ElMessage.success(enabled ? i18n.t('common.enabledState') : i18n.t('common.disabledState'))
  } finally {
    toggling.value = { ...toggling.value, [row.id]: false }
  }
}

function onToggleEnabled(row: SearchEngineConfig, value: string | number | boolean) {
  toggleEnabled(row, Boolean(value))
}

async function testSearch() {
  const query = testQuery.value.trim()
  if (!query) {
    ElMessage.warning(i18n.t('searchEngine.queryRequired'))
    return
  }
  if (!testTarget.value) return
  testing.value = true
  try {
    const res: any = await searchEngineApi.test(testTarget.value.id, { query, limit: 5 })
    results.value = (res.data?.results || []) as SearchEngineResult[]
  } finally {
    testing.value = false
  }
}

async function testConfigForm() {
  const query = configTestQuery.value.trim()
  if (!query) {
    ElMessage.warning(i18n.t('searchEngine.queryRequired'))
    return
  }
  if (!form.name.trim()) {
    ElMessage.warning(i18n.t('searchEngine.nameRequired'))
    return
  }
  configTesting.value = true
  try {
    const res: any = await searchEngineApi.testConfig({
      id: form.id > 0 ? form.id : undefined,
      query,
      limit: 5,
      provider: form.provider,
      name: form.name.trim(),
      base_url: form.base_url.trim(),
      api_key: form.api_key.trim() || undefined,
    })
    configTestResults.value = (res.data?.results || []) as SearchEngineResult[]
  } finally {
    configTesting.value = false
  }
}

function onProviderChange(provider: SearchEngineProvider) {
  form.base_url = defaultBaseURLs[provider]
  if (!form.name || Object.values(providerLabels).includes(form.name)) {
    form.name = providerLabel(provider)
  }
  form.api_key = ''
  form.api_key_set = false
  configTestResults.value = []
}

function upsertConfig(cfg: SearchEngineConfig) {
  const idx = configs.value.findIndex((item) => item.id === cfg.id)
  if (idx >= 0) {
    configs.value[idx] = cfg
  } else {
    configs.value = [...configs.value, cfg]
  }
}

function resetConfigTest() {
  configTestQuery.value = ''
  configTestResults.value = []
}

onMounted(() => loadConfigs())
</script>

<style scoped>
.search-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.test-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.test-engine {
  font-weight: 600;
}

.test-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.test-input {
  flex: 1;
}

.results-block {
  margin-top: 24px;
  padding-top: 20px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.config-test {
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--el-border-color-lighter);
}

.config-test-title {
  margin-bottom: 10px;
  color: var(--aic-card-title-color);
  font-weight: 600;
}

.config-results {
  margin-top: 12px;
}

.results-title {
  margin-bottom: 12px;
  color: var(--aic-card-title-color);
  font-weight: 600;
}

.result-link {
  color: var(--el-color-primary);
  text-decoration: none;
}

.result-link:hover {
  text-decoration: underline;
}

@media (max-width: 720px) {
  .search-card-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .test-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .test-input {
    width: 100%;
  }
}
</style>
