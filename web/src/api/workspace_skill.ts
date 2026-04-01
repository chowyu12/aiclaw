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

export const workspaceSkillApi = {
  list: () => request.get('/workspace/skills'),
}
