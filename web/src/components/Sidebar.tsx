import { useState } from 'react'
import { Link, NavLink, useNavigate } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Avatar } from './Avatar'
import { Logo } from './Logo'
import { chatApi } from '../lib/chat-api'
import { chatsKey, upsertChat } from '../lib/chat-cache'
import { describeError } from '../lib/errors'
import { formatListTime, lastSeenText } from '../lib/format'
import { useDebounced, useNow } from '../lib/hooks'
import { useConnection } from '../lib/realtime'
import type { Chat, PublicUser } from '../lib/types'
import { normalizeUsername } from '../lib/validation'
import { useAuth } from '../store/auth'
import { useMessages } from '../store/messages'

const searchRe = /^[a-z0-9_]{1,32}$/

export function Sidebar() {
  const user = useAuth((s) => s.user)
  const status = useConnection((s) => s.status)
  const [query, setQuery] = useState('')
  const normalized = normalizeUsername(useDebounced(query, 250))
  const searching = query.trim().length > 0

  return (
    <aside className="sidebar">
      <header className="sidebar__head">
        <Logo size={32} />
        {user && (
          <Link to="/settings" className="sidebar__me" aria-label="Настройки">
            <Avatar id={user.id} name={user.display_name} size={36} />
          </Link>
        )}
      </header>

      <div className="sidebar__search">
        <input
          type="search"
          className="search-input"
          placeholder="Поиск по @username"
          aria-label="Поиск людей"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
        />
      </div>

      {status === 'offline' || status === 'connecting' ? (
        <p className="sidebar__status" role="status">
          {status === 'offline' ? 'Нет соединения. Переподключаемся…' : 'Подключение…'}
        </p>
      ) : null}

      {searching ? (
        <UserSearch query={normalized} onDone={() => setQuery('')} />
      ) : (
        <ChatList />
      )}
    </aside>
  )
}

function UserSearch({ query, onDone }: { query: string; onDone: () => void }) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const valid = searchRe.test(query)
  const results = useQuery({
    queryKey: ['user-search', query],
    queryFn: () => chatApi.searchUsers(query),
    enabled: valid,
    staleTime: 10_000,
  })
  const open = useMutation({
    mutationFn: (u: PublicUser) => chatApi.openDirect(u.id),
    onSuccess: (chat) => {
      upsertChat(qc, chat)
      onDone()
      void navigate(`/chat/${chat.id}`)
    },
  })

  if (!valid) {
    return <p className="sidebar__empty">Имя пользователя: латиница, цифры и _</p>
  }
  if (results.isPending) {
    return <p className="sidebar__empty">Ищем…</p>
  }
  if (results.isError) {
    return <p className="sidebar__empty">{describeError(results.error).message}</p>
  }
  if (results.data.length === 0) {
    return <p className="sidebar__empty">Никого не нашли по @{query}</p>
  }
  return (
    <ul className="list" aria-label="Найденные пользователи">
      {open.isError && <li className="alert">{describeError(open.error).message}</li>}
      {results.data.map((u) => (
        <li key={u.id}>
          <button type="button" className="row" onClick={() => open.mutate(u)} disabled={open.isPending}>
            <Avatar id={u.id} name={u.display_name} online={u.online} />
            <span className="row__main">
              <span className="row__title">{u.display_name}</span>
              <span className="row__sub">@{u.username}</span>
            </span>
          </button>
        </li>
      ))}
    </ul>
  )
}

function ChatList() {
  const chats = useQuery({ queryKey: chatsKey, queryFn: chatApi.list })
  const typing = useMessages((s) => s.typing)
  const anyTyping = Object.keys(typing).length > 0
  const now = useNow(1000, anyTyping)

  if (chats.isPending) return <p className="sidebar__empty">Загружаем чаты…</p>
  if (chats.isError) return <p className="sidebar__empty">{describeError(chats.error).message}</p>
  if (chats.data.length === 0) {
    return (
      <div className="sidebar__empty sidebar__empty--intro">
        <p>Чатов пока нет.</p>
        <p>Найдите собеседника по @username в поиске выше.</p>
      </div>
    )
  }
  return (
    <ul className="list" aria-label="Чаты">
      {chats.data.map((chat) => (
        <li key={chat.id}>
          <ChatRow chat={chat} isTyping={(typing[chat.id]?.until ?? 0) > now} />
        </li>
      ))}
    </ul>
  )
}

function ChatRow({ chat, isTyping }: { chat: Chat; isTyping: boolean }) {
  const me = useAuth((s) => s.user?.id)
  const last = chat.last_message
  const mine = last?.sender_id === me
  const read = last ? chat.peer_last_read_seq >= last.seq : false

  return (
    <NavLink to={`/chat/${chat.id}`} className={({ isActive }) => `row${isActive ? ' row--active' : ''}`}>
      <Avatar id={chat.peer.id} name={chat.peer.display_name} online={chat.peer.online} />
      <span className="row__main">
        <span className="row__line">
          <span className="row__title">{chat.peer.display_name}</span>
          {last && <span className="row__time">{formatListTime(last.created_at)}</span>}
        </span>
        <span className="row__line">
          <span className={`row__sub${isTyping ? ' row__sub--typing' : ''}`}>
            {isTyping
              ? 'печатает…'
              : last
                ? `${mine ? 'Вы: ' : ''}${last.body}`
                : lastSeenText(chat.peer.online, chat.peer.last_seen_at)}
          </span>
          {chat.unread > 0 ? (
            <span className="badge-count" aria-label={`Непрочитанных: ${chat.unread}`}>
              {chat.unread > 99 ? '99+' : chat.unread}
            </span>
          ) : (
            mine && <Ticks read={read} />
          )}
        </span>
      </span>
    </NavLink>
  )
}

export function Ticks({ read }: { read: boolean }) {
  return (
    <span className={`ticks${read ? ' ticks--read' : ''}`} aria-label={read ? 'Прочитано' : 'Отправлено'}>
      <svg viewBox="0 0 18 12" width="18" height="12" aria-hidden="true">
        <path d="M1 6.5 4.5 10 11 2" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        {read && (
          <path d="M7.5 9.2 8.3 10 14.8 2" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        )}
      </svg>
    </span>
  )
}
