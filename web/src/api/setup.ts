import request from './request'

export interface DatabaseConfig {
  driver: 'sqlite' | 'mysql' | 'postgres'
  host?: string
  port?: number
  user?: string
  password?: string
  database?: string
  charset?: string
  ssl_mode?: string
  dsn?: string
}

export interface SetupStatus {
  database_configured: boolean
}

export const setupApi = {
  check: () => request.get<any, { data: SetupStatus }>('/setup/check'),
  testDatabase: (data: DatabaseConfig) => request.post('/setup/database/test', data),
  saveDatabase: (data: DatabaseConfig) => request.post('/setup/database', data),
}
