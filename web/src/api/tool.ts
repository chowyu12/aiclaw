import request, { type ListQuery } from './request'

export interface Tool {
  id: number
  uuid: string
  name: string
  description: string
  function_def: any
  handler_type: string
  handler_config: any
  enabled: boolean
  timeout: number
  created_at: string
  updated_at: string
}

export interface CreateToolReq {
  name: string
  description?: string
  function_def?: any
  handler_type: string
  handler_config?: any
  enabled?: boolean
  timeout?: number
}

export const toolApi = {
  list: (params: ListQuery) => request.get('/tools', { params }),
  get: (id: number) => request.get(`/tools/${id}`),
  create: (data: CreateToolReq) => request.post('/tools', data),
  update: (id: number, data: Partial<CreateToolReq>) => request.put(`/tools/${id}`, data),
  delete: (id: number) => request.delete(`/tools/${id}`),
}
