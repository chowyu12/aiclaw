import request, { type ListQuery } from './request'

export type ChannelType =
  | 'wecom'
  | 'wechat_kf'
  | 'feishu'
  | 'dingtalk'
  | 'whatsapp'
  | 'telegram'

export interface Channel {
  id: number
  uuid: string
  name: string
  channel_type: ChannelType
  enabled: boolean
  webhook_token: string
  config: Record<string, unknown> | null
  description: string
  created_at: string
  updated_at: string
}

export interface CreateChannelReq {
  name: string
  channel_type: ChannelType
  enabled?: boolean
  webhook_token?: string
  config?: Record<string, unknown>
  description?: string
}

export const channelApi = {
  list: (params: ListQuery) => request.get('/channels', { params }),
  get: (id: number) => request.get(`/channels/${id}`),
  create: (data: CreateChannelReq) => request.post('/channels', data),
  update: (id: number, data: Partial<CreateChannelReq> & { config?: Record<string, unknown> | null }) =>
    request.put(`/channels/${id}`, data),
  delete: (id: number) => request.delete(`/channels/${id}`),
}
