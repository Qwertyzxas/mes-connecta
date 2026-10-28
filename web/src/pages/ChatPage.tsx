import {
  Fragment,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from 'react'
import { Link, useParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Avatar } from '../components/Avatar'
import { Ticks } from '../components/Sidebar'
import { ApiError } from '../lib/api'
import { chatApi } from '../lib/chat-api'
import { chatKey, chatsKey, markReadLocally } from '../lib/chat-cache'
import { describeError } from '../lib/errors'
import { dayKey, formatDay, formatTime, lastSeenText } from '../lib/format'
import { useDocumentVisible, useNow } from '../lib/hooks'
import { realtime } from '../lib/realtime'
import type { Chat, Message, ReplyPreview } from '../lib/types'
import { useAuth } from '../store/auth'
import { selectDraft, useMessages, type PendingMessage } from '../store/messages'

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

function scrollToSeq(seq: number) {
  const el = document.querySelector<HTMLElement>(`.bubble-row[data-seq="${seq}"]`)
  if (!el) return
  el.scrollIntoView({ block: 'center', behavior: 'smooth' })
  el.classList.remove('bubble-row--flash')
  void el.offsetWidth
  el.classList.add('bubble-row--flash')
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
  const [menuFor, setMenuFor] = useState<string | null>(null)

  const items = useMemo(() => state?.items ?? [], [state?.items])

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

  useEffect(() => {
    if (!menuFor) return
    const close = (e: Event) => {
      if (e instanceof window.KeyboardEvent && e.key !== 'Escape') return
      if (e.target instanceof Element && e.target.closest('.msg-menu, .msg-more')) return
      setMenuFor(null)
    }
    document.addEventListener('pointerdown', close)
    document.addEventListener('keydown', close)
    return () => {
      document.removeEventListener('pointerdown', close)
      document.removeEventListener('keydown', close)
    }
  }, [menuFor])

  const lastOwn = useMemo(() => {
    for (let i = items.length - 1; i >= 0; i--) {
      const m = items[i]!
      if (m.sender_id === me && !m.deleted_at) return m
    }
    return null
  }, [items, me])

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
          <p
            className={`conversation__status${isTyping || chat.peer.online ? ' conversation__status--live' : ''}`}
            data-testid="peer-status"
          >
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
        <MessageList
          items={items}
          pending={pending ?? []}
          me={me}
          peerReadSeq={chat.peer_last_read_seq}
          chatId={chat.id}
          menuFor={menuFor}
          setMenuFor={setMenuFor}
        />
        {isTyping && (
          <div className="typing" aria-label="печатает">
            <span />
            <span />
            <span />
          </div>
        )}
      </div>

      <Composer chatId={chat.id} lastOwn={lastOwn} onSent={() => (stickToBottom.current = true)} />
    </section>
  )
}

type ListProps = {
  items: Message[]
  pending: PendingMessage[]
  me: string
  peerReadSeq: number
  chatId: string
  menuFor: string | null
  setMenuFor: (id: string | null) => void
}

