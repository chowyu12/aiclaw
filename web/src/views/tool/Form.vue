<template>
  <div class="tool-form-page aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">{{ isEdit ? '编辑工具' : '新增工具' }}</h1>
      <p class="aic-sub">配置处理器、超时与函数定义；保存后对已关联该工具的 Agent 生效。</p>
    </div>
    <div class="aic-page-body">
    <div class="form-nav">
      <el-button link type="primary" @click="goBack">
        <el-icon><ArrowLeft /></el-icon>
        返回工具列表
      </el-button>
    </div>

    <el-card class="aic-card" shadow="never" v-loading="pageLoading">
      <el-form :model="form" label-width="120px" class="tool-form-body">
        <el-divider content-position="left">基本信息</el-divider>

        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="工具名称（英文标识，如 weather）" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="工具功能描述（管理页展示）" />
        </el-form-item>
        <el-form-item label="处理器类型" required>
          <el-select v-model="form.handler_type" placeholder="选择类型" style="width: 100%">
            <el-option label="内置函数 (builtin)" value="builtin" />
            <el-option label="HTTP 回调 (http)" value="http" />
            <el-option label="命令行 (command)" value="command" />
            <el-option label="脚本 (script)" value="script" />
          </el-select>
        </el-form-item>
        <el-row :gutter="24">
          <el-col :span="12">
            <el-form-item label="超时(秒)">
              <el-input-number v-model="form.timeout" :min="5" :max="300" style="width: 100%" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="启用">
              <el-switch v-model="form.enabled" />
            </el-form-item>
          </el-col>
        </el-row>

        <template v-if="form.handler_type === 'http'">
          <el-divider content-position="left">HTTP 配置</el-divider>
          <el-form-item label="请求 URL" required>
            <el-input v-model="httpConfig.url" placeholder="https://api.example.com/tool?q={param}" />
            <div class="form-hint">使用 {参数名} 引用 LLM 传入的参数值</div>
          </el-form-item>
          <el-form-item label="请求方法">
            <el-select v-model="httpConfig.method" style="width: 100%">
              <el-option label="GET" value="GET" />
              <el-option label="POST" value="POST" />
            </el-select>
          </el-form-item>
        </template>

        <template v-if="form.handler_type === 'command'">
          <el-divider content-position="left">命令配置</el-divider>
          <el-form-item label="命令模板" required>
            <el-input v-model="cmdConfig.command" placeholder="ls -la {path}" />
            <div class="form-hint">使用 {参数名} 引用 LLM 传入的参数值</div>
          </el-form-item>
          <el-form-item label="工作目录">
            <el-input v-model="cmdConfig.working_dir" placeholder="留空使用默认目录" />
          </el-form-item>
          <el-form-item label="Shell">
            <el-input v-model="cmdConfig.shell" placeholder="/bin/sh" />
          </el-form-item>
        </template>

        <el-divider content-position="left">
          <span>函数定义</span>
          <el-button link type="primary" style="margin-left: 12px" @click="showRawJson = !showRawJson">
            {{ showRawJson ? '可视化模式' : '高级 JSON 模式' }}
          </el-button>
        </el-divider>

        <template v-if="showRawJson">
          <el-form-item label="原始 JSON">
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
          <el-form-item label="LLM 描述">
            <el-input v-model="llmDescription" placeholder="英文描述，告诉大模型这个工具做什么（如 Get weather for a city）" />
            <div class="form-hint">发送给大模型的函数描述，建议用英文</div>
          </el-form-item>

          <el-form-item label="参数列表">
            <div class="params-editor">
              <div v-if="params.length > 0" class="params-header">
                <span class="col-name">参数名</span>
                <span class="col-type">类型</span>
                <span class="col-desc">描述</span>
                <span class="col-required">必填</span>
                <span class="col-enum">枚举值</span>
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
                <el-input v-model="p.description" placeholder="参数描述" class="col-desc" size="small" />
                <el-checkbox v-model="p.required" class="col-required" />
                <el-input v-model="p.enumStr" placeholder="a,b,c" class="col-enum" size="small" />
                <el-button link type="danger" class="col-action" @click="params.splice(idx, 1)">
                  <el-icon><Delete /></el-icon>
                </el-button>
              </div>
              <el-button size="small" @click="addParam" style="margin-top: 8px">
                <el-icon><Plus /></el-icon> 添加参数
              </el-button>
            </div>
          </el-form-item>
        </template>

        <el-form-item>
          <el-button type="primary" @click="handleSubmit" :loading="submitting">
            {{ isEdit ? '保存' : '创建' }}
          </el-button>
          <el-button @click="goBack">取消</el-button>
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
  handler_type: 'builtin',
  enabled: true,
  timeout: 30,
})

const httpConfig = reactive({ url: '', method: 'GET', headers: {} as Record<string, string> })
const cmdConfig = reactive({ command: '', working_dir: '', shell: '/bin/sh' })

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
    rawJsonError.value = 'JSON 格式错误'
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
    if (detail.handler_type === 'command' && detail.handler_config) {
      Object.assign(cmdConfig, { command: '', working_dir: '', shell: '/bin/sh', ...detail.handler_config })
    }
    parseFunctionDef(detail.function_def)
  } finally {
    pageLoading.value = false
  }
}

async function handleSubmit() {
  if (!form.value.name.trim()) {
    ElMessage.warning('请输入工具名称')
    return
  }
  submitting.value = true
  try {
    if (showRawJson.value) {
      syncFromRawJson()
      if (rawJsonError.value) {
        ElMessage.error('Function Def JSON 格式错误')
        return
      }
    }

    const data: any = { ...form.value }
    data.function_def = buildFunctionDef()
    if (data.handler_type === 'http') {
      data.handler_config = { ...httpConfig }
    } else if (data.handler_type === 'command') {
      data.handler_config = { ...cmdConfig }
    }

    if (isEdit.value) {
      await toolApi.update(toolId.value!, data)
      ElMessage.success('更新成功')
      goBack()
    } else {
      await toolApi.create(data)
      ElMessage.success('创建成功')
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
