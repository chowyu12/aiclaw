<template>
  <div class="tool-form-page aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">{{ isEdit ? 'Edit Tool' : 'New Tool' }}</h1>
      <p class="aic-sub">Configure the handler, timeout, and function definition. Changes apply to Agents that reference this tool.</p>
    </div>
    <div class="aic-page-body">
    <div class="form-nav">
      <el-button link type="primary" @click="goBack">
        <el-icon><ArrowLeft /></el-icon>
        Back to Tools
      </el-button>
    </div>

    <el-card class="aic-card" shadow="never" v-loading="pageLoading">
      <el-form :model="form" label-width="120px" class="tool-form-body">
        <el-divider content-position="left">Basic Info</el-divider>

        <el-form-item label="Name" required>
          <el-input v-model="form.name" placeholder="Tool name, for example weather" />
        </el-form-item>
        <el-form-item label="Description">
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="Tool description shown in the management page" />
        </el-form-item>
        <el-form-item label="Handler" required>
          <el-select v-model="form.handler_type" placeholder="Select type" style="width: 100%">
            <el-option label="HTTP Callback" value="http" />
            <el-option label="Script" value="script" />
          </el-select>
        </el-form-item>
        <el-row :gutter="24">
          <el-col :span="12">
            <el-form-item label="Timeout(s)">
              <el-input-number v-model="form.timeout" :min="5" :max="300" style="width: 100%" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="Enabled">
              <el-switch v-model="form.enabled" />
            </el-form-item>
          </el-col>
        </el-row>

        <template v-if="form.handler_type === 'http'">
          <el-divider content-position="left">HTTP Config</el-divider>
          <el-form-item label="Request URL" required>
            <el-input v-model="httpConfig.url" placeholder="https://api.example.com/tool?q={param}" />
            <div class="form-hint">Use {param_name} to reference values supplied by the LLM.</div>
          </el-form-item>
          <el-form-item label="Method">
            <el-select v-model="httpConfig.method" style="width: 100%">
              <el-option label="GET" value="GET" />
              <el-option label="POST" value="POST" />
            </el-select>
          </el-form-item>
        </template>

        <template v-if="form.handler_type === 'script'">
          <el-divider content-position="left">Script Config</el-divider>
          <el-form-item label="Language" required>
            <el-select v-model="scriptConfig.language" style="width: 100%">
              <el-option label="Python" value="python" />
              <el-option label="JavaScript" value="javascript" />
              <el-option label="Shell" value="shell" />
              <el-option label="Go" value="go" />
            </el-select>
          </el-form-item>
          <el-form-item label="Script" required>
            <el-input
              v-model="scriptConfig.content"
              type="textarea"
              :rows="12"
              placeholder="#!/bin/sh&#10;echo &quot;Hello {name}, your query is: {query}&quot;"
              style="font-family: monospace"
            />
            <div class="form-hint">Use {param_name} to reference LLM-supplied values. They are substituted at runtime.</div>
          </el-form-item>
        </template>

        <el-divider content-position="left">
          <span>Function Definition</span>
          <el-button link type="primary" style="margin-left: 12px" @click="showRawJson = !showRawJson">
            {{ showRawJson ? 'Visual Mode' : 'Advanced JSON Mode' }}
          </el-button>
        </el-divider>

        <template v-if="showRawJson">
          <el-form-item label="Raw JSON">
            <el-input
              v-model="rawJsonStr"
              type="textarea"
              :rows="12"
              placeholder='{"name":"...","description":"...","parameters":{...}}'
              @blur="syncFromRawJson"
            />
            <div v-if="rawJsonError" class="form-hint" style="color: #f56c6c">{{ rawJsonError }}</div>
          </el-form-item>
        </template>

        <template v-else>
          <el-form-item label="LLM Description">
            <el-input v-model="llmDescription" placeholder="English description telling the model what this tool does, e.g. Get weather for a city" />
            <div class="form-hint">Function description sent to the model. English is recommended.</div>
          </el-form-item>

          <el-form-item label="Parameters">
            <div class="params-editor">
              <div v-if="params.length > 0" class="params-header">
                <span class="col-name">Name</span>
                <span class="col-type">Type</span>
                <span class="col-desc">Description</span>
                <span class="col-required">Required</span>
                <span class="col-enum">Enum</span>
                <span class="col-action"></span>
              </div>
              <div v-for="(p, idx) in params" :key="idx" class="param-row">
                <el-input v-model="p.name" placeholder="name" class="col-name" size="small" />
                <el-select v-model="p.type" class="col-type" size="small">
                  <el-option label="string" value="string" />
                  <el-option label="integer" value="integer" />
                  <el-option label="number" value="number" />
                  <el-option label="boolean" value="boolean" />
                  <el-option label="array" value="array" />
                </el-select>
                <el-input v-model="p.description" placeholder="Parameter description" class="col-desc" size="small" />
                <el-checkbox v-model="p.required" class="col-required" />
                <el-input v-model="p.enumStr" placeholder="a,b,c" class="col-enum" size="small" />
                <el-button link type="danger" class="col-action" @click="params.splice(idx, 1)">
                  <el-icon><Delete /></el-icon>
                </el-button>
              </div>
              <el-button size="small" @click="addParam" style="margin-top: 8px">
                <el-icon><Plus /></el-icon> Add Parameter
              </el-button>
            </div>
          </el-form-item>
        </template>

        <el-form-item>
          <el-button type="primary" @click="handleSubmit" :loading="submitting">
            {{ isEdit ? 'Save' : 'Create' }}
          </el-button>
          <el-button @click="goBack">Cancel</el-button>
        </el-form-item>
      </el-form>
    </el-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, Delete, Plus } from '@element-plus/icons-vue'
