import request, { type ListQuery } from './request'

export type SearchEngineProvider = 'tavily' | 'serpapi' | 'aliyun-iqs'

export interface SearchEngineConfig {
  id: number
  provider: SearchEngineProvider
  name: string
  base_url: string
  api_key_set: boolean
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface UpdateSearchEngineConfigReq {
  provider?: SearchEngineProvider
  name?: string
  base_url?: string
  api_key?: string
  enabled?: boolean
}

export interface CreateSearchEngineConfigReq {
  provider: SearchEngineProvider
  name: string
  base_url: string
  api_key?: string
  enabled: boolean
}

export interface SearchEngineResult {
  title: string
  url: string
  snippet: string
}

export interface SearchEngineTestResp {
  query: string
  provider: string
  results: SearchEngineResult[]
}

export interface TestSearchEngineConfigReq {
  id?: number
  query: string
  limit?: number
  provider: SearchEngineProvider
  name: string
  base_url: string
  api_key?: string
}

export const searchEngineApi = {
  list: (params?: ListQuery) => request.get('/search-engines', { params }),
  get: (id: number) => request.get(`/search-engines/${id}`),
  create: (data: CreateSearchEngineConfigReq) => request.post('/search-engines', data),
  update: (id: number, data: UpdateSearchEngineConfigReq) => request.put(`/search-engines/${id}`, data),
  delete: (id: number) => request.delete(`/search-engines/${id}`),
  test: (id: number, data: { query: string; limit?: number }) => request.post(`/search-engines/${id}/test`, data),
  testConfig: (data: TestSearchEngineConfigReq) => request.post('/search-engines/test', data),
}
