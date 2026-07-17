import request from './request'

export interface Runtime {
  id: number
  uuid: string
  name: string
  description: string
  agent_type: 'custom' | 'codex' | 'cursor' | 'claude-code' | 'codebuddy' | 'openclaw' | 'hermes'
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
  command: string
  args?: string[]
  prompt_mode?: Runtime['prompt_mode']
}

export const runtimeApi = {
  list: (params?: { page?: number; page_size?: number; keyword?: string }) =>
    request.get<any, { data: { list: Runtime[]; total: number } }>('/runtimes', { params }),
  create: (data: RuntimePayload) => request.post<any, { data: Runtime }>('/runtimes', data),
  get: (id: number) => request.get<any, { data: Runtime }>(`/runtimes/${id}`),
  update: (id: number, data: Partial<RuntimePayload>) => request.put(`/runtimes/${id}`, data),
  delete: (id: number) => request.delete(`/runtimes/${id}`),
  resetToken: (id: number) => request.post<any, { data: { token: string } }>(`/runtimes/${id}/reset-token`),
}
