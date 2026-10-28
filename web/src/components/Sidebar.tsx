import { useEffect, useRef, useState } from 'react'
import { Link, NavLink, useNavigate } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Avatar } from './Avatar'
import { LockIcon } from './LockIcon'
import { Logo } from './Logo'
import { NewChannelDialog, NewGroupDialog, NewSecretDialog } from './NewChatDialogs'
import { chatApi } from '../lib/chat-api'
import { chatsKey, upsertChat } from '../lib/chat-cache'
import { chatAvatarId, chatTitle, plural, previewText, secretStatus } from '../lib/chat-display'
import { describeError } from '../lib/errors'
import { formatListTime, lastSeenText } from '../lib/format'
import { useDebounced, useNow } from '../lib/hooks'
import { useConnection } from '../lib/realtime'
import type { Chat } from '../lib/types'
import { normalizeUsername } from '../lib/validation'
import { useAuth } from '../store/auth'
import { useMessages } from '../store/messages'

const searchRe = /^[a-z0-9_]{1,32}$/

export function Sidebar() {
  const user = useAuth((s) => s.user)
  const status = useConnection((s) => s.status)
  const [query, setQuery] = useState('')
  const [dialog, setDialog] = useState<'group' | 'channel' | 'secret' | null>(null)
  const normalized = normalizeUsername(useDebounced(query, 250))
  const searching = query.trim().length > 0

  return (
    <aside className="sidebar">
      <header className="sidebar__head">
        <Logo size={32} />
        <div className="sidebar__actions">
          <NewMenu onPick={setDialog} />
          {user && (
            <Link to="/settings" className="sidebar__me" aria-label="Настройки">
              <Avatar id={user.id} name={user.display_name} size={36} />
            </Link>
          )}
        </div>
      </header>

      <div className="sidebar__search">
        <input
          type="search"
          className="search-input"
          placeholder="Поиск людей и каналов"
          aria-label="Поиск"
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

      {searching ? <SearchResults query={normalized} onDone={() => setQuery('')} /> : <ChatList />}

      {dialog === 'group' && <NewGroupDialog onClose={() => setDialog(null)} />}
      {dialog === 'channel' && <NewChannelDialog onClose={() => setDialog(null)} />}
      {dialog === 'secret' && <NewSecretDialog onClose={() => setDialog(null)} />}
    </aside>
  )
}

function NewMenu({ onPick }: { onPick: (kind: 'group' | 'channel' | 'secret') => void }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const close = (e: Event) => {
      if (e instanceof window.KeyboardEvent && e.key !== 'Escape') return
      if (e.target instanceof Node && ref.current?.contains(e.target)) return
      setOpen(false)
    }
    document.addEventListener('pointerdown', close)
    document.addEventListener('keydown', close)
    return () => {
      document.removeEventListener('pointerdown', close)
      document.removeEventListener('keydown', close)
    }
  }, [open])

  const pick = (kind: 'group' | 'channel' | 'secret') => {
    setOpen(false)
    onPick(kind)
  }

  return (
    <div className="new-menu" ref={ref}>
      <button
        type="button"
        className="icon-btn icon-btn--round"
        aria-label="Создать"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
          <path d="M12 5v14M5 12h14" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </button>
      {open && (
        <div className="msg-menu new-menu__list" role="menu" aria-label="Создать">
          <button type="button" role="menuitem" onClick={() => pick('group')}>
            Новая группа
          </button>
          <button type="button" role="menuitem" onClick={() => pick('channel')}>
            Новый канал
          </button>
          <button type="button" role="menuitem" onClick={() => pick('secret')}>
            Секретный чат
          </button>
        </div>
      )}
    </div>
  )
}

