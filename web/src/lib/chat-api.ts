import { api, json } from './api'
import type { Chat, Member, Message, PublicUser, Role, SearchResult } from './types'

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
  send: (id: string, clientId: string, body: string, replyToId?: string, attachmentIds: string[] = []) =>
    api<Message>(
      `/chats/${id}/messages`,
      json({ client_id: clientId, body, reply_to_id: replyToId ?? '', attachment_ids: attachmentIds }),
    ),
  fileUrl: (fileId: string) => api<{ url: string }>(`/files/${fileId}/url`),
  edit: (id: string, messageId: string, body: string) =>
    api<Message>(`/chats/${id}/messages/${messageId}`, { method: 'PATCH', body: JSON.stringify({ body }) }),
  remove: (id: string, messageId: string) => api<Message>(`/chats/${id}/messages/${messageId}`, { method: 'DELETE' }),
  markRead: (id: string, seq: number) => api<void>(`/chats/${id}/read`, json({ seq })),
  search: (q: string) => api<SearchResult>(`/search?q=${encodeURIComponent(q)}`),
  createGroup: (title: string, memberIds: string[]) => api<Chat>('/chats/group', json({ title, member_ids: memberIds })),
  createChannel: (input: { title: string; about: string; is_public: boolean; handle: string }) =>
    api<Chat>('/chats/channel', json(input)),
  update: (id: string, patch: { title?: string; about?: string }) =>
    api<Chat>(`/chats/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  members: (id: string) => api<Member[]>(`/chats/${id}/members`),
  addMembers: (id: string, userIds: string[]) => api<Member[]>(`/chats/${id}/members`, json({ user_ids: userIds })),
  removeMember: (id: string, userId: string) => api<void>(`/chats/${id}/members/${userId}`, { method: 'DELETE' }),
  setRole: (id: string, userId: string, role: Exclude<Role, 'owner'>) =>
    api<void>(`/chats/${id}/members/${userId}`, { method: 'PATCH', body: JSON.stringify({ role }) }),
  leave: (id: string) => api<void>(`/chats/${id}/leave`, { method: 'POST' }),
  mute: (id: string, muted: boolean) => api<void>(`/chats/${id}/mute`, json({ muted })),
  join: (id: string) => api<Chat>(`/channels/${id}/join`, { method: 'POST' }),
  wsTicket: () => api<{ ticket: string }>('/ws/ticket', { method: 'POST' }),
}
