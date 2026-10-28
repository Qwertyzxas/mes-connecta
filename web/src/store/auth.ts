import { create } from 'zustand'
import { refreshSession, setAccessToken, setUnauthorizedHandler } from '../lib/api'
import { authApi } from '../lib/auth-api'
import { queryClient } from '../lib/query'
import type { AuthResponse, User } from '../lib/types'

type Status = 'loading' | 'authenticated' | 'guest'

type AuthState = {
  status: Status
  user: User | null
  bootstrap: () => Promise<void>
  signIn: (res: AuthResponse) => void
  signOut: () => Promise<void>
  reset: () => void
}

type TabMessage = { type: 'signed-in' } | { type: 'signed-out' }

const channel = typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel('connecta:auth') : null
const broadcast = (message: TabMessage) => channel?.postMessage(message)

export const useAuth = create<AuthState>()((set, get) => ({
  status: 'loading',
  user: null,

  bootstrap: async () => {
    try {
      const res = await refreshSession()
      set({ status: 'authenticated', user: res.user })
    } catch {
      setAccessToken(null)
      set({ status: 'guest', user: null })
    }
  },

  signIn: (res) => {
    setAccessToken(res.access_token)
    set({ status: 'authenticated', user: res.user })
    broadcast({ type: 'signed-in' })
  },

  signOut: async () => {
    try {
      await authApi.logout()
    } finally {
      get().reset()
      broadcast({ type: 'signed-out' })
    }
  },

  reset: () => {
    setAccessToken(null)
    queryClient.clear()
    set({ status: 'guest', user: null })
  },
}))

setUnauthorizedHandler(() => useAuth.getState().reset())

channel?.addEventListener('message', (event: MessageEvent<TabMessage>) => {
  const { status, bootstrap, reset } = useAuth.getState()
  if (event.data.type === 'signed-out' && status === 'authenticated') reset()
  if (event.data.type === 'signed-in' && status === 'guest') void bootstrap()
})
