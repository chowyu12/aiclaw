<template>
  <div class="setup-page">
    <div class="setup-bg" />
    <div class="setup-card">
      <div class="setup-header">
        <div class="setup-logo-wrap">
          <AiclawLogo size="lg" />
        </div>
        <p class="setup-subtitle">首次使用，请配置数据库连接</p>
      </div>

      <div class="setup-steps">
        <el-steps :active="0" align-center finish-status="success" class="step-bar">
          <el-step title="配置数据库" />
          <el-step title="登录控制台" />
          <el-step title="开始使用" />
        </el-steps>
      </div>

      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" class="setup-form">
        <el-form-item label="数据库类型" prop="driver">
          <el-radio-group v-model="form.driver" size="large" class="driver-group">
            <el-radio-button label="sqlite">
              <div class="driver-label">
                <strong>SQLite</strong>
                <span>轻量级，适合开发</span>
              </div>
            </el-radio-button>
            <el-radio-button label="mysql">
              <div class="driver-label">
                <strong>MySQL</strong>
                <span>适合生产环境</span>
              </div>
            </el-radio-button>
            <el-radio-button label="postgres">
              <div class="driver-label">
                <strong>PostgreSQL</strong>
                <span>功能丰富</span>
              </div>
            </el-radio-button>
          </el-radio-group>
        </el-form-item>

        <template v-if="form.driver === 'sqlite'">
          <el-form-item label="数据库文件路径">
            <el-input v-model="form.dsn" placeholder="go_ai_agent.db" />
          </el-form-item>
        </template>

        <template v-if="form.driver === 'mysql'">
          <el-row :gutter="16">
            <el-col :span="16">
              <el-form-item label="主机地址" prop="host">
                <el-input v-model="form.host" placeholder="127.0.0.1" />
              </el-form-item>
            </el-col>
            <el-col :span="8">
              <el-form-item label="端口">
                <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-row :gutter="16">
            <el-col :span="12">
              <el-form-item label="用户名" prop="user">
                <el-input v-model="form.user" placeholder="root" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="密码">
                <el-input v-model="form.password" type="password" show-password placeholder="数据库密码" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-row :gutter="16">
            <el-col :span="16">
              <el-form-item label="数据库名" prop="database">
                <el-input v-model="form.database" placeholder="go_ai_agent" />
              </el-form-item>
            </el-col>
            <el-col :span="8">
              <el-form-item label="字符集">
                <el-input v-model="form.charset" placeholder="utf8mb4" />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <template v-if="form.driver === 'postgres'">
          <el-row :gutter="16">
            <el-col :span="16">
              <el-form-item label="主机地址" prop="host">
                <el-input v-model="form.host" placeholder="127.0.0.1" />
              </el-form-item>
            </el-col>
            <el-col :span="8">
              <el-form-item label="端口">
                <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-row :gutter="16">
            <el-col :span="12">
              <el-form-item label="用户名" prop="user">
                <el-input v-model="form.user" placeholder="postgres" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="密码">
                <el-input v-model="form.password" type="password" show-password placeholder="数据库密码" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-row :gutter="16">
            <el-col :span="16">
              <el-form-item label="数据库名" prop="database">
                <el-input v-model="form.database" placeholder="go_ai_agent" />
              </el-form-item>
            </el-col>
            <el-col :span="8">
              <el-form-item label="SSL 模式">
                <el-select v-model="form.ssl_mode" style="width: 100%">
                  <el-option label="disable" value="disable" />
                  <el-option label="require" value="require" />
                  <el-option label="prefer" value="prefer" />
                </el-select>
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <div class="btn-group">
          <el-button :loading="testing" :disabled="restarting" @click="handleTest">
            测试连接
          </el-button>
          <el-button type="primary" size="large" :loading="saving" :disabled="restarting" @click="handleSave">
            保存并继续
          </el-button>
        </div>
      </el-form>

      <div v-if="restarting" class="restart-overlay">
        <el-icon class="is-loading" :size="32"><Loading /></el-icon>
        <p>配置已保存，服务重启中...</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Loading } from '@element-plus/icons-vue'
import AiclawLogo from '@/components/brand/AiclawLogo.vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { setupApi, type DatabaseConfig } from '@/api/setup'
import { resetSetupStatus } from '@/router'

const router = useRouter()
const formRef = ref<FormInstance>()
const testing = ref(false)
const saving = ref(false)
const restarting = ref(false)

const form = reactive<DatabaseConfig>({
  driver: 'sqlite',
  host: '',
  port: 3306,
  user: '',
  password: '',
  database: '',
  charset: 'utf8mb4',
  ssl_mode: 'disable',
  dsn: '',
})

