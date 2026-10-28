import type { QueryClient } from '@tanstack/react-query'
import { useMessages } from '../store/messages'
import { chatTitle, previewText } from './chat-display'
import { showLocalNotification } from './push'
import type { Chat, Message, ServerEvent } from './types'

export const chatsKey = ['chats'] as const
export const chatKey = (id: string) => ['chat', id] as const

const byActivity = (a: Chat, b: Chat) => b.updated_at.localeCompare(a.updated_at)

export function updateChat(qc: QueryClient, chatId: string, fn: (chat: Chat) => Chat): boolean {
  let found = false
  qc.setQueryData<Chat[]>(chatsKey, (list) => {
    if (!list) return list
    const next = list.map((c) => {
      if (c.id !== chatId) return c
      found = true
      return fn(c)
    })
    return next.sort(byActivity)
  })
  qc.setQueryData<Chat>(chatKey(chatId), (chat) => {
    if (!chat) return chat
    found = true
    return fn(chat)
  })
  return found
}

export function upsertChat(qc: QueryClient, chat: Chat) {
  qc.setQueryData<Chat>(chatKey(chat.id), chat)
  qc.setQueryData<Chat[]>(chatsKey, (list) => {
    if (!list) return list
    const rest = list.filter((c) => c.id !== chat.id)
    return [chat, ...rest].sort(byActivity)
  })
}

function isViewing(chatId: string) {
  return useMessages.getState().activeChatId === chatId && document.visibilityState === 'visible'
}

export function applyMessage(qc: QueryClient, me: string, message: Message) {
  const mine = message.sender_id === me
  const found = updateChat(qc, message.chat_id, (chat) => {
    if (message.seq <= chat.last_seq && chat.last_message && chat.last_message.seq >= message.seq) {
      return chat
    }
    return {
      ...chat,
      last_seq: Math.max(chat.last_seq, message.seq),
      last_message: message,
      updated_at: message.created_at,
      last_read_seq: mine ? Math.max(chat.last_read_seq, message.seq) : chat.last_read_seq,
      unread: mine || isViewing(chat.id) ? chat.unread : chat.unread + 1,
    }
  })
  if (!found) {
    void qc.invalidateQueries({ queryKey: chatsKey })
  }
  if (!mine && message.kind === 'text' && document.visibilityState === 'hidden') {
    const chat = qc.getQueryData<Chat[]>(chatsKey)?.find((c) => c.id === message.chat_id)
    if (chat && !chat.muted) {
      void showLocalNotification(chatTitle(chat), previewText(chat, message, me), `/chat/${chat.id}`, `chat-${chat.id}`)
    }
  }
}

export function applyMessageUpdate(qc: QueryClient, message: Message) {
  updateChat(qc, message.chat_id, (chat) =>
    chat.last_message?.id === message.id && message.rev >= chat.last_message.rev
      ? { ...chat, last_message: message, last_rev: Math.max(chat.last_rev, message.rev) }
      : chat,
  )
}

export function markReadLocally(qc: QueryClient, chatId: string, seq: number) {
  updateChat(qc, chatId, (chat) =>
    seq <= chat.last_read_seq
      ? chat
      : { ...chat, last_read_seq: seq, unread: seq >= chat.last_seq ? 0 : chat.unread },
  )
}

export function handleServerEvent(qc: QueryClient, me: string, event: ServerEvent) {
  const store = useMessages.getState()
  switch (event.type) {
    case 'ready':
      void qc.invalidateQueries({ queryKey: chatsKey })
      void qc.invalidateQueries({ queryKey: ['chat'] })
      void store.catchUpAll()
      break
    case 'message.new':
      store.receive(event.message)
      if (event.message.sender_id !== me) store.clearTyping(event.chat_id)
      applyMessage(qc, me, event.message)
      break
    case 'message.updated':
      store.receive(event.message)
      applyMessageUpdate(qc, event.message)
      break
    case 'chat.read':
      if (event.user_id === me) {
        let partial = false
        updateChat(qc, event.chat_id, (chat) => {
          partial = event.seq < chat.last_seq
          return chat
        })
        markReadLocally(qc, event.chat_id, event.seq)
        if (partial) {
          void qc.invalidateQueries({ queryKey: chatsKey })
          void qc.invalidateQueries({ queryKey: chatKey(event.chat_id) })
        }
      } else {
        updateChat(qc, event.chat_id, (chat) =>
          event.seq > chat.peer_last_read_seq ? { ...chat, peer_last_read_seq: event.seq } : chat,
        )
      }
      break
    case 'chat.updated':
      void qc.invalidateQueries({ queryKey: chatsKey })
      void qc.invalidateQueries({ queryKey: chatKey(event.chat_id) })
      void qc.invalidateQueries({ queryKey: ['members', event.chat_id] })
      break
    case 'chat.removed':
      qc.setQueryData<Chat[]>(chatsKey, (list) => list?.filter((c) => c.id !== event.chat_id))
      qc.removeQueries({ queryKey: chatKey(event.chat_id) })
      qc.removeQueries({ queryKey: ['members', event.chat_id] })
      store.forget(event.chat_id)
      break
    case 'typing':
      if (event.user_id !== me) store.setTyping(event.chat_id, event.user_id)
      break
    case 'presence': {
      const patch = (chat: Chat) =>
        chat.peer && chat.peer.id === event.user_id
          ? { ...chat, peer: { ...chat.peer, online: event.online, last_seen_at: event.last_seen_at ?? chat.peer.last_seen_at } }
          : chat
      qc.setQueryData<Chat[]>(chatsKey, (list) => list?.map(patch))
      for (const query of qc.getQueryCache().findAll({ queryKey: ['chat'] })) {
        qc.setQueryData<Chat>(query.queryKey, (chat) => (chat ? patch(chat) : chat))
      }
      break
    }
  }
}
