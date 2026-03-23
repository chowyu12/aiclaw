import request from './request'

export interface WebTokenLoginReq {
  token: string
}

export const authApi = {
  login: (data: WebTokenLoginReq) => request.post<{ token: string }>('/auth/login', data),
  me: () => request.get<{ ok: boolean }>('/auth/me'),
}
