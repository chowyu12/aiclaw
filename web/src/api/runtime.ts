import request from './request'

export type RuntimeAgentType = 'custom' | 'codex' | 'cursor' | 'claude-code' | 'codebuddy' | 'openclaw' | 'hermes'

export interface RuntimeAgentConfig {
  id: number
  runtime_id: number
  agent_type: Exclude<RuntimeAgentType, 'custom'>
  enabled: boolean
  model_name: string
  created_at: string
  updated_at: string
}

export interface Runtime {
  id: number
  uuid: string
  name: string
  description: string
  builtin: boolean
  agent_type: RuntimeAgentType
  detected_agents: Array<Exclude<RuntimeAgentType, 'custom'>>
  agent_configs: RuntimeAgentConfig[]
  command: string
  args: string[]
  prompt_mode: 'stdin' | 'argument'
  token: string
  status: 'online' | 'offline'
  version?: string
  last_seen_at?: string
  created_at: string
  updated_at: string
}

export interface RuntimePayload {
  name: string
  description?: string
  agent_type?: Runtime['agent_type']
  command?: string
  args?: string[]
  prompt_mode?: Runtime['prompt_mode']
}

export interface RuntimeAgentConfigPayload {
  enabled?: boolean
  model_name?: string
}

export const runtimeApi = {
  list: (params?: { page?: number; page_size?: number; keyword?: string }) =>
    request.get<any, { data: { list: Runtime[]; total: number } }>('/runtimes', { params }),
  create: (data: RuntimePayload) => request.post<any, { data: Runtime }>('/runtimes', data),
  get: (id: number) => request.get<any, { data: Runtime }>(`/runtimes/${id}`),
  update: (id: number, data: Partial<RuntimePayload>) => request.put(`/runtimes/${id}`, data),
  delete: (id: number) => request.delete(`/runtimes/${id}`),
  resetToken: (id: number) => request.post<any, { data: { token: string } }>(`/runtimes/${id}/reset-token`),
  listAgents: (id: number) => request.get<any, { data: RuntimeAgentConfig[] }>(`/runtimes/${id}/agents`),
  updateAgent: (runtimeId: number, agentType: RuntimeAgentConfig['agent_type'], data: RuntimeAgentConfigPayload) =>
    request.put<any, { data: RuntimeAgentConfig }>(`/runtimes/${runtimeId}/agents/${agentType}`, data),
}