function SearchResults({ query, onDone }: { query: string; onDone: () => void }) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const valid = searchRe.test(query)
  const results = useQuery({
    queryKey: ['search', query],
    queryFn: () => chatApi.search(query),
    enabled: valid,
    staleTime: 10_000,
  })
  const opened = (chat: Chat) => {
    upsertChat(qc, chat)
    onDone()
    void navigate(`/chat/${chat.id}`)
  }
  const openUser = useMutation({ mutationFn: (id: string) => chatApi.openDirect(id), onSuccess: opened })
  const joinChannel = useMutation({ mutationFn: (id: string) => chatApi.join(id), onSuccess: opened })
  const busy = openUser.isPending || joinChannel.isPending
  const error = openUser.error ?? joinChannel.error

  if (!valid) return <p className="sidebar__empty">Ищите по @адресу: латиница, цифры и _</p>
  if (results.isPending) return <p className="sidebar__empty">Ищем…</p>
  if (results.isError) return <p className="sidebar__empty">{describeError(results.error).message}</p>
  const { users, channels } = results.data
  if (users.length === 0 && channels.length === 0) {
    return <p className="sidebar__empty">Никого не нашли по @{query}</p>
  }

  return (
    <div className="search-results">
      {error && <p className="alert">{describeError(error).message}</p>}
      {users.length > 0 && (
        <>
          <h3 className="list-heading">Люди</h3>
          <ul className="list" aria-label="Найденные пользователи">
            {users.map((u) => (
              <li key={u.id}>
                <button type="button" className="row" onClick={() => openUser.mutate(u.id)} disabled={busy}>
                  <Avatar id={u.id} name={u.display_name} online={u.online} />
                  <span className="row__main">
                    <span className="row__title">{u.display_name}</span>
                    <span className="row__sub">@{u.username}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      {channels.length > 0 && (
        <>
          <h3 className="list-heading">Каналы</h3>
          <ul className="list" aria-label="Найденные каналы">
            {channels.map((c) => (
              <li key={c.id}>
                <button
                  type="button"
                  className="row"
                  disabled={busy}
                  onClick={() => (c.joined ? (onDone(), void navigate(`/chat/${c.id}`)) : joinChannel.mutate(c.id))}
                >
                  <Avatar id={c.id} name={c.title} />
                  <span className="row__main">
                    <span className="row__title">{c.title}</span>
                    <span className="row__sub">
                      @{c.handle} · {c.member_count} {plural(c.member_count, 'подписчик', 'подписчика', 'подписчиков')}
                    </span>
                  </span>
                  {!c.joined && <span className="pill">Подписаться</span>}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
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
        <p>Найдите собеседника по @username или создайте группу.</p>
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

function KindIcon({ kind }: { kind: Chat['kind'] }) {
  if (kind === 'direct') return null
  if (kind === 'secret') return <LockIcon size={14} />
  return (
    <svg className="kind-icon" viewBox="0 0 24 24" width="15" height="15" aria-label={kind === 'group' ? 'группа' : 'канал'}>
      {kind === 'group' ? (
        <path
          d="M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7Zm-7 9c0-3.3 3.1-6 7-6s7 2.7 7 6M16 4.6a3.5 3.5 0 0 1 0 6.8M18 14.4c2.4.8 4 2.9 4 5.6"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
        />
      ) : (
        <path d="M3 10v4h3l6 4V6L6 10H3Zm13-1a4 4 0 0 1 0 6m2.5-9a8 8 0 0 1 0 12" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      )}
    </svg>
  )
}

function ChatRow({ chat, isTyping }: { chat: Chat; isTyping: boolean }) {
  const me = useAuth((s) => s.user?.id ?? '')
  const last = chat.last_message
  const mine = last?.sender_id === me && last.kind === 'text'
  const read = last ? chat.peer_last_read_seq >= last.seq : false
  const title = chatTitle(chat)

  return (
    <NavLink to={`/chat/${chat.id}`} className={({ isActive }) => `row${isActive ? ' row--active' : ''}`}>
      <Avatar id={chatAvatarId(chat)} name={title} online={chat.peer?.online} />
      <span className="row__main">
        <span className="row__line">
          <KindIcon kind={chat.kind} />
          <span className="row__title">{title}</span>
          {chat.muted && (
            <svg className="muted-icon" viewBox="0 0 24 24" width="14" height="14" aria-label="без уведомлений">
              <path d="M4 9v6h4l5 4V5L8 9H4Zm13 0 4 6m0-6-4 6" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          )}
          {last && <span className="row__time">{formatListTime(last.created_at)}</span>}
        </span>
        <span className="row__line">
          <span className={`row__sub${isTyping ? ' row__sub--typing' : ''}`}>
            {isTyping
              ? 'печатает…'
              : last
                ? previewText(chat, last, me)
                : secretStatus(chat)
                  ? secretStatus(chat)
                  : chat.peer
                  ? lastSeenText(chat.peer.online, chat.peer.last_seen_at)
                  : ''}
          </span>
          {chat.unread > 0 ? (
            <span className={`badge-count${chat.muted ? ' badge-count--muted' : ''}`} aria-label={`Непрочитанных: ${chat.unread}`}>
              {chat.unread > 99 ? '99+' : chat.unread}
            </span>
          ) : (
            mine && chat.kind !== 'channel' && <Ticks read={read} />
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
