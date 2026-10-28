import { Fragment, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Avatar } from '../components/Avatar'
import { Ticks } from '../components/Sidebar'
import { ApiError } from '../lib/api'
import { chatApi } from '../lib/chat-api'
import { chatKey, chatsKey, markReadLocally } from '../lib/chat-cache'
import { dayKey, formatDay, formatTime, lastSeenText } from '../lib/format'
import { useDocumentVisible, useNow } from '../lib/hooks'
import { realtime } from '../lib/realtime'
import type { Chat, Message } from '../lib/types'
import { useAuth } from '../store/auth'
import { useMessages, type PendingMessage } from '../store/messages'

const MAX_LEN = 4096
const NEAR_BOTTOM = 120
const TYPING_EVERY = 3000

export function ChatPage() {
  const { chatId = '' } = useParams()
  const qc = useQueryClient()

  const chat = useQuery({
    queryKey: chatKey(chatId),
    queryFn: () => chatApi.get(chatId),
    initialData: () => qc.getQueryData<Chat[]>(chatsKey)?.find((c) => c.id === chatId),
    initialDataUpdatedAt: () => qc.getQueryState(chatsKey)?.dataUpdatedAt,
  })

  const setActive = useMessages((s) => s.setActive)
  const open = useMessages((s) => s.open)

  useEffect(() => {
    setActive(chatId)
    void open(chatId)
    return () => setActive(null)
  }, [chatId, open, setActive])

  if (chat.isError) {
    const notFound = chat.error instanceof ApiError && chat.error.status === 404
    return (
      <section className="conversation conversation--empty">
        <p>{notFound ? 'Чат не найден' : 'Не удалось загрузить чат'}</p>
        <Link to="/">К списку чатов</Link>
      </section>
    )
  }
  if (!chat.data) {
    return (
      <section className="conversation conversation--empty" aria-busy="true">
        <span className="spinner spinner--large" />
      </section>
    )
  }
  return <Conversation key={chatId} chat={chat.data} />
}

function Conversation({ chat }: { chat: Chat }) {
  const me = useAuth((s) => s.user?.id ?? '')
  const qc = useQueryClient()
  const state = useMessages((s) => s.byChat[chat.id])
  const pending = useMessages((s) => s.pending[chat.id])
  const typing = useMessages((s) => s.typing[chat.id])
  const loadOlder = useMessages((s) => s.loadOlder)
  const visible = useDocumentVisible()
  const now = useNow(1000, Boolean(typing))
  const isTyping = Boolean(typing && typing.until > now)

  const items = state?.items ?? []

  const readTarget = useMemo(() => {
    for (let i = items.length - 1; i >= 0; i--) {
      const m = items[i]!
      if (m.sender_id !== me) return m.seq
    }
    return 0
  }, [items, me])

  useEffect(() => {
    if (!visible || readTarget <= chat.last_read_seq) return
    const id = setTimeout(() => {
      markReadLocally(qc, chat.id, readTarget)
      chatApi.markRead(chat.id, readTarget).catch(() => {
        void qc.invalidateQueries({ queryKey: chatKey(chat.id) })
      })
    }, 300)
    return () => clearTimeout(id)
  }, [visible, readTarget, chat.id, chat.last_read_seq, qc])

  const scroller = useRef<HTMLDivElement>(null)
  const stickToBottom = useRef(true)
  const prevFirstSeq = useRef<number | null>(null)
  const prevHeight = useRef(0)

  const onScroll = useCallback(() => {
    const el = scroller.current
    if (!el) return
    stickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM
    if (el.scrollTop < 200) void loadOlder(chat.id)
  }, [chat.id, loadOlder])

  useLayoutEffect(() => {
    const el = scroller.current
    if (!el) return
    const firstSeq = items[0]?.seq ?? null
    if (prevFirstSeq.current !== null && firstSeq !== null && firstSeq < prevFirstSeq.current) {
      el.scrollTop += el.scrollHeight - prevHeight.current
    } else if (stickToBottom.current) {
      el.scrollTop = el.scrollHeight
    }
    prevFirstSeq.current = firstSeq
    prevHeight.current = el.scrollHeight
  }, [items, pending, isTyping])

  const status = isTyping ? 'печатает…' : lastSeenText(chat.peer.online, chat.peer.last_seen_at, now)

  return (
    <section className="conversation" aria-label={`Чат с ${chat.peer.display_name}`}>
      <header className="conversation__head">
        <Link to="/" className="icon-btn conversation__back" aria-label="Назад к чатам">
          <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
            <path d="M15 5 8 12l7 7" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </Link>
        <Avatar id={chat.peer.id} name={chat.peer.display_name} size={40} online={chat.peer.online} />
        <div className="conversation__who">
          <h1 className="conversation__name">{chat.peer.display_name}</h1>
          <p className={`conversation__status${isTyping || chat.peer.online ? ' conversation__status--live' : ''}`} data-testid="peer-status">
            {status}
          </p>
        </div>
      </header>

      <div className="messages" ref={scroller} onScroll={onScroll} role="log" aria-live="polite" aria-label="Сообщения">
        {state?.status === 'loading' && <p className="messages__note">Загружаем сообщения…</p>}
        {state?.status === 'error' && <p className="messages__note">Не удалось загрузить сообщения</p>}
        {state?.loadingOlder && <p className="messages__note">Загружаем историю…</p>}
        {state?.status === 'ready' && items.length === 0 && !pending?.length && (
          <div className="messages__intro">
            <Avatar id={chat.peer.id} name={chat.peer.display_name} size={72} />
            <p>Это начало переписки с @{chat.peer.username}</p>
          </div>
        )}
        <MessageList items={items} pending={pending ?? []} me={me} peerReadSeq={chat.peer_last_read_seq} chatId={chat.id} />
        {isTyping && (
          <div className="typing" aria-label="печатает">
            <span />
            <span />
            <span />
          </div>
        )}
      </div>

      <Composer chatId={chat.id} onSent={() => (stickToBottom.current = true)} />
    </section>
  )
}

