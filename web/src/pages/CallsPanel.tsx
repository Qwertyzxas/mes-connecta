import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { Avatar } from '../components/Avatar'
import { PhoneIcon, VideoIcon } from '../components/CallLayer'
import { startCall, useCall } from '../lib/calls'
import { callApi, chatApi } from '../lib/chat-api'
import { chatKey, chatsKey } from '../lib/chat-cache'
import { describeError } from '../lib/errors'
import { formatListTime } from '../lib/format'
import type { CallEntry, Chat } from '../lib/types'
import { useAuth } from '../store/auth'

export const callsKey = ['calls'] as const

function duration(entry: CallEntry): string | null {
  if (!entry.answered_at || !entry.ended_at) return null
  const total = Math.max(0, Math.round((Date.parse(entry.ended_at) - Date.parse(entry.answered_at)) / 1000))
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

function describe(entry: CallEntry, me: string): { text: string; missed: boolean } {
  const outgoing = entry.started_by === me
  const kind = entry.video ? 'видеозвонок' : 'звонок'
  switch (entry.status) {
    case 'missed':
      return { text: outgoing ? `Отменённый ${kind}` : `Пропущенный ${kind}`, missed: !outgoing }
    case 'declined':
      return { text: outgoing ? `${entry.video ? 'Видеозвонок' : 'Звонок'} отклонён` : `Отклонённый ${kind}`, missed: false }
    case 'ringing':
    case 'active':
      return { text: 'Идёт сейчас', missed: false }
    default: {
      const d = duration(entry)
      return { text: `${outgoing ? 'Исходящий' : 'Входящий'} ${kind}${d ? ` · ${d}` : ''}`, missed: false }
    }
  }
}

function Direction({ outgoing, missed }: { outgoing: boolean; missed: boolean }) {
  return (
    <svg
      className={`call-dir${missed ? ' call-dir--missed' : ''}`}
      viewBox="0 0 16 16"
      width="14"
      height="14"
      aria-hidden="true"
    >
      <path
        d={outgoing ? 'M4 12 12 4M6 4h6v6' : 'M12 4 4 12M10 12H4V6'}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

export function CallsPanel() {
  const me = useAuth((s) => s.user?.id ?? '')
  const qc = useQueryClient()
  const navigate = useNavigate()
  const busy = useCall((s) => s.phase !== 'idle')
  const calls = useQuery({ queryKey: callsKey, queryFn: callApi.history, refetchOnMount: 'always' })

  const chatFor = async (chatId: string): Promise<Chat> =>
    qc.getQueryData<Chat[]>(chatsKey)?.find((c) => c.id === chatId) ??
    (await qc.fetchQuery({ queryKey: chatKey(chatId), queryFn: () => chatApi.get(chatId) }))

  const callBack = async (entry: CallEntry, video: boolean) => {
    const chat = await chatFor(entry.chat_id).catch(() => null)
    if (chat) void startCall(chat, video)
  }

  return (
    <div className="panel-view">
      <header className="panel-view__head">
        <h2 className="panel-view__title">Звонки</h2>
      </header>
      {calls.isPending && <p className="sidebar__empty">Загружаем звонки…</p>}
      {calls.isError && <p className="sidebar__empty">{describeError(calls.error).message}</p>}
      {calls.data?.length === 0 && (
        <div className="sidebar__empty sidebar__empty--intro">
          <p>Звонков пока нет.</p>
          <p>Позвоните из любого личного чата или группы.</p>
        </div>
      )}
      {calls.data && calls.data.length > 0 && (
        <ul className="list" aria-label="История звонков">
          {calls.data.map((entry) => {
            const { text, missed } = describe(entry, me)
            const title = entry.peer?.display_name ?? entry.chat_title
            return (
              <li key={entry.id} className="call-row" data-testid="call-entry">
                <button
                  type="button"
                  className="call-row__main"
                  onClick={() => void navigate(`/chat/${entry.chat_id}`)}
                  aria-label={`${title}: ${text}`}
                >
                  <Avatar id={entry.peer?.id ?? entry.chat_id} name={title} online={entry.peer?.online} />
                  <span className="row__main">
                    <span className="row__line">
                      <span className={`row__title${missed ? ' row__title--missed' : ''}`}>{title}</span>
                      <span className="row__time">{formatListTime(entry.created_at)}</span>
                    </span>
                    <span className="row__sub">
                      <Direction outgoing={entry.started_by === me} missed={missed} />
                      {text}
                    </span>
                  </span>
                </button>
                <button
                  type="button"
                  className="icon-btn call-row__action"
                  aria-label={entry.video ? `Видеозвонок: ${title}` : `Позвонить: ${title}`}
                  disabled={busy}
                  onClick={() => void callBack(entry, entry.video)}
                >
                  {entry.video ? <VideoIcon size={20} /> : <PhoneIcon size={20} />}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
