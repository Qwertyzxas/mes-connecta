import { api, json } from './api'
import type { Chat, Message, PublicUser } from './types'

export const chatApi = {
  searchUsers: (q: string) => api<PublicUser[]>(`/users/search?q=${encodeURIComponent(q)}`),
  openDirect: (userId: string) => api<Chat>('/chats/direct', json({ user_id: userId })),
  list: () => api<Chat[]>('/chats'),
  get: (id: string) => api<Chat>(`/chats/${id}`),
  messages: (id: string, params: { before?: number; after?: number; since?: number; limit?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.before) q.set('before_seq', String(params.before))
    if (params.after) q.set('after_seq', String(params.after))
    if (params.since) q.set('since_rev', String(params.since))
    if (params.limit) q.set('limit', String(params.limit))
    const qs = q.toString()
    return api<Message[]>(`/chats/${id}/messages${qs ? `?${qs}` : ''}`)
  },
  send: (id: string, clientId: string, body: string, replyToId?: string) =>
    api<Message>(`/chats/${id}/messages`, json({ client_id: clientId, body, reply_to_id: replyToId ?? '' })),
  edit: (id: string, messageId: string, body: string) =>
    api<Message>(`/chats/${id}/messages/${messageId}`, { method: 'PATCH', body: JSON.stringify({ body }) }),
  remove: (id: string, messageId: string) => api<Message>(`/chats/${id}/messages/${messageId}`, { method: 'DELETE' }),
  markRead: (id: string, seq: number) => api<void>(`/chats/${id}/read`, json({ seq })),
  wsTicket: () => api<{ ticket: string }>('/ws/ticket', { method: 'POST' }),
}
