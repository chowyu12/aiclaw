import { createRouter, createWebHistory } from 'vue-router'
import { setupApi, type SetupStatus } from '@/api/setup'
import { authApi } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

let _setupStatus: SetupStatus | null = null
let _tokenVerified = false

async function checkSetupStatus(): Promise<SetupStatus> {
  if (_setupStatus) return _setupStatus
  try {
    const res: any = await setupApi.check()
    _setupStatus = res.data
    return _setupStatus!
  } catch {
    return { database_configured: true }
  }
}

async function verifyToken(): Promise<boolean> {
  if (_tokenVerified) return true
  try {
    await authApi.me()
    _tokenVerified = true
    return true
  } catch {
    const authStore = useAuthStore()
    authStore.logout()
    _tokenVerified = false
    return false
  }
}

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/setup/database',
      name: 'SetupDatabase',
      component: () => import('../views/setup/Database.vue'),
      meta: { public: true },
    },
    {
      path: '/login',
      name: 'Login',
      component: () => import('../views/login/Index.vue'),
      meta: { public: true },
    },
    {
      path: '/',
      component: () => import('../components/layout/AppLayout.vue'),
      children: [
        { path: '', redirect: '/chat' },
        { path: 'chat', name: 'Chat', component: () => import('../views/chat/Index.vue') },
        { path: 'settings', name: 'Settings', component: () => import('../views/settings/Index.vue') },
        { path: 'mcp', name: 'Mcp', component: () => import('../views/mcp/Index.vue') },
        { path: 'skill', name: 'Skill', component: () => import('../views/skill/Index.vue') },
        { path: 'providers', name: 'Providers', component: () => import('../views/provider/Index.vue') },
        { path: 'tools', name: 'Tools', component: () => import('../views/tool/Index.vue') },
        { path: 'tools/create', name: 'ToolCreate', component: () => import('../views/tool/Form.vue') },
        { path: 'tools/:id/edit', name: 'ToolEdit', component: () => import('../views/tool/Form.vue') },
        { path: 'logs', name: 'Logs', component: () => import('../views/log/Index.vue') },
        { path: 'channels', name: 'Channels', component: () => import('../views/channel/Index.vue') },
      ],
    },
  ],
})

router.beforeEach(async (to) => {
  const authStore = useAuthStore()
  const queryToken = typeof to.query.token === 'string' ? to.query.token.trim() : ''
  if (queryToken) {
    authStore.setToken(queryToken)
    resetTokenVerified()

    const { token: _token, ...restQuery } = to.query
    return {
      path: to.path,
      query: restQuery,
      hash: to.hash,
      replace: true,
    }
  }

  const status = await checkSetupStatus()

  if (!status.database_configured && to.path !== '/setup/database') {
    return '/setup/database'
  }

  if (status.database_configured && (to.path === '/setup/database')) {
    return '/login'
  }

  const token = authStore.token || localStorage.getItem('token')
  if (!to.meta.public && !token) {
    return '/login'
  }

  if (!to.meta.public && token && !_tokenVerified) {
    const valid = await verifyToken()
    if (!valid) {
      return '/login'
    }
  }

  if (to.path === '/login' && token) {
    return '/chat'
  }
})

export function resetSetupStatus() {
  _setupStatus = null
}

export function resetTokenVerified() {
  _tokenVerified = false
}

export default router
