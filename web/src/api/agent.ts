import request from './request'

export interface MemOSConfig {
  base_url?: string
  api_key?: string
  user_id?: string
  top_k?: number
  async?: boolean
}

export interface Agent {
  id: number
  uuid: string
  name: string
  description: string
  system_prompt: string
  provider_id: number
  model_name: string
  temperature: number
  max_tokens: number
  timeout: number
  max_history: number
  max_iterations: number
  tool_search_enabled: boolean
  memos_enabled: boolean
  memos_config: MemOSConfig
  token: string
  tools?: any[]
  created_at: string
  updated_at: string
}

/** 与后端 model.UpdateAgentReq 对齐的保存载荷 */
export interface UpdateAgentPayload {
  name?: string
  description?: string
  system_prompt?: string
  provider_id?: number
  model_name?: string
  temperature?: number
  max_tokens?: number
  timeout?: number
  max_history?: number
  max_iterations?: number
  tool_search_enabled?: boolean
  memos_enabled?: boolean
  memos_config?: MemOSConfig
  tool_ids?: number[]
}

export const agentApi = {
  get: () => request.get('/agent'),
  update: (data: UpdateAgentPayload) => request.put('/agent', data),
  resetToken: () => request.post('/agent/reset-token'),
}
