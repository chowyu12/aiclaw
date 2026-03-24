<template>
  <div class="login-page">
    <div class="login-bg" />
    <div class="login-card">
      <div class="login-brand">
        <div class="login-logo-wrap">
          <AiclawLogo size="lg" />
        </div>
        <p class="login-tagline">控制台登录</p>
      </div>
      <p class="hint">
        请输入配置文件中的 <code>auth.web_token</code>，验证成功后浏览器会保存该令牌用于后续请求。
      </p>
      <el-form ref="formRef" :model="form" :rules="rules" @submit.prevent="handleLogin" class="login-form">
        <el-form-item prop="token">
          <el-input
            v-model="form.token"
            type="password"
            placeholder="Web 访问令牌"
            :prefix-icon="Key"
            size="large"
            show-password
            @keyup.enter="handleLogin"
          />
        </el-form-item>
        <el-button type="primary" size="large" :loading="loading" class="login-btn" @click="handleLogin">
          进入控制台
        </el-button>
      </el-form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Key } from '@element-plus/icons-vue'
import AiclawLogo from '@/components/brand/AiclawLogo.vue'
import { ElMessage, type FormInstance } from 'element-plus'
import { authApi } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()
const formRef = ref<FormInstance>()
const loading = ref(false)

const form = reactive({ token: '' })
const rules = {
  token: [{ required: true, message: '请输入访问令牌', trigger: 'blur' }],
}

async function handleLogin() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  loading.value = true
  try {
    const res: any = await authApi.login({ token: form.token.trim() })
    const t = res?.data?.token
    if (!t || typeof t !== 'string') {
      ElMessage.error('登录响应无效，请重试')
      return
    }
    authStore.setToken(t)
    ElMessage.success('验证成功')
    router.push('/')
  } catch {
    // error handled by interceptor
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  position: relative;
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  background: var(--aic-login-page-bg);
}
.login-bg {
  position: absolute;
  inset: 0;
  background: var(--aic-login-bg);
  pointer-events: none;
}
.login-card {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 420px;
  margin: 24px;
  padding: 40px 36px 36px;
  background: var(--aic-login-card-bg);
  border-radius: 20px;
  border: 1px solid var(--aic-login-card-border);
  box-shadow: var(--aic-login-card-shadow);
}
.login-brand {
  text-align: center;
  margin-bottom: 20px;
}
.login-logo-wrap {
  display: flex;
  justify-content: center;
  margin: 0 auto 14px;
  color: var(--aic-login-brand-title);
}
.login-tagline {
  margin: 0;
  font-size: 13px;
  color: var(--aic-login-tagline);
  font-weight: 500;
}
.hint {
  font-size: 13px;
  color: var(--aic-login-hint);
  line-height: 1.55;
  margin-bottom: 20px;
}
.hint code {
  font-size: 12px;
  background: var(--aic-login-code-bg);
  color: var(--aic-login-code-color);
  padding: 2px 6px;
  border-radius: 6px;
}
.login-form {
  margin-top: 4px;
}
.login-btn {
  width: 100%;
  margin-top: 8px;
  height: 44px;
  font-size: 15px;
  font-weight: 600;
  border-radius: 10px;
  border: none;
}
.login-btn:hover {
  filter: brightness(1.05);
}
</style>