const rules: FormRules = {
  driver: [{ required: true, message: '请选择数据库类型', trigger: 'change' }],
  host: [{ required: true, message: '请输入主机地址', trigger: 'blur' }],
  user: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  database: [{ required: true, message: '请输入数据库名', trigger: 'blur' }],
}

function buildPayload(): DatabaseConfig {
  const base: DatabaseConfig = { driver: form.driver }
  if (form.driver === 'sqlite') {
    base.dsn = form.dsn || 'go_ai_agent.db'
  } else if (form.driver === 'mysql') {
    Object.assign(base, {
      host: form.host, port: form.port, user: form.user,
      password: form.password, database: form.database, charset: form.charset || 'utf8mb4',
    })
  } else {
    Object.assign(base, {
      host: form.host, port: form.port, user: form.user,
      password: form.password, database: form.database, ssl_mode: form.ssl_mode || 'disable',
    })
  }
  return base
}

async function handleTest() {
  if (form.driver !== 'sqlite') {
    const valid = await formRef.value?.validate().catch(() => false)
    if (!valid) return
  }

  testing.value = true
  try {
    const res: any = await setupApi.testDatabase(buildPayload())
    if (res.data.success) {
      ElMessage.success('数据库连接成功!')
    } else {
      ElMessage.error('连接失败: ' + res.data.error)
    }
  } catch {
    // handled by interceptor
  } finally {
    testing.value = false
  }
}

function sleep(ms: number) {
  return new Promise(resolve => setTimeout(resolve, ms))
}

async function pollServerReady(): Promise<boolean> {
  for (let i = 0; i < 30; i++) {
    await sleep(1000)
    try {
      const res: any = await setupApi.check()
      if (res.data.database_configured) return true
    } catch {
      // server restarting
    }
  }
  return false
}

async function handleSave() {
  if (form.driver !== 'sqlite') {
    const valid = await formRef.value?.validate().catch(() => false)
    if (!valid) return
  }

  saving.value = true
  try {
    await setupApi.saveDatabase(buildPayload())
    ElMessage.success('数据库配置已保存')
    restarting.value = true

    const ready = await pollServerReady()
    if (ready) {
      resetSetupStatus()
      router.push('/login')
    } else {
      ElMessage.warning('服务重启超时，请手动刷新页面')
      restarting.value = false
    }
  } catch {
    // handled by interceptor
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.setup-page {
  position: relative;
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  background: var(--aic-login-page-bg);
}
.setup-bg {
  position: absolute;
  inset: 0;
  background: var(--aic-login-bg);
  pointer-events: none;
}
.setup-card {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 560px;
  margin: 24px;
  padding: 40px 36px 36px;
  background: var(--aic-login-card-bg);
  border-radius: 20px;
  border: 1px solid var(--aic-login-card-border);
  box-shadow: var(--aic-login-card-shadow);
  overflow: hidden;
}
.setup-header {
  text-align: center;
  margin-bottom: 24px;
}
.setup-logo-wrap {
  display: flex;
  justify-content: center;
  margin: 0 auto 16px;
  color: var(--aic-login-brand-title);
}
.setup-subtitle {
  margin: 0;
  color: var(--aic-login-tagline);
  font-size: 14px;
  font-weight: 500;
}
.setup-steps {
  margin-bottom: 28px;
}
.step-bar {
  --el-color-primary: #0284c7;
}
.setup-form {
  margin-top: 4px;
}
.driver-group {
  width: 100%;
  display: flex !important;
}
.driver-group .el-radio-button {
  flex: 1;
}
.driver-group .el-radio-button :deep(.el-radio-button__inner) {
  width: 100%;
  padding: 12px 8px;
}
.driver-label {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.4;
}
.driver-label span {
  font-size: 11px;
  opacity: 0.65;
  font-weight: normal;
}
.btn-group {
  display: flex;
  gap: 12px;
  margin-top: 12px;
}
.btn-group .el-button:last-child {
  flex: 1;
  border-radius: 12px;
  height: 44px;
  font-weight: 600;
  background: linear-gradient(135deg, #0ea5e9, #0284c7);
  border: none;
}
.btn-group .el-button:last-child:hover {
  filter: brightness(1.06);
}
.restart-overlay {
  position: absolute;
  inset: 0;
  background: rgba(252, 252, 253, 0.94);
  backdrop-filter: blur(4px);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  z-index: 10;
}
.restart-overlay p {
  color: #475569;
  font-size: 15px;
}
</style>
