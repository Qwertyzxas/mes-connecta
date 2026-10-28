import { useEffect, useRef, useState } from 'react'
import { Track, type Participant } from 'livekit-client'
import { Avatar } from './Avatar'
import { PhoneIcon, VideoIcon } from './CallLayer'
import { enableAudio, hangUp, toggleCamera, toggleMic, useCall } from '../lib/calls'

function useNow(active: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [active])
  return now
}

function clock(ms: number) {
  const total = Math.max(0, Math.floor(ms / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = String(total % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`
}

function Tile({ participant, local }: { participant: Participant; local?: boolean }) {
  useCall((s) => s.version)
  const videoRef = useRef<HTMLVideoElement>(null)
  const audioRef = useRef<HTMLAudioElement>(null)
  const cam = participant.getTrackPublication(Track.Source.Camera)
  const mic = participant.getTrackPublication(Track.Source.Microphone)
  const videoTrack = cam && !cam.isMuted ? cam.track : undefined
  const audioTrack = local ? undefined : mic?.track
  const name = participant.name || participant.identity

  useEffect(() => {
    const el = videoRef.current
    if (!videoTrack || !el) return
    videoTrack.attach(el)
    return () => {
      videoTrack.detach(el)
    }
  }, [videoTrack])

  useEffect(() => {
    const el = audioRef.current
    if (!audioTrack || !el) return
    audioTrack.attach(el)
    return () => {
      audioTrack.detach(el)
    }
  }, [audioTrack])

  return (
    <div
      className={`call-tile${local ? ' call-tile--local' : ''}${participant.isSpeaking ? ' call-tile--speaking' : ''}`}
      data-testid={local ? 'call-self' : 'call-participant'}
      data-identity={participant.identity}
    >
      {videoTrack ? (
        <video ref={videoRef} autoPlay playsInline muted className={local ? 'call-tile__video--mirror' : undefined} />
      ) : (
        <Avatar id={participant.identity} name={name} size={local ? 56 : 112} />
      )}
      {!local && <audio ref={audioRef} autoPlay />}
      {!local && (
        <span className="call-tile__name">
          {name}
          {mic?.isMuted || !mic ? <span aria-label="микрофон выключен"> · без звука</span> : null}
        </span>
      )}
    </div>
  )
}

function MicIcon({ off }: { off: boolean }) {
  return (
    <svg viewBox="0 0 24 24" width="24" height="24" aria-hidden="true">
      <rect x="9" y="3" width="6" height="11" rx="3" fill="currentColor" />
      <path d="M5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      {off && <path d="M4 4l16 16" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" />}
    </svg>
  )
}

export default function CallScreen() {
  const phase = useCall((s) => s.phase)
  const call = useCall((s) => s.call)
  const title = useCall((s) => s.title)
  const avatarId = useCall((s) => s.avatarId)
  const room = useCall((s) => s.room)
  const connected = useCall((s) => s.connected)
  const micOn = useCall((s) => s.micOn)
  const camOn = useCall((s) => s.camOn)
  const error = useCall((s) => s.error)
  const endedText = useCall((s) => s.endedText)
  const audioBlocked = useCall((s) => s.audioBlocked)
  useCall((s) => s.version)

  const answeredAt = call?.answered_at ? Date.parse(call.answered_at) : null
  const now = useNow(phase === 'active' && answeredAt !== null)
  const remotes = room ? Array.from(room.remoteParticipants.values()) : []
  const local = room?.localParticipant

  let status: string
  if (phase === 'ended') status = endedText ?? 'Звонок завершён'
  else if (phase === 'connecting') status = 'Соединение…'
  else if (phase === 'outgoing') status = call?.chat_kind === 'group' ? 'Ожидание участников…' : 'Вызов…'
  else if (!connected) status = 'Соединение…'
  else if (answeredAt === null || (remotes.length === 0 && call?.chat_kind === 'group')) status = 'Ожидание участников…'
  else status = clock(now - answeredAt)

  const live = phase !== 'ended'
  const showRemotes = remotes.length > 0 && live

  return (
    <section className={`call${phase === 'ended' ? ' call--ended' : ''}`} role="dialog" aria-label="Звонок" aria-modal="true">
      <header className="call__head">
        <h2 className="call__title">{title}</h2>
        <p className="call__status" data-testid="call-status" aria-live="polite">
          {status}
        </p>
      </header>

      <div className={`call__stage call__stage--${Math.min(remotes.length, 4)}`}>
        {showRemotes ? (
          remotes.map((p) => <Tile key={p.identity} participant={p} />)
        ) : (
          <div className="call__waiting">
            <Avatar id={avatarId} name={title} size={128} />
          </div>
        )}
        {local && camOn && live && <Tile participant={local} local />}
      </div>

      {error && live && (
        <p className="call__error" role="alert">
          {error}
        </p>
      )}
      {audioBlocked && live && (
        <button type="button" className="call__unmute" onClick={() => void enableAudio()}>
          Включить звук собеседника
        </button>
      )}

      <footer className="call__controls">
        <button
          type="button"
          className={`call-btn call-btn--toggle${micOn ? '' : ' call-btn--off'}`}
          aria-label={micOn ? 'Выключить микрофон' : 'Включить микрофон'}
          aria-pressed={!micOn}
          disabled={!connected || !live}
          onClick={() => void toggleMic()}
        >
          <MicIcon off={!micOn} />
        </button>
        <button
          type="button"
          className={`call-btn call-btn--toggle${camOn ? '' : ' call-btn--off'}`}
          aria-label={camOn ? 'Выключить камеру' : 'Включить камеру'}
          aria-pressed={camOn}
          disabled={!connected || !live}
          onClick={() => void toggleCamera()}
        >
          <VideoIcon size={24} />
        </button>
        <button
          type="button"
          className="call-btn call-btn--decline"
          aria-label="Завершить звонок"
          disabled={!live}
          onClick={() => void hangUp()}
        >
          <span className="call-btn__hang">
            <PhoneIcon />
          </span>
        </button>
      </footer>
    </section>
  )
}
