import { create } from 'zustand'
import { chatApi } from '../lib/chat-api'
import type { Message, ReplyPreview } from '../lib/types'

export const PAGE_SIZE = 50
const SYNC_PAGE = 100
const TYPING_TTL = 5_000

export type PendingMessage = {
  client_id: string
  chat_id: string
  body: string
  reply_to: ReplyPreview | null
  created_at: string
  status: 'sending' | 'failed'
}

type ChatMessages = {
  items: Message[]
  status: 'loading' | 'ready' | 'error'
  hasOlder: boolean
  loadingOlder: boolean
  syncRev: number
}

export type Draft = {
  replyTo: Message | null
  editing: Message | null
}

type MessagesState = {
  byChat: Record<string, ChatMessages>
  pending: Record<string, PendingMessage[]>
  drafts: Record<string, Draft>
  typing: Record<string, { userId: string; until: number }>
  activeChatId: string | null

  open: (chatId: string) => Promise<void>
  loadOlder: (chatId: string) => Promise<void>
  catchUp: (chatId: string) => Promise<void>
  catchUpAll: () => Promise<void>
  receive: (message: Message) => void
  send: (chatId: string, body: string, replyTo?: Message | null) => Promise<Message | null>
  retry: (chatId: string, clientId: string) => Promise<void>
  discard: (chatId: string, clientId: string) => void
  edit: (chatId: string, messageId: string, body: string) => Promise<void>
  remove: (chatId: string, messageId: string) => Promise<void>
  setReply: (chatId: string, message: Message | null) => void
  setEditing: (chatId: string, message: Message | null) => void
  setTyping: (chatId: string, userId: string) => void
  clearTyping: (chatId: string) => void
  setActive: (chatId: string | null) => void
  reset: () => void
}

const emptyDraft: Draft = { replyTo: null, editing: null }

export function preview(message: Message): ReplyPreview {
  return {
    id: message.id,
    seq: message.seq,
    sender_id: message.sender_id,
    sender_name: message.sender.display_name,
    body: [...message.body].slice(0, 200).join(''),
    deleted: Boolean(message.deleted_at),
  }
}

function applyBatch(items: Message[], batch: Message[], mode: 'window' | 'append'): Message[] {
  if (batch.length === 0) return items
  const bySeq = new Map<number, Message>(items.map((m) => [m.seq, m]))
  const first = items[0]?.seq ?? Number.POSITIVE_INFINITY
  const last = items.at(-1)?.seq ?? 0
  for (const m of batch) {
    const current = bySeq.get(m.seq)
    if (current) {
      if (m.rev >= current.rev) bySeq.set(m.seq, m)
      continue
    }
    if (mode === 'append' || m.seq > last || m.seq >= first || items.length === 0) {
      bySeq.set(m.seq, m)
    }
  }
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq)
}

const maxRev = (batch: Message[], current = 0) => batch.reduce((acc, m) => Math.max(acc, m.rev), current)

function newClientId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const b = crypto.getRandomValues(new Uint8Array(16))
  b[6] = (b[6]! & 0x0f) | 0x40
  b[8] = (b[8]! & 0x3f) | 0x80
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('')
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}

