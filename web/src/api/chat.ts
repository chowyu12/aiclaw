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
  agent_uuid?: string
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
  run_uuid?: string
  message_id: number
  conversation_id: number
  step_order: number
  step_type: 'llm_call' | 'tool_call' | 'agent_call' | 'skill_match'
  name: string
  input: string
  output: string
  status: 'success' | 'error' | 'pending' | 'running'
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
    plan_item_id?: string
    channel_id?: number
    channel_uuid?: string
    channel_type?: string
    channel_thread_key?: string
    channel_sender_id?: string
  }
  sub_agent_call_id?: string
  sub_agent_depth?: number
  created_at: string
  _expanded?: boolean
  _childrenOpen?: boolean
}

export type PlanItemStatus = 'pending' | 'running' | 'completed' | 'blocked' | 'failed' | 'skipped'
export type PlanStatus = 'active' | 'completed' | 'failed'

export interface PlanItem {
  id: number
  plan_run_id: number
  item_key: string
  title: string
  detail?: string
  status: PlanItemStatus
  reason?: string
  step_id?: number
  sort_order: number
  created_at: string
  updated_at: string
}

export interface PlanState {
  id: number
  uuid: string
  conversation_id: number
  message_id?: number
  goal?: string
  source?: 'model' | 'harness'
  status: PlanStatus
  revision_reason?: string
  items: PlanItem[]
  updated_at: string
}

export interface StepNode {
  step: ExecutionStep
  children: StepNode[]
}

export interface ChatResponse {
  run_id?: string
  conversation_id: string
  message: string
  tokens_used: number
  steps?: ExecutionStep[]
  files?: FileInfo[]
  plan?: PlanState
}

export interface StreamChunk {
  run_id?: string
  conversation_id?: string
  message_id?: number
  delta?: string
  content?: string
  tokens_used?: number
  duration_ms?: number
  done: boolean
  step?: ExecutionStep
  steps?: ExecutionStep[]
  files?: FileInfo[]
  plan?: PlanState
  harness_event?: HarnessEvent
}

export type AgentRunStatus = 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface AgentRun {
  id: number
  uuid: string
  agent_id: number
  agent_uuid: string
  conversation_id: number
  conversation_uuid: string
  message_id?: number
  user_id: string
  input: string
  content?: string
  status: AgentRunStatus
  error?: string
  tokens_used: number
  duration_ms: number
  started_at: string
  finished_at?: string
}

export interface AgentRunEvent {
  type: string
  run_id: string
  status?: AgentRunStatus
  run?: AgentRun
  error?: string
  created_at?: string
}

