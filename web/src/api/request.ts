import axios from 'axios'
import { ElMessage } from 'element-plus'

const request = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
})

request.interceptors.request.use((config) => {
  // 登录接口不能带旧的 Bearer 令牌，否则会用过期/错误的令牌校验登录
  const path = `${config.baseURL ?? ''}${config.url ?? ''}`
  if (path.endsWith('/auth/login')) {
    delete (config.headers as any).Authorization
    return config
  }
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

request.interceptors.response.use(
  (response) => {
    const { data } = response
    if (data.code !== 0) {
      ElMessage.error(data.message || '请求失败')
      return Promise.reject(new Error(data.message))
    }
    return data
  },
  (error) => {
    if (error.response?.status === 401) {
      const reqUrl = String(error.config?.url ?? '')
      if (!reqUrl.includes('auth/login')) {
        localStorage.removeItem('token')
        window.location.href = '/login'
      }
      return Promise.reject(error)
    }
    ElMessage.error(error.response?.data?.message || error.message || '网络错误')
    return Promise.reject(error)
  }
)

export default request

export interface ApiResponse<T = any> {
  code: number
  message: string
  data: T
}

export interface PageData<T = any> {
  list: T[]
  total: number
}

export interface ListQuery {
  page: number
  page_size: number
  keyword?: string
}
