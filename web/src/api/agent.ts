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
  is_default: boolean
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
  token_budget: number
  disable_thinking: boolean
  tool_search_enabled: boolean
  memos_enabled: boolean
  memos_config: MemOSConfig
  token: string
  tool_ids?: number[]
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
  token_budget?: number
  disable_thinking?: boolean
  tool_search_enabled?: boolean
  memos_enabled?: boolean
  memos_config?: MemOSConfig
  tool_ids?: number[]
  is_default?: boolean
}

export interface CreateAgentPayload {
  name: string
  description?: string
  system_prompt?: string
  provider_id?: number
  model_name?: string
  temperature?: number
  max_tokens?: number
  timeout?: number
  max_history?: number
  max_iterations?: number
  token_budget?: number
  disable_thinking?: boolean
  tool_search_enabled?: boolean
  memos_enabled?: boolean
  memos_config?: MemOSConfig
  tool_ids?: number[]
  is_default?: boolean
}

export const agentApi = {
  list: (params?: { page?: number; page_size?: number; keyword?: string }) =>
    request.get<any, { data: { list: Agent[]; total: number } }>('/agents', { params }),
  create: (data: CreateAgentPayload) =>
    request.post<any, { data: Agent }>('/agents', data),
  getById: (id: number) =>
    request.get<any, { data: Agent }>(`/agents/${id}`),
  updateById: (id: number, data: UpdateAgentPayload) =>
    request.put(`/agents/${id}`, data),
  deleteById: (id: number) =>
    request.delete(`/agents/${id}`),
  resetTokenById: (id: number) =>
    request.post<any, { data: { token: string } }>(`/agents/${id}/reset-token`),
}
