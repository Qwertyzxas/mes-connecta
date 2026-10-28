import { lazy, Suspense, useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Avatar } from './Avatar'
import { chatsKey } from '../lib/chat-cache'
import { acceptCall, declineCall, handleCallEvent, leaveOnUnload, setCallUser, syncLiveCalls, useCall } from '../lib/calls'
import { realtime } from '../lib/realtime'
import type { Chat } from '../lib/types'
import { useAuth } from '../store/auth'

const CallScreen = lazy(() => import('./CallScreen'))

export function PhoneIcon({ size = 22 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <path
        d="M6.6 3.5h2.6l1.4 4.2-2 1.6a12 12 0 0 0 6.1 6.1l1.6-2 4.2 1.4v2.6a2 2 0 0 1-2.2 2A16.6 16.6 0 0 1 4.6 5.7a2 2 0 0 1 2-2.2Z"
        fill="currentColor"
      />
    </svg>
  )
}

export function VideoIcon({ size = 22 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <rect x="2.5" y="6" width="13" height="12" rx="3" fill="currentColor" />
      <path d="m16.5 10.2 5-3v9.6l-5-3Z" fill="currentColor" />
    </svg>
  )
}

function IncomingCall() {
  const title = useCall((s) => s.title)
  const avatarId = useCall((s) => s.avatarId)
  const video = useCall((s) => s.video)
  const caller = useCall((s) => s.caller)
  const kind = useCall((s) => s.call?.chat_kind)
  const label = video ? 'Входящий видеозвонок' : 'Входящий звонок'

  return (
    <div className="call-incoming" role="alertdialog" aria-label={label} aria-describedby="call-incoming-title">
      <Avatar id={avatarId} name={title} size={52} />
      <div className="call-incoming__text">
        <strong id="call-incoming-title">{title}</strong>
        <span>{kind === 'group' && caller ? `${caller.display_name} · ${label.toLowerCase()}` : label}</span>
      </div>
      <button type="button" className="call-btn call-btn--decline" aria-label="Отклонить" onClick={() => void declineCall()}>
        <span className="call-btn__hang">
          <PhoneIcon />
        </span>
      </button>
      <button type="button" className="call-btn call-btn--accept" aria-label="Принять" onClick={() => void acceptCall()}>
        {video ? <VideoIcon /> : <PhoneIcon />}
      </button>
    </div>
  )
}

export function CallLayer() {
  const me = useAuth((s) => s.user?.id)
  const qc = useQueryClient()
  const phase = useCall((s) => s.phase)

  useEffect(() => {
    if (!me) return
    setCallUser(me)
    return realtime.subscribe((event) => {
      if (event.type === 'ready') void syncLiveCalls(me, qc.getQueryData<Chat[]>(chatsKey))
      else handleCallEvent(me, event)
    })
  }, [me, qc])

  useEffect(() => {
    window.addEventListener('pagehide', leaveOnUnload)
    return () => window.removeEventListener('pagehide', leaveOnUnload)
  }, [])

  if (phase === 'idle') return null
  if (phase === 'incoming') return <IncomingCall />
  return (
    <Suspense fallback={<div className="call" role="dialog" aria-label="Звонок" />}>
      <CallScreen />
    </Suspense>
  )
}
