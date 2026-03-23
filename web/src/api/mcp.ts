import request from './request'

export interface McpServer {
  id?: number
  uuid: string
  name: string
  description: string
  transport: 'stdio' | 'sse'
  endpoint: string
  args: string[] | null
  env: Record<string, string> | null
  headers: Record<string, string> | null
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export const runtimeMcpApi = {
  list: () => request.get('/runtime/mcp'),
  save: (servers: McpServer[]) => request.put('/runtime/mcp', { servers }),
}
