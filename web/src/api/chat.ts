import request, { type ListQuery } from './request'

export type ChatFileType = 'document' | 'image' | 'audio' | 'video' | 'custom'
export type TransferMethod = 'remote_url' | 'local_file'

export interface ChatFile {
  type: ChatFileType
  transfer_method: TransferMethod
  url?: string
  upload_file_id?: string
}

export interface ChatRequest {
  conversation_id?: string
  user_id?: string
  message: string
  stream?: boolean
  files?: ChatFile[]
}

export interface FileInfo {
  id: number
  uuid: string
  conversation_id: number
  message_id: number
  filename: string
  content_type: string
  file_size: number
  file_type: 'text' | 'image' | 'document'
  created_at: string
}

export interface ExecutionStep {
  id: number
  message_id: number
  conversation_id: number
  step_order: number
  step_type: 'llm_call' | 'tool_call' | 'agent_call' | 'skill_match'
  name: string
  input: string
  output: string
  status: 'success' | 'error' | 'pending'
  error?: string
  duration_ms: number
  tokens_used: number
  metadata?: {
    provider?: string
    model?: string
    temperature?: number
    tool_name?: string
    skill_name?: string
    skill_tools?: string[]
    channel_id?: number
    channel_uuid?: string
    channel_type?: string
    channel_thread_key?: string
    channel_sender_id?: string
  }
  created_at: string
  _expanded?: boolean
}

export interface ChatResponse {
  conversation_id: string
  message: string
  tokens_used: number
  steps?: ExecutionStep[]
}

export interface StreamChunk {
  conversation_id?: string
  delta?: string
  done: boolean
  step?: ExecutionStep
  steps?: ExecutionStep[]
  files?: FileInfo[]
}

export interface FileInfo {
  uuid: string
  filename: string
  content_type: string
  file_type: 'text' | 'image' | 'document'
  file_size: number
}

export interface Conversation {
  id: number
  uuid: string
  user_id: string
  title: string
  created_at: string
  updated_at: string
}

export interface Message {
  id: number
  conversation_id: number
  role: string
  content: string
  tokens_used?: number
  steps?: ExecutionStep[]
  files?: FileInfo[]
  created_at: string
}

export const chatApi = {
  conversations: (params: ListQuery & { user_id?: string; user_prefix?: string }) =>
    request.get('/conversations', { params }),
  messages: (id: number, limit?: number, withSteps?: boolean) =>
    request.get(`/conversations/${id}/messages`, { params: { limit, with_steps: withSteps ? 'true' : undefined } }),
  deleteConversation: (id: number) => request.delete(`/conversations/${id}`),
}

export const fileApi = {
  upload: (file: File, conversationId?: number) => {
    const form = new FormData()
    form.append('file', file)
    if (conversationId) form.append('conversation_id', String(conversationId))
    return request.post('/files', form, { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 120000 })
  },
  delete: (uuid: string) => request.delete(`/files/${uuid}`),
}

export function streamChat(
  data: ChatRequest,
  onChunk: (chunk: StreamChunk) => void,
  onDone: () => void,
  onError: (err: string) => void,
) {
  const controller = new AbortController()
  const IDLE_TIMEOUT_MS = 300_000
  let idleTimer: ReturnType<typeof setTimeout> | null = null

  const resetIdleTimer = () => {
    if (idleTimer) clearTimeout(idleTimer)
    idleTimer = setTimeout(() => {
      controller.abort()
      onError('请求空闲超时 (300s 无数据)')
    }, IDLE_TIMEOUT_MS)
  }
  resetIdleTimer()

  const token = localStorage.getItem('token') || ''
  fetch('/api/v1/chat/stream', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
    body: JSON.stringify(data),
    signal: controller.signal,
  }).then(async (response) => {
    if (!response.ok) {
      onError(`HTTP ${response.status}`)
      return
    }
    const reader = response.body?.getReader()
    if (!reader) {
      onError('No reader')
      return
    }
    const decoder = new TextDecoder()
    let buffer = ''
    let currentEvent = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      resetIdleTimer()

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''

      for (const line of lines) {
        if (line.startsWith('event: ')) {
          currentEvent = line.slice(7).trim()
          continue
        }
        if (line.startsWith('data: ')) {
          const payload = line.slice(6).trim()
          if (payload === '[DONE]') {
            onDone()
            return
          }
          try {
            if (currentEvent === 'error') {
              const errData = JSON.parse(payload)
              onError(errData.error || 'unknown error')
              return
            }
            const chunk: StreamChunk = JSON.parse(payload)
            onChunk(chunk)
          } catch {
            // skip invalid JSON
          }
          currentEvent = ''
        }
        if (line === '') {
          currentEvent = ''
        }
      }
    }
    onDone()
  }).catch((err) => {
    if (err.name !== 'AbortError') {
      onError(err.message)
    }
  }).finally(() => {
    if (idleTimer) clearTimeout(idleTimer)
  })

  return controller
}
