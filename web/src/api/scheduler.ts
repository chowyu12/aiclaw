import request from './request'

export interface SchedulerJob {
  id: string
  name: string
  expression: string
  type: 'prompt' | 'command'
  agent_uuid?: string
  prompt?: string
  command?: string
  user_id?: string
  enabled: boolean
  max_runs?: number
  run_count: number
  created_at: string
  last_run_at?: string
  next_run_at?: string
  description?: string
}

export interface RunRecord {
  job_id: string
  run_at: string
  duration: string
  status: 'success' | 'error'
  output?: string
  error?: string
}

export const schedulerApi = {
  listJobs: () =>
    request.get<any, { data: SchedulerJob[] }>('/scheduler/jobs'),
  toggleJob: (id: string, enabled: boolean) =>
    request.put(`/scheduler/jobs/${id}/toggle`, { enabled }),
  deleteJob: (id: string) =>
    request.delete(`/scheduler/jobs/${id}`),
  getJobLogs: (id: string, limit = 50) =>
    request.get<any, { data: RunRecord[] }>(`/scheduler/jobs/${id}/logs`, { params: { limit } }),
}
