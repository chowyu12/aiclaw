import request, { type ListQuery } from './request'

export interface Provider {
  id: number
  name: string
  type: string
  base_url: string
  api_key: string
  models: string[]
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface CreateProviderReq {
  name: string
  type: string
  base_url: string
  api_key: string
  models?: string[]
  enabled?: boolean
}

export const providerApi = {
  list: (params: ListQuery) => request.get('/providers', { params }),
  get: (id: number) => request.get(`/providers/${id}`),
  create: (data: CreateProviderReq) => request.post('/providers', data),
  update: (id: number, data: Partial<CreateProviderReq>) => request.put(`/providers/${id}`, data),
  delete: (id: number) => request.delete(`/providers/${id}`),
  models: (id: number) => request.get(`/providers/${id}/models`),
  remoteModels: (id: number) => request.get(`/providers/${id}/models/remote`),
  remoteModelsByConfig: (data: { type: string; base_url: string; api_key: string }) =>
    request.post('/providers/models/remote', data),
}
