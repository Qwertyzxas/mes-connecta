export type User = {
  id: string
  email: string
  username: string
  display_name: string
  created_at: string
}

export type Session = {
  id: string
  user_agent: string
  ip: string
  created_at: string
  last_used_at: string
  expires_at: string
  current: boolean
}

export type AuthResponse = {
  access_token: string
  expires_at: string
  user: User
}

export type RegisterInput = {
  email: string
  username: string
  display_name: string
  password: string
}

export type LoginInput = {
  email: string
  password: string
}

export type PublicUser = {
  id: string
  username: string
  display_name: string
  last_seen_at: string | null
  online: boolean
}

export type Sender = {
  id: string
  username: string
  display_name: string
}

export type ReplyPreview = {
  id: string
  seq: number
  sender_id: string
  sender_name: string
  body: string
  deleted: boolean
}

export type SystemMeta = {
  action: 'created' | 'added' | 'removed' | 'left' | 'renamed'
  title?: string
  target_id?: string
  target_name?: string
}

export type Message = {
  id: string
  chat_id: string
  seq: number
  rev: number
  kind: 'text' | 'system'
  meta?: SystemMeta
  sender_id: string
  sender: Sender
  client_id: string
  body: string
  reply_to: ReplyPreview | null
  created_at: string
  edited_at: string | null
  deleted_at: string | null
}

export type ChatKind = 'direct' | 'group' | 'channel'
export type Role = 'owner' | 'admin' | 'member'

export type Chat = {
  id: string
  kind: ChatKind
  title: string
  about: string
  handle: string | null
  is_public: boolean
  peer: PublicUser | null
  role: Role
  member_count: number
  online_count: number
  last_seq: number
  last_rev: number
  last_read_seq: number
  peer_last_read_seq: number
  unread: number
  last_message: Message | null
  updated_at: string
}

export type ServerEvent =
  | { type: 'ready' }
  | { type: 'message.new'; chat_id: string; message: Message }
  | { type: 'message.updated'; chat_id: string; message: Message }
  | { type: 'chat.read'; chat_id: string; user_id: string; seq: number }
  | { type: 'chat.updated'; chat_id: string }
  | { type: 'chat.removed'; chat_id: string }
  | { type: 'typing'; chat_id: string; user_id: string }
  | { type: 'presence'; user_id: string; online: boolean; last_seen_at: string | null }

export type Member = PublicUser & {
  role: Role
  joined_at: string
}

export type ChannelResult = {
  id: string
  title: string
  about: string
  handle: string | null
  member_count: number
  joined: boolean
}

export type SearchResult = {
  users: PublicUser[]
  channels: ChannelResult[]
}