function MessageList({ items, pending, me, peerReadSeq, chatId, menuFor, setMenuFor }: ListProps) {
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
        const next = items[i + 1]
        const tail = !next || next.sender_id !== m.sender_id || dayKey(next.created_at) !== dayKey(m.created_at)
        return (
          <Fragment key={m.id}>
            {day(m.created_at)}
            <MessageBubble
              message={m}
              mine={m.sender_id === me}
              me={me}
              tail={tail}
              read={peerReadSeq >= m.seq}
              chatId={chatId}
              menuOpen={menuFor === m.id}
              onToggleMenu={() => setMenuFor(menuFor === m.id ? null : m.id)}
              onCloseMenu={() => setMenuFor(null)}
            />
          </Fragment>
        )
      })}
      {pending.map((p) => (
        <Fragment key={p.client_id}>
          {day(p.created_at)}
          <div className="bubble-row bubble-row--mine bubble-row--tail" data-pending={p.status}>
            <div className={`bubble${p.status === 'failed' ? ' bubble--failed' : ''}`}>
              {p.reply_to && <Quote reply={p.reply_to} me={me} />}
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

function Quote({ reply, me }: { reply: ReplyPreview; me: string }) {
  return (
    <button type="button" className="quote" onClick={() => scrollToSeq(reply.seq)}>
      <span className="quote__author">{reply.sender_id === me ? 'Вы' : reply.sender_name}</span>
      <span className={`quote__text${reply.deleted ? ' quote__text--deleted' : ''}`}>
        {reply.deleted ? 'Сообщение удалено' : reply.body}
      </span>
    </button>
  )
}

type BubbleProps = {
  message: Message
  mine: boolean
  me: string
  tail: boolean
  read: boolean
  chatId: string
  menuOpen: boolean
  onToggleMenu: () => void
  onCloseMenu: () => void
}

function MessageBubble({ message: m, mine, me, tail, read, chatId, menuOpen, onToggleMenu, onCloseMenu }: BubbleProps) {
  const deleted = Boolean(m.deleted_at)
  const classes = ['bubble-row', mine && 'bubble-row--mine', tail && 'bubble-row--tail', deleted && 'bubble-row--deleted']
    .filter(Boolean)
    .join(' ')

  return (
    <div className={classes} data-seq={m.seq} data-id={m.id}>
      <div className="bubble-wrap">
        <div className="bubble" onContextMenu={(e) => {
          if (deleted) return
          e.preventDefault()
          onToggleMenu()
        }}>
          {m.reply_to && !deleted && <Quote reply={m.reply_to} me={me} />}
          <span className="bubble__text">{deleted ? 'Сообщение удалено' : m.body}</span>
          <span className="bubble__meta">
            {m.edited_at && !deleted && <span className="bubble__edited">изменено</span>}
            <time dateTime={m.created_at}>{formatTime(m.created_at)}</time>
            {mine && !deleted && <Ticks read={read} />}
          </span>
        </div>
        {!deleted && (
          <button
            type="button"
            className="msg-more"
            aria-label="Действия с сообщением"
            aria-expanded={menuOpen}
            onClick={onToggleMenu}
          >
            <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
              <circle cx="5" cy="12" r="1.8" fill="currentColor" />
              <circle cx="12" cy="12" r="1.8" fill="currentColor" />
              <circle cx="19" cy="12" r="1.8" fill="currentColor" />
            </svg>
          </button>
        )}
        {menuOpen && <MessageMenu message={m} mine={mine} chatId={chatId} onClose={onCloseMenu} />}
      </div>
    </div>
  )
}

function MessageMenu({ message, mine, chatId, onClose }: { message: Message; mine: boolean; chatId: string; onClose: () => void }) {
  const setReply = useMessages((s) => s.setReply)
  const setEditing = useMessages((s) => s.setEditing)
  const remove = useMessages((s) => s.remove)
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const del = async () => {
    setBusy(true)
    setError(null)
    try {
      await remove(chatId, message.id)
      onClose()
    } catch (err) {
      setError(describeError(err).message)
      setBusy(false)
    }
  }

  if (confirming) {
    return (
      <div className="msg-menu" role="dialog" aria-label="Удаление сообщения">
        <p className="msg-menu__title">Удалить у всех?</p>
        {error && <p className="msg-menu__error">{error}</p>}
        <div className="msg-menu__row">
          <button type="button" className="msg-menu__danger" onClick={() => void del()} disabled={busy}>
            Удалить
          </button>
          <button type="button" onClick={onClose}>
            Отмена
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="msg-menu" role="menu" aria-label="Действия с сообщением">
      <button
        type="button"
        role="menuitem"
        onClick={() => {
          setReply(chatId, message)
          onClose()
        }}
      >
        Ответить
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => {
          void navigator.clipboard?.writeText(message.body)
          onClose()
        }}
      >
        Копировать
      </button>
      {mine && (
        <button
          type="button"
          role="menuitem"
          onClick={() => {
            setEditing(chatId, message)
            onClose()
          }}
        >
          Изменить
        </button>
      )}
      {mine && (
        <button type="button" role="menuitem" className="msg-menu__danger" onClick={() => setConfirming(true)}>
          Удалить
        </button>
      )}
    </div>
  )
}

function Composer({ chatId, lastOwn, onSent }: { chatId: string; lastOwn: Message | null; onSent: () => void }) {
  const me = useAuth((s) => s.user?.id ?? '')
  const send = useMessages((s) => s.send)
  const edit = useMessages((s) => s.edit)
  const setReply = useMessages((s) => s.setReply)
  const setEditing = useMessages((s) => s.setEditing)
  const draft = useMessages(selectDraft(chatId))
  const [text, setText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const area = useRef<HTMLTextAreaElement>(null)
  const lastTyping = useRef(0)
  const editingId = draft.editing?.id

  useEffect(() => {
    setText('')
    area.current?.focus()
  }, [chatId])

  useEffect(() => {
    setError(null)
    if (draft.editing) setText(draft.editing.body)
    if (draft.editing || draft.replyTo) area.current?.focus()
  }, [editingId, draft.editing, draft.replyTo])

  useLayoutEffect(() => {
    const el = area.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`
  }, [text])

  const trimmed = text.trim()
  const tooLong = [...trimmed].length > MAX_LEN

  const cancel = () => {
    if (draft.editing) {
      setEditing(chatId, null)
      setText('')
    } else if (draft.replyTo) {
      setReply(chatId, null)
    }
    setError(null)
  }

  const submit = async (e?: FormEvent) => {
    e?.preventDefault()
    if (!trimmed || tooLong || saving) return

    if (draft.editing) {
      const target = draft.editing
      if (trimmed === target.body) {
        cancel()
        return
      }
      setSaving(true)
      try {
        await edit(chatId, target.id, trimmed)
        setEditing(chatId, null)
        setText('')
      } catch (err) {
        setError(describeError(err).message)
      } finally {
        setSaving(false)
      }
      return
    }

    onSent()
    void send(chatId, trimmed, draft.replyTo)
    if (draft.replyTo) setReply(chatId, null)
    setText('')
    lastTyping.current = 0
    area.current?.focus()
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void submit()
    } else if (e.key === 'Escape' && (draft.editing || draft.replyTo)) {
      e.preventDefault()
      cancel()
    } else if (e.key === 'ArrowUp' && !text && !draft.editing && lastOwn) {
      e.preventDefault()
      setEditing(chatId, lastOwn)
    }
  }

  const onChange = (value: string) => {
    setText(value)
    const now = Date.now()
    if (!draft.editing && value.trim() && now - lastTyping.current > TYPING_EVERY) {
      lastTyping.current = now
      realtime.sendTyping(chatId)
    }
  }

  const context = draft.editing ?? draft.replyTo

  return (
    <form className="composer" onSubmit={(e) => void submit(e)}>
      {context && (
        <div className="composer__context" data-testid="composer-context">
          <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true" className="composer__context-icon">
            {draft.editing ? (
              <path d="M4 20h4l10-10-4-4L4 16v4Zm12-14 2-2 4 4-2 2" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
            ) : (
              <path d="M10 8V4l-7 7 7 7v-4c5 0 8.5 1.6 11 5-1-5-4-10-11-11Z" fill="currentColor" />
            )}
          </svg>
          <span className="composer__context-text">
            <span className="composer__context-title">
              {draft.editing
                ? 'Редактирование'
                : `В ответ ${context.sender_id === me ? 'себе' : context.sender.display_name}`}
            </span>
            <span className="composer__context-body">{context.body}</span>
          </span>
          <button type="button" className="composer__context-close" onClick={cancel} aria-label="Отменить">
            ×
          </button>
        </div>
      )}
      {error && <p className="composer__error" role="alert">{error}</p>}
      <div className="composer__row">
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
        {tooLong && (
          <span className="composer__limit">
            {[...trimmed].length}/{MAX_LEN}
          </span>
        )}
        <button
          type="submit"
          className="composer__send"
          disabled={!trimmed || tooLong || saving}
          aria-label={draft.editing ? 'Сохранить' : 'Отправить'}
        >
          {draft.editing ? (
            <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
              <path d="m5 12.5 4.5 4.5L19 7.5" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          ) : (
            <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
              <path d="M4 12 20 4l-6 16-2.5-6.5L4 12Z" fill="currentColor" />
            </svg>
          )}
        </button>
      </div>
    </form>
  )
}
