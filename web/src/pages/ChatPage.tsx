import {
  Fragment,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ClipboardEvent,
  type DragEvent,
  type FormEvent,
  type KeyboardEvent,
} from 'react'
import { Link, Navigate, useParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { MessageAttachments, PendingAttachments } from '../components/Attachments'
import { Avatar } from '../components/Avatar'
import { ChatInfo } from '../components/ChatInfo'
import { Ticks } from '../components/Sidebar'
import { ApiError } from '../lib/api'
import { chatApi } from '../lib/chat-api'
import { chatKey, chatsKey, markReadLocally } from '../lib/chat-cache'
import { canManage, canPost, chatAvatarId, chatTitle, membersText, systemText } from '../lib/chat-display'
import { describeError } from '../lib/errors'
import { dayKey, formatDay, formatTime, lastSeenText } from '../lib/format'
import { useDocumentVisible, useNow } from '../lib/hooks'
import { realtime } from '../lib/realtime'
import { useRecorder } from '../lib/recorder'
import { formatDuration, formatSize, kindOf } from '../lib/upload'
import type { Chat, Member, Message, ReplyPreview } from '../lib/types'
import { useAuth } from '../store/auth'
import { selectDraft, useMessages, type LocalFile, type PendingMessage } from '../store/messages'

const MAX_LEN = 4096
const NEAR_BOTTOM = 120
const TYPING_EVERY = 3000
const MAX_FILES = 10

let localSeq = 0

function toLocalFiles(files: Iterable<File>): LocalFile[] {
  return [...files].map((file) => {
    const kind = kindOf(file)
    return {
      localId: `local-${Date.now()}-${localSeq++}`,
      kind,
      name: file.name || (kind === 'image' ? 'image.png' : 'file'),
      blob: file,
      size: file.size,
      previewUrl: kind === 'image' ? URL.createObjectURL(file) : undefined,
    }
  })
}

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
  const [showInfo, setShowInfo] = useState(false)
  const [staged, setStaged] = useState<LocalFile[]>([])
  const [dragging, setDragging] = useState(false)
  const removed = useMessages((s) => Boolean(s.removed[chat.id]))
  const stage = useCallback((files: Iterable<File>) => {
    const added = toLocalFiles(files)
    setStaged((prev) => [...prev, ...added].slice(0, MAX_FILES))
  }, [])
  const members = useQuery({
    queryKey: ['members', chat.id],
    queryFn: () => chatApi.members(chat.id),
    enabled: chat.kind === 'group',
    staleTime: 60_000,
  })
  const memberNames = useMemo(() => new Map((members.data ?? []).map((m: Member) => [m.id, m.display_name])), [members.data])

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

  const title = chatTitle(chat)
  const typingName = typing && chat.kind === 'group' ? memberNames.get(typing.userId) : undefined
  const status = isTyping
    ? typingName
      ? `${typingName} печатает…`
      : 'печатает…'
    : chat.peer
      ? lastSeenText(chat.peer.online, chat.peer.last_seen_at, now)
      : membersText(chat)
  const live = isTyping || Boolean(chat.peer?.online)

  if (removed) return <Navigate to="/" replace />

  const droppable = canPost(chat)
  const onDragOver = (e: DragEvent) => {
    if (!droppable || !e.dataTransfer.types.includes('Files')) return
    e.preventDefault()
    setDragging(true)
  }
  const onDrop = (e: DragEvent) => {
    if (!droppable) return
    e.preventDefault()
    setDragging(false)
    if (e.dataTransfer.files.length) stage(e.dataTransfer.files)
  }

  return (
    <section
      className={`conversation${dragging ? ' conversation--drop' : ''}`}
      aria-label={`Чат с ${title}`}
      onDragOver={onDragOver}
      onDragLeave={(e) => e.currentTarget === e.target && setDragging(false)}
      onDrop={onDrop}
    >
      <header className="conversation__head">
        <Link to="/" className="icon-btn conversation__back" aria-label="Назад к чатам">
          <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
            <path d="M15 5 8 12l7 7" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </Link>
        <button type="button" className="conversation__avatar-btn" onClick={() => setShowInfo(true)} aria-label="Информация о чате">
          <Avatar id={chatAvatarId(chat)} name={title} size={40} online={chat.peer?.online} />
        </button>
        <div className="conversation__who" onClick={() => setShowInfo(true)}>
          <h1 className="conversation__name">{title}</h1>
          <p className={`conversation__status${live ? ' conversation__status--live' : ''}`} data-testid="peer-status">
            {status}
          </p>
        </div>
      </header>

      <div className="messages" ref={scroller} onScroll={onScroll} role="log" aria-live="polite" aria-label="Сообщения">
        {state?.status === 'loading' && <p className="messages__note">Загружаем сообщения…</p>}
        {state?.status === 'error' && <p className="messages__note">Не удалось загрузить сообщения</p>}
        {state?.loadingOlder && <p className="messages__note">Загружаем историю…</p>}
        {state?.status === 'ready' && items.length === 0 && !pending?.length && chat.peer && (
          <div className="messages__intro">
            <Avatar id={chat.peer.id} name={chat.peer.display_name} size={72} />
            <p>Это начало переписки с @{chat.peer.username}</p>
          </div>
        )}
        <MessageList
          items={items}
          pending={pending ?? []}
          me={me}
          chat={chat}
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

      {canPost(chat) ? (
        <Composer
          chatId={chat.id}
          lastOwn={lastOwn}
          staged={staged}
          setStaged={setStaged}
          onStage={stage}
          onSent={() => (stickToBottom.current = true)}
        />
      ) : (
        <div className="composer composer--readonly" role="note">
          В канале публикуют только администраторы
        </div>
      )}
      {showInfo && <ChatInfo chat={chat} onClose={() => setShowInfo(false)} />}
    </section>
  )
}