export interface HarnessEvent {
  version: string
  type: string
  layer: string
  run_id?: string
  turn_id?: string
  item_id?: string
  parent_item_id?: string
  name?: string
  status?: string
  delta?: string
  input?: unknown
  output?: unknown
  error?: string
  metadata?: Record<string, unknown>
  created_at: string
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
  tool_calls?: any
  tokens_used?: number
  duration_ms?: number
  steps?: ExecutionStep[]
  files?: FileInfo[]
  plan?: PlanState
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

export interface RetryRequest {
  conversation_id: string
  message_id: number
}

function streamRequest(
  url: string,
  data: unknown,
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
  fetch(url, {
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
            if (currentEvent === 'ping') {
              continue
            }
            if (currentEvent === 'harness') {
              onChunk({ done: false, harness_event: JSON.parse(payload) as HarnessEvent })
              continue
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
    if (err.name === 'AbortError') {
      onDone()
    } else {
      onError(err.message)
    }
  }).finally(() => {
    if (idleTimer) clearTimeout(idleTimer)
  })

  return controller
}

export function streamChat(
  data: ChatRequest,
  onChunk: (chunk: StreamChunk) => void,
  onDone: () => void,
  onError: (err: string) => void,
) {
  return streamRequest('/api/v1/chat/stream', data, onChunk, onDone, onError)
}

export function retryStream(
  data: RetryRequest,
  onChunk: (chunk: StreamChunk) => void,
  onDone: () => void,
  onError: (err: string) => void,
) {
  return streamRequest('/api/v1/chat/retry', data, onChunk, onDone, onError)
}

// streamBackgroundChat starts a durable run first, then attaches SSE to that
// run. Aborting the browser stream sends an explicit cancellation request;
// closing a tab alone does not interrupt the Agent on the server.
export function streamBackgroundChat(
  data: ChatRequest,
  onChunk: (chunk: StreamChunk) => void,
  onDone: () => void,
  onError: (err: string) => void,
  onRun?: (run: AgentRun) => void,
) {
  const controller = new AbortController()
  const token = localStorage.getItem('token') || ''
  const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` }
  const IDLE_TIMEOUT_MS = 300_000
  let runID = ''
  let finished = false
  let idleTimer: ReturnType<typeof setTimeout> | null = null

  const finish = () => {
    if (finished) return
    finished = true
    if (idleTimer) clearTimeout(idleTimer)
    onDone()
  }
  const fail = (message: string) => {
    if (finished) return
    finished = true
    if (idleTimer) clearTimeout(idleTimer)
    onError(message)
  }
  const resetIdleTimer = () => {
    if (idleTimer) clearTimeout(idleTimer)
    idleTimer = setTimeout(() => {
      controller.abort()
      fail('Stream idle timeout (no data for 300s)')
    }, IDLE_TIMEOUT_MS)
  }

  controller.signal.addEventListener('abort', () => {
    if (runID) {
      void fetch(`/api/v1/agent-runs/${encodeURIComponent(runID)}`, {
        method: 'DELETE',
        headers,
      })
    }
  })

  void (async () => {
    try {
      // Do not bind creation to controller.signal: if Stop is pressed while
      // creation is in flight, wait for the ID and then cancel that exact run.
      const startResponse = await fetch('/api/v1/chat/runs', {
        method: 'POST',
        headers,
        body: JSON.stringify(data),
      })
      if (!startResponse.ok) {
        fail(`HTTP ${startResponse.status}`)
        return
      }
      const startPayload = await startResponse.json() as { code?: number; message?: string; data?: AgentRun }
      if (startPayload.code !== 0 || !startPayload.data?.uuid) {
        fail(startPayload.message || 'Unable to start agent run')
        return
      }
      const run = startPayload.data
      runID = run.uuid
      onRun?.(run)
      if (controller.signal.aborted) {
		void fetch(`/api/v1/agent-runs/${encodeURIComponent(runID)}`, { method: 'DELETE', headers })
        finish()
        return
      }

      resetIdleTimer()
      const streamResponse = await fetch(`/api/v1/agent-runs/${encodeURIComponent(runID)}/stream`, {
        headers: { 'Authorization': `Bearer ${token}` },
        signal: controller.signal,
      })
      if (!streamResponse.ok) {
        fail(`HTTP ${streamResponse.status}`)
        return
      }
      const reader = streamResponse.body?.getReader()
      if (!reader) {
        fail('No stream reader')
        return
      }
      const decoder = new TextDecoder()
      let buffer = ''
      let currentEvent = ''
      while (!finished) {
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
              finish()
              return
            }
            try {
              if (currentEvent === 'ping') {
                continue
              }
              if (currentEvent === 'error') {
                const errData = JSON.parse(payload) as { error?: string }
                fail(errData.error || 'Unknown stream error')
                return
              }
              if (currentEvent === 'harness') {
                onChunk({ done: false, harness_event: JSON.parse(payload) as HarnessEvent })
                continue
              }
              if (currentEvent === 'run') {
                const event = JSON.parse(payload) as AgentRunEvent
                if (event.run) onRun?.(event.run)
                if (event.type === 'run.completed' || event.type === 'run.failed' || event.type === 'run.cancelled') {
                  finish()
                  return
                }
                continue
              }
              onChunk(JSON.parse(payload) as StreamChunk)
            } catch {
              // Ignore malformed event payloads and keep the connection alive.
            } finally {
              currentEvent = ''
            }
          }
          if (line === '') currentEvent = ''
        }
      }
      finish()
    } catch (err: any) {
      if (err?.name === 'AbortError') {
        finish()
      } else {
        fail(err?.message || 'Network error')
      }
    }
  })()

  return controller
}
