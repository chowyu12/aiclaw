import request, { type ListQuery } from './request'

export type ChannelType =
  | 'wecom'
  | 'wechat'
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
  agent_uuid: string
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
  agent_uuid?: string
}

export interface ChannelConversationItem {
  conversation_id: number
  conversation_uuid: string
  title: string
  user_id: string
  sender_id: string
  thread_keys?: string[]
  message_count: number
  last_user_message?: string
  last_reply_message?: string
  updated_at: string
  created_at: string
}

export interface ChannelMessage {
  id: number
  conversation_id: number
  role: string
  content: string
  tool_calls?: unknown
  tool_call_id?: string
  name?: string
  tokens_used: number
  created_at: string
  steps?: unknown[]
}

export interface WeChatQRCodeResult {
  qrcode: string
  qrcode_url: string
}

export interface WeChatQRStatusResult {
  status: 'wait' | 'scaned' | 'confirmed' | 'expired' | 'error'
  bot_id?: string
  error?: string
}

export const channelApi = {
  list: (params: ListQuery) => request.get('/channels', { params }),
  setEnabled: (id: number, enabled: boolean) => request.patch(`/channels/${id}/enabled`, { enabled }),
  conversations: (id: number, params: ListQuery & { thread_key?: string; sender_id?: string }) =>
    request.get(`/channels/${id}/conversations`, { params }),
  conversationMessages: (channelId: number, conversationId: number, params?: { limit?: number; with_steps?: boolean }) =>
    request.get(`/channels/${channelId}/conversations/${conversationId}/messages`, { params }),
  deleteConversation: (channelId: number, conversationId: number) =>
    request.delete(`/channels/${channelId}/conversations/${conversationId}`),
  create: (data: CreateChannelReq) => request.post('/channels', data),
  update: (id: number, data: Partial<CreateChannelReq> & { config?: Record<string, unknown> | null }) =>
    request.put(`/channels/${id}`, data),
  delete: (id: number) => request.delete(`/channels/${id}`),
  wechatQRCode: (id: number) =>
    request.post<WeChatQRCodeResult>(`/channels/${id}/wechat/qrcode`),
  wechatQRStatus: (id: number, qrcode: string) =>
    request.get<WeChatQRStatusResult>(`/channels/${id}/wechat/qrcode/status`, { params: { qrcode } }),
}