type ListProps = {
  items: Message[]
  pending: PendingMessage[]
  me: string
  chat: Chat
  menuFor: string | null
  setMenuFor: (id: string | null) => void
}

function MessageList({ items, pending, me, chat, menuFor, setMenuFor }: ListProps) {
  const chatId = chat.id
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
        if (m.kind === 'system') {
          return (
            <Fragment key={m.id}>
              {day(m.created_at)}
              <div className="system-msg" data-seq={m.seq} role="status">
                <span>{systemText(m, me)}</span>
              </div>
            </Fragment>
          )
        }
        const prev = items[i - 1]
        const next = items[i + 1]
        const sameDay = (a?: Message) => a && dayKey(a.created_at) === dayKey(m.created_at)
        const tail = !next || next.kind !== 'text' || next.sender_id !== m.sender_id || !sameDay(next)
        const head = !prev || prev.kind !== 'text' || prev.sender_id !== m.sender_id || !sameDay(prev)
        const mine = m.sender_id === me
        return (
          <Fragment key={m.id}>
            {day(m.created_at)}
            <MessageBubble
              message={m}
              mine={mine}
              me={me}
              tail={tail}
              author={chat.kind === 'group' && !mine && head ? m.sender.display_name : undefined}
              read={chat.peer_last_read_seq >= m.seq}
              showTicks={chat.kind !== 'channel'}
              chatId={chatId}
              canReply={canPost(chat)}
              canDelete={mine || canManage(chat)}
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
              {p.attachments.length > 0 && <PendingAttachments attachments={p.attachments} />}
              {p.body && <span className="bubble__text">{p.body}</span>}
              <span className="bubble__meta">
                {p.status === 'sending' ? (
                  <span className="clock" aria-label="Отправляется" />
                ) : (
                  <span className="bubble__failed">{p.error ?? 'Не отправлено'}</span>
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
  author?: string
  read: boolean
  showTicks: boolean
  chatId: string
  canReply: boolean
  canDelete: boolean
  menuOpen: boolean
  onToggleMenu: () => void
  onCloseMenu: () => void
}

function authorColor(id: string): string {
  const colors = ['#2f9bf0', '#7a5be6', '#d4568f', '#18a090', '#e07a2c', '#4a9a3c']
  let h = 0
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) | 0
  return colors[Math.abs(h) % colors.length]!
}

function MessageBubble(props: BubbleProps) {
  const { message: m, mine, me, tail, author, read, showTicks, chatId, canReply, canDelete, menuOpen, onToggleMenu, onCloseMenu } = props
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
          {author && (
            <span className="bubble__author" style={{ color: authorColor(m.sender_id) }}>
              {author}
            </span>
          )}
          {m.reply_to && !deleted && <Quote reply={m.reply_to} me={me} />}
          {!deleted && m.attachments?.length > 0 && <MessageAttachments attachments={m.attachments} />}
          {(deleted || m.body) && <span className="bubble__text">{deleted ? 'Сообщение удалено' : m.body}</span>}
          <span className="bubble__meta">
            {m.edited_at && !deleted && <span className="bubble__edited">изменено</span>}
            <time dateTime={m.created_at}>{formatTime(m.created_at)}</time>
            {mine && !deleted && showTicks && <Ticks read={read} />}
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
        {menuOpen && (
          <MessageMenu message={m} mine={mine} chatId={chatId} canReply={canReply} canDelete={canDelete} onClose={onCloseMenu} />
        )}
      </div>
    </div>
  )
}

type MenuProps = {
  message: Message
  mine: boolean
  chatId: string
  canReply: boolean
  canDelete: boolean
  onClose: () => void
}

function MessageMenu({ message, mine, chatId, canReply, canDelete, onClose }: MenuProps) {
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
        <p className="msg-menu__title">{mine ? 'Удалить у всех?' : 'Удалить сообщение участника?'}</p>
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
      {canReply && (
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
      )}
      <button
        type="button"
        role="menuitem"
        onClick={() => {
          void navigator.clipboard?.writeText(message.body || message.attachments.map((a) => a.name).join(', '))
          onClose()
        }}
      >
        Копировать
      </button>
      {mine && message.body && (
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
      {canDelete && (
        <button type="button" role="menuitem" className="msg-menu__danger" onClick={() => setConfirming(true)}>
          Удалить
        </button>
      )}
    </div>
  )
}

type ComposerProps = {
  chatId: string
  lastOwn: Message | null
  staged: LocalFile[]
  setStaged: (files: LocalFile[]) => void
  onStage: (files: Iterable<File>) => void
  onSent: () => void
}

function Composer({ chatId, lastOwn, staged, setStaged, onStage, onSent }: ComposerProps) {
  const me = useAuth((s) => s.user?.id ?? '')
  const send = useMessages((s) => s.send)
  const edit = useMessages((s) => s.edit)
  const setReply = useMessages((s) => s.setReply)
  const setEditing = useMessages((s) => s.setEditing)
  const draft = useMessages(selectDraft(chatId))
  const recorder = useRecorder()
  const [text, setText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const area = useRef<HTMLTextAreaElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
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
  const hasContent = Boolean(trimmed) || staged.length > 0
  const editing = Boolean(draft.editing)

  const cancel = () => {
    if (draft.editing) {
      setEditing(chatId, null)
      setText('')
    } else if (draft.replyTo) {
      setReply(chatId, null)
    }
    setError(null)
  }

  const unstage = (localId: string) => {
    const target = staged.find((f) => f.localId === localId)
    if (target?.previewUrl) URL.revokeObjectURL(target.previewUrl)
    setStaged(staged.filter((f) => f.localId !== localId))
  }

  const submit = async (e?: FormEvent) => {
    e?.preventDefault()
    if (tooLong || saving) return

    if (draft.editing) {
      if (!trimmed) return
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

    if (!hasContent) return
    onSent()
    void send(chatId, trimmed, draft.replyTo, staged)
    if (draft.replyTo) setReply(chatId, null)
    setText('')
    setStaged([])
    lastTyping.current = 0
    area.current?.focus()
  }

  const sendVoice = async () => {
    const recording = await recorder.stop()
    if (!recording) return
    onSent()
    void send(chatId, '', draft.replyTo, [
      {
        localId: `voice-${Date.now()}`,
        kind: 'voice',
        name: recording.name,
        blob: recording.blob,
        size: recording.blob.size,
        durationMs: recording.durationMs,
      },
    ])
    if (draft.replyTo) setReply(chatId, null)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void submit()
    } else if (e.key === 'Escape' && (draft.editing || draft.replyTo)) {
      e.preventDefault()
      cancel()
    } else if (e.key === 'ArrowUp' && !text && !draft.editing && staged.length === 0 && lastOwn) {
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

  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    if (editing || e.clipboardData.files.length === 0) return
    e.preventDefault()
    onStage(e.clipboardData.files)
  }

  const context = draft.editing ?? draft.replyTo
  const shownError = error ?? recorder.error

  if (recorder.state !== 'idle') {
    return (
      <div className="composer composer--recording" role="group" aria-label="Запись голосового">
        <div className="composer__row">
          <button type="button" className="composer__icon" onClick={recorder.cancel} aria-label="Отменить запись">
            <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
              <path d="M5 7h14M10 7V5h4v2m-7 0 1 13h8l1-13" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
            </svg>
          </button>
          <span className="recording" aria-live="polite">
            <span className="recording__dot" aria-hidden="true" />
            <span data-testid="recording-time">{formatDuration(recorder.elapsed)}</span>
          </span>
          <button
            type="button"
            className="composer__send"
            onClick={() => void sendVoice()}
            disabled={recorder.state !== 'recording'}
            aria-label="Отправить голосовое"
          >
            <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
              <path d="M4 12 20 4l-6 16-2.5-6.5L4 12Z" fill="currentColor" />
            </svg>
          </button>
        </div>
      </div>
    )
  }

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
            <span className="composer__context-body">{context.body || 'Вложение'}</span>
          </span>
          <button type="button" className="composer__context-close" onClick={cancel} aria-label="Отменить">
            ×
          </button>
        </div>
      )}
      {staged.length > 0 && (
        <ul className="staged" aria-label="Вложения к отправке">
          {staged.map((f) => (
            <li key={f.localId} className="staged__item">
              {f.previewUrl ? (
                <img src={f.previewUrl} alt={f.name} />
              ) : (
                <span className="staged__file">
                  <span className="staged__name">{f.name}</span>
                  <span className="staged__size">{formatSize(f.size)}</span>
                </span>
              )}
              <button type="button" onClick={() => unstage(f.localId)} aria-label={`Убрать ${f.name}`}>
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      {shownError && (
        <p className="composer__error" role="alert">
          {shownError}
        </p>
      )}
      <div className="composer__row">
        {!editing && (
          <>
            <button
              type="button"
              className="composer__icon"
              onClick={() => fileInput.current?.click()}
              aria-label="Прикрепить"
              disabled={staged.length >= MAX_FILES}
            >
              <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
                <path
                  d="m20 11.5-7.8 7.8a5 5 0 0 1-7.1-7.1l8.5-8.5a3.3 3.3 0 0 1 4.7 4.7l-8.5 8.5a1.7 1.7 0 0 1-2.4-2.4l7.8-7.8"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.8"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            </button>
            <input
              ref={fileInput}
              type="file"
              multiple
              hidden
              aria-label="Прикрепить файл"
              onChange={(e) => {
                if (e.target.files?.length) onStage(e.target.files)
                e.target.value = ''
              }}
            />
          </>
        )}
        <textarea
          ref={area}
          className="composer__input"
          rows={1}
          placeholder="Сообщение"
          aria-label="Сообщение"
          value={text}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          enterKeyHint="send"
        />
        {tooLong && (
          <span className="composer__limit">
            {[...trimmed].length}/{MAX_LEN}
          </span>
        )}
        {!editing && !hasContent ? (
          <button type="button" className="composer__send" onClick={() => void recorder.start()} aria-label="Записать голосовое">
            <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
              <path d="M12 3a3 3 0 0 0-3 3v6a3 3 0 0 0 6 0V6a3 3 0 0 0-3-3Zm-6 9a6 6 0 0 0 12 0M12 18v3" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" />
            </svg>
          </button>
        ) : (
          <button
            type="submit"
            className="composer__send"
            disabled={(editing ? !trimmed : !hasContent) || tooLong || saving}
            aria-label={editing ? 'Сохранить' : 'Отправить'}
          >
            {editing ? (
              <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
                <path d="m5 12.5 4.5 4.5L19 7.5" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            ) : (
              <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
                <path d="M4 12 20 4l-6 16-2.5-6.5L4 12Z" fill="currentColor" />
              </svg>
            )}
          </button>
        )}
      </div>
    </form>
  )
}
