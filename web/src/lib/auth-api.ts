import { api, json } from './api'
import type { AuthResponse, LoginInput, RegisterInput, Session, User } from './types'

export const authApi = {
  register: (input: RegisterInput) => api<AuthResponse>('/auth/register', json(input)),
  login: (input: LoginInput) => api<AuthResponse>('/auth/login', json(input)),
  logout: () => api<void>('/auth/logout', { method: 'POST' }),
  me: () => api<User>('/me'),
  sessions: () => api<Session[]>('/sessions'),
  revokeSession: (id: string) => api<void>(`/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
}