export const useMessages = create<MessagesState>()((set, get) => {
  const patchChat = (chatId: string, fn: (c: ChatMessages) => Partial<ChatMessages>) =>
    set((s) => {
      const current = s.byChat[chatId]
      if (!current) return s
      return { byChat: { ...s.byChat, [chatId]: { ...current, ...fn(current) } } }
    })

  const removePending = (chatId: string, clientId: string) =>
    set((s) => {
      const list = s.pending[chatId]
      if (!list?.some((p) => p.client_id === clientId)) return s
      return { pending: { ...s.pending, [chatId]: list.filter((p) => p.client_id !== clientId) } }
    })

  const setDraft = (chatId: string, draft: Draft) => set((s) => ({ drafts: { ...s.drafts, [chatId]: draft } }))

  const deliver = async (chatId: string, pending: PendingMessage): Promise<Message | null> => {
    try {
      const msg = await chatApi.send(chatId, pending.client_id, pending.body, pending.reply_to?.id)
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
    drafts: {},
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
        byChat: {
          ...s.byChat,
          [chatId]: { items: [], status: 'loading', hasOlder: false, loadingOlder: false, syncRev: 0 },
        },
      }))
      try {
        const items = await chatApi.messages(chatId, { limit: PAGE_SIZE })
        patchChat(chatId, (c) => ({
          items: applyBatch(c.items, items, 'append'),
          status: 'ready',
          hasOlder: items.length === PAGE_SIZE,
          syncRev: maxRev(items, c.syncRev),
        }))
        await get().catchUp(chatId)
      } catch {
        patchChat(chatId, () => ({ status: 'error' }))
      }
    },

    loadOlder: async (chatId) => {
      const chat = get().byChat[chatId]
      if (!chat || chat.status !== 'ready' || !chat.hasOlder || chat.loadingOlder) return
      const first = chat.items[0]
      if (!first) return
      patchChat(chatId, () => ({ loadingOlder: true }))
      try {
        const older = await chatApi.messages(chatId, { before: first.seq, limit: PAGE_SIZE })
        patchChat(chatId, (c) => ({
          items: applyBatch(c.items, older, 'append'),
          hasOlder: older.length === PAGE_SIZE,
          loadingOlder: false,
        }))
      } catch {
        patchChat(chatId, () => ({ loadingOlder: false }))
      }
    },

    catchUp: async (chatId) => {
      for (;;) {
        const chat = get().byChat[chatId]
        if (!chat || chat.status !== 'ready') return
        let batch: Message[]
        try {
          batch = chat.syncRev
            ? await chatApi.messages(chatId, { since: chat.syncRev, limit: SYNC_PAGE })
            : await chatApi.messages(chatId, { limit: PAGE_SIZE })
        } catch {
          return
        }
        patchChat(chatId, (c) => ({ items: applyBatch(c.items, batch, 'window'), syncRev: maxRev(batch, c.syncRev) }))
        if (!chat.syncRev || batch.length < SYNC_PAGE) return
      }
    },

    catchUpAll: async () => {
      await Promise.all(Object.keys(get().byChat).map((id) => get().catchUp(id)))
    },

    receive: (message) => {
      removePending(message.chat_id, message.client_id)
      const chat = get().byChat[message.chat_id]
      if (!chat || chat.status !== 'ready') return
      const last = chat.items.at(-1)?.seq ?? 0
      patchChat(message.chat_id, (c) => ({ items: applyBatch(c.items, [message], 'window') }))
      const draft = get().drafts[message.chat_id]
      if (draft?.editing?.id === message.id && message.deleted_at) setDraft(message.chat_id, { ...draft, editing: null })
      if (draft?.replyTo?.id === message.id && message.deleted_at) setDraft(message.chat_id, { ...draft, replyTo: null })
      if (last && message.seq > last + 1) void get().catchUp(message.chat_id)
    },

    send: async (chatId, body, replyTo) => {
      const pending: PendingMessage = {
        client_id: newClientId(),
        chat_id: chatId,
        body,
        reply_to: replyTo ? preview(replyTo) : null,
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

    edit: async (chatId, messageId, body) => {
      const msg = await chatApi.edit(chatId, messageId, body)
      get().receive(msg)
    },

    remove: async (chatId, messageId) => {
      const msg = await chatApi.remove(chatId, messageId)
      get().receive(msg)
    },

    setReply: (chatId, message) => setDraft(chatId, { replyTo: message, editing: null }),

    setEditing: (chatId, message) => setDraft(chatId, { replyTo: null, editing: message }),

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

    reset: () => set({ byChat: {}, pending: {}, drafts: {}, typing: {}, activeChatId: null }),
  }
})

export const selectDraft = (chatId: string) => (s: MessagesState) => s.drafts[chatId] ?? emptyDraft