import { toolApi } from '../../api/tool'

interface ParamItem {
  name: string
  type: string
  description: string
  required: boolean
  enumStr: string
}

const route = useRoute()
const router = useRouter()

const toolId = computed(() => {
  const id = route.params.id
  return id ? Number(id) : null
})
const isEdit = computed(() => !!toolId.value)

const pageLoading = ref(false)
const submitting = ref(false)
const showRawJson = ref(false)
const rawJsonStr = ref('')
const rawJsonError = ref('')

const form = ref({
  name: '',
  description: '',
  handler_type: 'http',
  enabled: true,
  timeout: 30,
})

const httpConfig = reactive({ url: '', method: 'GET', headers: {} as Record<string, string> })
const scriptConfig = reactive({ language: 'python', content: '' })

const llmDescription = ref('')
const params = ref<ParamItem[]>([])

function addParam() {
  params.value.push({ name: '', type: 'string', description: '', required: false, enumStr: '' })
}

function buildFunctionDef(): any {
  const properties: Record<string, any> = {}
  const required: string[] = []
  for (const p of params.value) {
    if (!p.name) continue
    const prop: Record<string, any> = { type: p.type, description: p.description }
    if (p.enumStr.trim()) {
      prop.enum = p.enumStr.split(',').map(v => v.trim()).filter(Boolean)
    }
    properties[p.name] = prop
    if (p.required) {
      required.push(p.name)
    }
  }
  const def: Record<string, any> = {
    name: form.value.name,
    description: llmDescription.value || form.value.description,
    parameters: { type: 'object', properties },
  }
  if (required.length > 0) {
    def.parameters.required = required
  }
  return def
}

function parseFunctionDef(fd: any) {
  if (!fd) return
  let obj = fd
  if (typeof fd === 'string') {
    try { obj = JSON.parse(fd) } catch { return }
  }
  llmDescription.value = obj.description || ''
  params.value = []
  const props = obj.parameters?.properties
  const req: string[] = obj.parameters?.required || []
  if (props && typeof props === 'object') {
    for (const [name, val] of Object.entries(props)) {
      const v = val as any
      params.value.push({
        name,
        type: v.type || 'string',
        description: v.description || '',
        required: req.includes(name),
        enumStr: Array.isArray(v.enum) ? v.enum.join(', ') : '',
      })
    }
  }
}

