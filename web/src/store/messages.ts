import { create } from 'zustand'
import { chatApi } from '../lib/chat-api'
import type { Message } from '../lib/types'

export const PAGE_SIZE = 50
const CATCH_UP_PAGE = 100
const TYPING_TTL = 5_000

export type PendingMessage = {
  client_id: string
  chat_id: string
  body: string
  created_at: string
  status: 'sending' | 'failed'
}

type ChatMessages = {
  items: Message[]
  status: 'loading' | 'ready' | 'error'
  hasOlder: boolean
  loadingOlder: boolean
}

type MessagesState = {
  byChat: Record<string, ChatMessages>
  pending: Record<string, PendingMessage[]>
  typing: Record<string, { userId: string; until: number }>
  activeChatId: string | null

  open: (chatId: string) => Promise<void>
  loadOlder: (chatId: string) => Promise<void>
  catchUp: (chatId: string) => Promise<void>
  catchUpAll: () => Promise<void>
  receive: (message: Message) => 'added' | 'duplicate' | 'gap' | 'unloaded'
  send: (chatId: string, body: string) => Promise<Message | null>
  retry: (chatId: string, clientId: string) => Promise<void>
  discard: (chatId: string, clientId: string) => void
  setTyping: (chatId: string, userId: string) => void
  clearTyping: (chatId: string) => void
  setActive: (chatId: string | null) => void
  reset: () => void
}

function merge(items: Message[], incoming: Message[]): Message[] {
  const bySeq = new Map<number, Message>()
  for (const m of items) bySeq.set(m.seq, m)
  for (const m of incoming) bySeq.set(m.seq, m)
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq)
}

function newClientId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const b = crypto.getRandomValues(new Uint8Array(16))
  b[6] = (b[6]! & 0x0f) | 0x40
  b[8] = (b[8]! & 0x3f) | 0x80
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('')
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}

export const useMessages = create<MessagesState>()((set, get) => {
  const patchChat = (chatId: string, patch: Partial<ChatMessages>) =>
    set((s) => {
      const current = s.byChat[chatId]
      if (!current) return s
      return { byChat: { ...s.byChat, [chatId]: { ...current, ...patch } } }
    })

  const removePending = (chatId: string, clientId: string) =>
    set((s) => ({
      pending: { ...s.pending, [chatId]: (s.pending[chatId] ?? []).filter((p) => p.client_id !== clientId) },
    }))

  const deliver = async (chatId: string, pending: PendingMessage): Promise<Message | null> => {
    try {
      const msg = await chatApi.send(chatId, pending.client_id, pending.body)
      get().receive(msg)
      removePending(chatId, pending.client_id)
      return msg
    } catch {
      set((s) => ({
        pending: {
          ...s.pending,
          [chatId]: (s.pending[chatId] ?? []).map((p) =>
            p.client_id === pending.client_id ? { ...p, status: 'failed' } : p,
          ),
        },
      }))
      return null
    }
  }

  return {
    byChat: {},
    pending: {},
    typing: {},
    activeChatId: null,

    open: async (chatId) => {
      const existing = get().byChat[chatId]
      if (existing?.status === 'ready') {
        await get().catchUp(chatId)
        return
      }
      if (existing?.status === 'loading') return
      set((s) => ({
        byChat: { ...s.byChat, [chatId]: { items: [], status: 'loading', hasOlder: false, loadingOlder: false } },
      }))
      try {
        const items = await chatApi.messages(chatId, { limit: PAGE_SIZE })
        set((s) => ({
          byChat: {
            ...s.byChat,
            [chatId]: {
              items: merge(s.byChat[chatId]?.items ?? [], items),
              status: 'ready',
              hasOlder: items.length === PAGE_SIZE,
              loadingOlder: false,
            },
          },
        }))
      } catch {
        patchChat(chatId, { status: 'error' })
      }
    },

    loadOlder: async (chatId) => {
      const chat = get().byChat[chatId]
      if (!chat || chat.status !== 'ready' || !chat.hasOlder || chat.loadingOlder) return
      const first = chat.items[0]
      if (!first) return
      patchChat(chatId, { loadingOlder: true })
      try {
        const older = await chatApi.messages(chatId, { before: first.seq, limit: PAGE_SIZE })
        set((s) => {
          const current = s.byChat[chatId]
          if (!current) return s
          return {
            byChat: {
              ...s.byChat,
              [chatId]: {
                ...current,
                items: merge(current.items, older),
                hasOlder: older.length === PAGE_SIZE,
                loadingOlder: false,
              },
            },
          }
        })
      } catch {
        patchChat(chatId, { loadingOlder: false })
      }
    },

    catchUp: async (chatId) => {
      for (;;) {
        const chat = get().byChat[chatId]
        if (!chat || chat.status !== 'ready') return
        const last = chat.items.at(-1)?.seq ?? 0
        let batch: Message[]
        try {
          batch = last
            ? await chatApi.messages(chatId, { after: last, limit: CATCH_UP_PAGE })
            : await chatApi.messages(chatId, { limit: PAGE_SIZE })
        } catch {
          return
        }
        set((s) => {
          const current = s.byChat[chatId]
          if (!current) return s
          return { byChat: { ...s.byChat, [chatId]: { ...current, items: merge(current.items, batch) } } }
        })
        if (!last || batch.length < CATCH_UP_PAGE) return
      }
    },

    catchUpAll: async () => {
      await Promise.all(Object.keys(get().byChat).map((id) => get().catchUp(id)))
    },

    receive: (message) => {
      removePending(message.chat_id, message.client_id)
      const chat = get().byChat[message.chat_id]
      if (!chat || chat.status !== 'ready') return 'unloaded'
      if (chat.items.some((m) => m.seq === message.seq)) return 'duplicate'
      const last = chat.items.at(-1)?.seq ?? 0
      set((s) => {
        const current = s.byChat[message.chat_id]
        if (!current) return s
        return {
          byChat: { ...s.byChat, [message.chat_id]: { ...current, items: merge(current.items, [message]) } },
        }
      })
      if (last && message.seq > last + 1) {
        void get().catchUp(message.chat_id)
        return 'gap'
      }
      return 'added'
    },

    send: async (chatId, body) => {
      const pending: PendingMessage = {
        client_id: newClientId(),
        chat_id: chatId,
        body,
        created_at: new Date().toISOString(),
        status: 'sending',
      }
      set((s) => ({ pending: { ...s.pending, [chatId]: [...(s.pending[chatId] ?? []), pending] } }))
      return deliver(chatId, pending)
    },

    retry: async (chatId, clientId) => {
      const pending = get().pending[chatId]?.find((p) => p.client_id === clientId)
      if (!pending) return
      set((s) => ({
        pending: {
          ...s.pending,
          [chatId]: (s.pending[chatId] ?? []).map((p) => (p.client_id === clientId ? { ...p, status: 'sending' } : p)),
        },
      }))
      await deliver(chatId, { ...pending, status: 'sending' })
    },

    discard: (chatId, clientId) => removePending(chatId, clientId),

    setTyping: (chatId, userId) =>
      set((s) => ({ typing: { ...s.typing, [chatId]: { userId, until: Date.now() + TYPING_TTL } } })),

    clearTyping: (chatId) =>
      set((s) => {
        if (!s.typing[chatId]) return s
        const typing = { ...s.typing }
        delete typing[chatId]
        return { typing }
      }),

    setActive: (chatId) => set({ activeChatId: chatId }),

    reset: () => set({ byChat: {}, pending: {}, typing: {}, activeChatId: null }),
  }
})