function MessageList({
  items,
  pending,
  me,
  peerReadSeq,
  chatId,
}: {
  items: Message[]
  pending: PendingMessage[]
  me: string
  peerReadSeq: number
  chatId: string
}) {
  const retry = useMessages((s) => s.retry)
  const discard = useMessages((s) => s.discard)
  let prevDay: number | null = null

  const day = (iso: string) => {
    const key = dayKey(iso)
    const show = key !== prevDay
    prevDay = key
    return show ? (
      <div className="day-sep" role="separator">
        <span>{formatDay(iso)}</span>
      </div>
    ) : null
  }

  return (
    <>
      {items.map((m, i) => {
        const mine = m.sender_id === me
        const next = items[i + 1]
        const tail = !next || next.sender_id !== m.sender_id || dayKey(next.created_at) !== dayKey(m.created_at)
        return (
          <Fragment key={m.id}>
            {day(m.created_at)}
            <div className={`bubble-row${mine ? ' bubble-row--mine' : ''}${tail ? ' bubble-row--tail' : ''}`} data-seq={m.seq}>
              <div className="bubble">
                <span className="bubble__text">{m.body}</span>
                <span className="bubble__meta">
                  <time dateTime={m.created_at}>{formatTime(m.created_at)}</time>
                  {mine && <Ticks read={peerReadSeq >= m.seq} />}
                </span>
              </div>
            </div>
          </Fragment>
        )
      })}
      {pending.map((p) => (
        <Fragment key={p.client_id}>
          {day(p.created_at)}
          <div className="bubble-row bubble-row--mine bubble-row--tail" data-pending={p.status}>
            <div className={`bubble${p.status === 'failed' ? ' bubble--failed' : ''}`}>
              <span className="bubble__text">{p.body}</span>
              <span className="bubble__meta">
                {p.status === 'sending' ? (
                  <span className="clock" aria-label="Отправляется" />
                ) : (
                  <span className="bubble__failed">Не отправлено</span>
                )}
              </span>
            </div>
            {p.status === 'failed' && (
              <div className="bubble__actions">
                <button type="button" onClick={() => void retry(chatId, p.client_id)}>
                  Повторить
                </button>
                <button type="button" onClick={() => discard(chatId, p.client_id)}>
                  Удалить
                </button>
              </div>
            )}
          </div>
        </Fragment>
      ))}
    </>
  )
}

function Composer({ chatId, onSent }: { chatId: string; onSent: () => void }) {
  const send = useMessages((s) => s.send)
  const [text, setText] = useState('')
  const area = useRef<HTMLTextAreaElement>(null)
  const lastTyping = useRef(0)

  useEffect(() => {
    setText('')
    area.current?.focus()
  }, [chatId])

  useLayoutEffect(() => {
    const el = area.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`
  }, [text])

  const trimmed = text.trim()
  const tooLong = [...trimmed].length > MAX_LEN

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    if (!trimmed || tooLong) return
    onSent()
    void send(chatId, trimmed)
    setText('')
    lastTyping.current = 0
    area.current?.focus()
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit()
    }
  }

  const onChange = (value: string) => {
    setText(value)
    const now = Date.now()
    if (value.trim() && now - lastTyping.current > TYPING_EVERY) {
      lastTyping.current = now
      realtime.sendTyping(chatId)
    }
  }

  return (
    <form className="composer" onSubmit={submit}>
      <textarea
        ref={area}
        className="composer__input"
        rows={1}
        placeholder="Сообщение"
        aria-label="Сообщение"
        value={text}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
        enterKeyHint="send"
      />
      {tooLong && <span className="composer__limit">{[...trimmed].length}/{MAX_LEN}</span>}
      <button type="submit" className="composer__send" disabled={!trimmed || tooLong} aria-label="Отправить">
        <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
          <path d="M4 12 20 4l-6 16-2.5-6.5L4 12Z" fill="currentColor" />
        </svg>
      </button>
    </form>
  )
}
