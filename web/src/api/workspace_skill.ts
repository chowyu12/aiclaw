import request from './request'

export interface SkillItem {
  dir_name: string
  name: string
  description: string
  version: string
  author: string
  slug: string
  main_file: string
  source: 'builtin' | 'local' | 'clawhub' | 'custom'
}

export interface PendingSkillItem {
  file_name: string
  updated_at: string
  preview: string
}

export interface PendingSkillContent {
  file_name: string
  content: string
}

export interface PromotePendingPayload {
  name: string
  description: string
}

export const workspaceSkillApi = {
  list: () => request.get('/workspace/skills'),
  listPending: () => request.get('/workspace/skills/pending'),
  readPending: (file: string) =>
    request.get(`/workspace/skills/pending/${encodeURIComponent(file)}`),
  promotePending: (file: string, payload: PromotePendingPayload) =>
    request.post(`/workspace/skills/pending/${encodeURIComponent(file)}/promote`, payload),
  discardPending: (file: string) =>
    request.delete(`/workspace/skills/pending/${encodeURIComponent(file)}`),
}
