import request, { type ListQuery } from './request'

export type MemoryScope = 'user' | 'agent_user'
export type MemoryKind = 'preference' | 'profile' | 'fact' | 'decision' | 'procedure' | 'constraint'
export type MemoryStatus = 'active' | 'candidate' | 'superseded' | 'dismissed' | 'deleted'
export type MemorySensitivity = 'normal' | 'sensitive'

export interface MemoryItem {
  id: number
  uuid: string
  user_id: string
  agent_uuid?: string
  scope: MemoryScope
  kind: MemoryKind
  memory_key: string
  content: string
  summary: string
  importance: number
  confidence: number
  sensitivity: MemorySensitivity
  status: MemoryStatus
  pinned: boolean
  expires_at?: string
  created_at: string
  updated_at: string
}

export interface MemoryRevision {
  id: number
  memory_id: number
  action: string
  content: string
  summary: string
  status: MemoryStatus
  actor: string
  created_at: string
}

export interface MemoryEvidence {
  id: number
  memory_id: number
  relation: 'source' | 'used'
  conversation_id?: number
  message_id?: number
  run_uuid?: string
  source: string
  created_at: string
}

export interface MemoryContext {
  items: MemoryItem[]
  token_hint?: number
  retrieved_at?: string
}

export interface CreateMemoryRequest {
  agent_uuid?: string
  scope: MemoryScope
  kind: MemoryKind
  memory_key: string
  content: string
  summary?: string
  importance?: number
  confidence?: number
  sensitivity?: MemorySensitivity
  status?: MemoryStatus
  pinned?: boolean
  expires_at?: string
}

export interface UpdateMemoryRequest {
  content?: string
  summary?: string
  importance?: number
  confidence?: number
  sensitivity?: MemorySensitivity
  status?: MemoryStatus
  pinned?: boolean
  expires_at?: string | null
}

export interface MemoryListParams extends ListQuery {
  agent_uuid?: string
  scope?: MemoryScope
  status?: MemoryStatus
  kind?: MemoryKind
  include_all?: boolean
  pending?: boolean
}

export const memoryApi = {
  list: (params: MemoryListParams) => request.get('/memories', { params }),
  get: (uuid: string) => request.get(`/memories/${uuid}`),
  create: (data: CreateMemoryRequest) => request.post('/memories', data),
  update: (uuid: string, data: UpdateMemoryRequest) => request.patch(`/memories/${uuid}`, data),
  delete: (uuid: string) => request.delete(`/memories/${uuid}`),
  revisions: (uuid: string) => request.get(`/memories/${uuid}/revisions`),
  evidence: (uuid: string) => request.get(`/memories/${uuid}/evidence`),
}
