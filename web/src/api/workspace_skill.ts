import request from './request'

export interface WorkspaceSkillItem {
  dir_name: string
  name: string
  description: string
  version: string
  author: string
  slug: string
  main_file: string
}

export const workspaceSkillApi = {
  list: () => request.get('/workspace/skills'),
}
