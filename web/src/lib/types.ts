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

export type Message = {
  id: string
  chat_id: string
  seq: number
  sender_id: string
  client_id: string
  body: string
  created_at: string
}

export type Chat = {
  id: string
  kind: 'direct'
  peer: PublicUser
  last_seq: number
  last_read_seq: number
  peer_last_read_seq: number
  unread: number
  last_message: Message | null
  updated_at: string
}

export type ServerEvent =
  | { type: 'ready' }
  | { type: 'message.new'; chat_id: string; message: Message }
  | { type: 'chat.read'; chat_id: string; user_id: string; seq: number }
  | { type: 'typing'; chat_id: string; user_id: string }
  | { type: 'presence'; user_id: string; online: boolean; last_seen_at: string | null }
