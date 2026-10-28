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
