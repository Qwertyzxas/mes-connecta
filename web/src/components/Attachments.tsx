import { useEffect, useRef, useState, type SyntheticEvent } from 'react'
import { chatApi } from '../lib/chat-api'
import { decryptBlob, type SecretAttachment } from '../lib/e2e'
import type { Attachment } from '../lib/types'
import { formatDuration, formatSize } from '../lib/upload'
import type { PendingAttachment } from '../store/messages'

const secretUrls = new Map<string, Promise<string>>()

function secretUrl(att: SecretAttachment): Promise<string> {
  let pending = secretUrls.get(att.id)
  if (!pending) {
    pending = (async () => {
      let res = await fetch(att.url)
      if (!res.ok) res = await fetch((await chatApi.fileUrl(att.id)).url)
      if (!res.ok) throw new Error('download failed')
      const blob = await decryptBlob(await res.arrayBuffer(), att.secret.key, att.secret.iv, att.content_type)
      return URL.createObjectURL(blob)
    })()
    pending.catch(() => secretUrls.delete(att.id))
    secretUrls.set(att.id, pending)
  }
  return pending
}

function useFreshUrl(attachment: Attachment) {
  const secret = (attachment as Partial<SecretAttachment>).secret
  const [url, setUrl] = useState(secret ? '' : attachment.url)
  const retried = useRef(false)
  useEffect(() => {
    retried.current = false
    if (!secret) {
      setUrl(attachment.url)
      return
    }
    let alive = true
    secretUrl(attachment as SecretAttachment)
      .then((u) => alive && setUrl(u))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [attachment, secret])
  const onError = () => {
    if (secret || retried.current) return
    retried.current = true
    chatApi
      .fileUrl(attachment.id)
      .then((r) => setUrl(r.url))
      .catch(() => {})
  }
  return { url, onError }
}

export function MessageAttachments({ attachments }: { attachments: Attachment[] }) {
  const images = attachments.filter((a) => a.kind === 'image')
  const others = attachments.filter((a) => a.kind !== 'image')
  const [viewing, setViewing] = useState<Attachment | null>(null)

  return (
    <div className="attachments">
      {images.length > 0 && (
        <div className={`images images--${Math.min(images.length, 4)}`}>
          {images.map((a) => (
            <ImageThumb key={a.id} attachment={a} single={images.length === 1} onOpen={() => setViewing(a)} />
          ))}
        </div>
      )}
      {others.map((a) => (a.kind === 'voice' ? <VoicePlayer key={a.id} attachment={a} /> : <FileCard key={a.id} attachment={a} />))}
      {viewing && <Lightbox attachment={viewing} onClose={() => setViewing(null)} />}
    </div>
  )
}

function ImageThumb({ attachment, single, onOpen }: { attachment: Attachment; single: boolean; onOpen: () => void }) {
  const { url, onError } = useFreshUrl(attachment)
  const ratio = attachment.width && attachment.height ? attachment.width / attachment.height : 4 / 3
  return (
    <button
      type="button"
      className="image-thumb"
      style={single ? { aspectRatio: String(Math.min(Math.max(ratio, 0.5), 2.5)) } : undefined}
      onClick={onOpen}
      aria-label={`Открыть ${attachment.name}`}
    >
      {url && <img src={url} alt={attachment.name} loading="lazy" onError={onError} />}
    </button>
  )
}

function Lightbox({ attachment, onClose }: { attachment: Attachment; onClose: () => void }) {
  const { url, onError } = useFreshUrl(attachment)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="lightbox" role="dialog" aria-modal="true" aria-label="Просмотр фото" onClick={onClose}>
      {url && <img src={url} alt={attachment.name} onError={onError} onClick={(e) => e.stopPropagation()} />}
      <div className="lightbox__bar" onClick={(e) => e.stopPropagation()}>
        <span>{attachment.name}</span>
        <a href={url} download={attachment.name}>
          Скачать
        </a>
        <button type="button" onClick={onClose} aria-label="Закрыть просмотр">
          ×
        </button>
      </div>
    </div>
  )
}

function FileIcon() {
  return (
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
      <path d="M6 2h8l5 5v15H6V2Zm8 0v5h5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
    </svg>
  )
}

function FileCard({ attachment }: { attachment: Attachment }) {
  const { url } = useFreshUrl(attachment)
  return (
    <a className="file-card" href={url || undefined} download={attachment.name} data-testid="file-card">
      <span className="file-card__icon">
        <FileIcon />
      </span>
      <span className="file-card__text">
        <span className="file-card__name">{attachment.name}</span>
        <span className="file-card__size">{formatSize(attachment.size)}</span>
      </span>
    </a>
  )
}

function VoicePlayer({ attachment }: { attachment: Attachment }) {
  const { url, onError } = useFreshUrl(attachment)
  const audio = useRef<HTMLAudioElement>(null)
  const [playing, setPlaying] = useState(false)
  const [position, setPosition] = useState(0)
  const duration = (attachment.duration_ms ?? 0) / 1000

  const toggle = () => {
    const el = audio.current
    if (!el) return
    if (el.paused) void el.play()
    else el.pause()
  }
  const seek = (e: SyntheticEvent<HTMLInputElement>) => {
    const el = audio.current
    if (el) el.currentTime = Number(e.currentTarget.value)
  }

  return (
    <div className="voice" data-testid="voice">
      <button type="button" className="voice__play" onClick={toggle} aria-label={playing ? 'Пауза' : 'Слушать'}>
        {playing ? (
          <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
            <path d="M7 5h4v14H7zM13 5h4v14h-4z" fill="currentColor" />
          </svg>
        ) : (
          <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
            <path d="M8 5v14l11-7L8 5Z" fill="currentColor" />
          </svg>
        )}
      </button>
      <input
        type="range"
        className="voice__track"
        min={0}
        max={duration || 1}
        step={0.05}
        value={Math.min(position, duration || 1)}
        onChange={seek}
        aria-label="Позиция воспроизведения"
      />
      <span className="voice__time">{formatDuration((playing || position > 0 ? position : duration) * 1000)}</span>
      <audio
        ref={audio}
        src={url || undefined}
        preload="metadata"
        onError={onError}
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onEnded={() => {
          setPlaying(false)
          setPosition(0)
        }}
        onTimeUpdate={(e) => setPosition(e.currentTarget.currentTime)}
      />
    </div>
  )
}

export function PendingAttachments({ attachments }: { attachments: PendingAttachment[] }) {
  return (
    <div className="attachments">
      {attachments.map((a) => (
        <div key={a.localId} className={`pending-att pending-att--${a.kind}`}>
          {a.previewUrl ? (
            <img src={a.previewUrl} alt={a.name} />
          ) : (
            <span className="pending-att__name">
              {a.kind === 'voice' ? `🎤 ${formatDuration(a.durationMs ?? 0)}` : `📎 ${a.name}`}
            </span>
          )}
          {!a.uploaded && (
            <span className="pending-att__progress" role="progressbar" aria-valuenow={Math.round(a.progress * 100)} aria-valuemin={0} aria-valuemax={100}>
              <span style={{ width: `${Math.round(a.progress * 100)}%` }} />
            </span>
          )}
        </div>
      ))}
    </div>
  )
}