function syncToRawJson() {
  rawJsonStr.value = JSON.stringify(buildFunctionDef(), null, 2)
  rawJsonError.value = ''
}

function syncFromRawJson() {
  rawJsonError.value = ''
  if (!rawJsonStr.value.trim()) {
    llmDescription.value = ''
    params.value = []
    return
  }
  try {
    const obj = JSON.parse(rawJsonStr.value)
    parseFunctionDef(obj)
  } catch {
    rawJsonError.value = 'Invalid JSON'
  }
}

watch(showRawJson, (val) => {
  if (val) syncToRawJson()
})

function goBack() {
  router.push({ name: 'Tools' })
}

async function loadTool() {
  if (!toolId.value) return
  pageLoading.value = true
  try {
    const res: any = await toolApi.get(toolId.value)
    const detail = res.data
    form.value = {
      name: detail.name || '',
      description: detail.description || '',
      handler_type: detail.handler_type || 'builtin',
      enabled: detail.enabled ?? true,
      timeout: detail.timeout || 30,
    }
    if (detail.handler_type === 'http' && detail.handler_config) {
      Object.assign(httpConfig, { url: '', method: 'GET', headers: {}, ...detail.handler_config })
    }
    if (detail.handler_type === 'script' && detail.handler_config) {
      Object.assign(scriptConfig, { language: 'python', content: '', ...detail.handler_config })
    }
    parseFunctionDef(detail.function_def)
  } finally {
    pageLoading.value = false
  }
}

async function handleSubmit() {
  if (!form.value.name.trim()) {
    ElMessage.warning('Enter a tool name')
    return
  }
  submitting.value = true
  try {
    if (showRawJson.value) {
      syncFromRawJson()
      if (rawJsonError.value) {
        ElMessage.error('Invalid Function Def JSON')
        return
      }
    }

    const data: any = { ...form.value }
    data.function_def = buildFunctionDef()
    if (data.handler_type === 'http') {
      data.handler_config = { ...httpConfig }
    } else if (data.handler_type === 'script') {
      data.handler_config = { ...scriptConfig }
    }

    if (isEdit.value) {
      await toolApi.update(toolId.value!, data)
      ElMessage.success('Updated')
      goBack()
    } else {
      await toolApi.create(data)
      ElMessage.success('Created')
      goBack()
    }
  } finally {
    submitting.value = false
  }
}

onMounted(loadTool)
</script>

<style scoped>
.tool-form-page {
  padding: 0;
}
.form-nav {
  margin-bottom: 16px;
}
.form-nav :deep(.el-icon) {
  margin-right: 4px;
  vertical-align: middle;
}
.tool-form-body {
  max-width: 100%;
}
.form-hint {
  font-size: 12px;
  color: var(--aic-sidebar-muted);
  margin-top: 2px;
  line-height: 1.4;
}

.params-editor {
  width: 100%;
}
.params-header {
  display: flex;
  gap: 8px;
  align-items: center;
  padding-bottom: 6px;
  border-bottom: 1px solid #ebeef5;
  margin-bottom: 8px;
  font-size: 12px;
  color: #909399;
  font-weight: 500;
}
.param-row {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}
.col-name {
  width: 120px;
  flex-shrink: 0;
}
.col-type {
  width: 100px;
  flex-shrink: 0;
}
.col-desc {
  flex: 1;
  min-width: 120px;
}
.col-required {
  width: 40px;
  flex-shrink: 0;
  display: flex;
  justify-content: center;
}
.col-enum {
  width: 120px;
  flex-shrink: 0;
}
.col-action {
  width: 32px;
  flex-shrink: 0;
}
</style>
