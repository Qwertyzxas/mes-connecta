import { useCallback, useEffect, useRef, useState } from 'react'

const candidates = ['audio/webm;codecs=opus', 'audio/ogg;codecs=opus', 'audio/mp4', 'audio/webm']
export const MAX_VOICE_MS = 15 * 60 * 1000

export type Recording = { blob: Blob; durationMs: number; name: string }

function pickType(): string {
  if (typeof MediaRecorder === 'undefined') return ''
  return candidates.find((t) => MediaRecorder.isTypeSupported(t)) ?? ''
}

function extensionFor(type: string): string {
  if (type.includes('ogg')) return 'ogg'
  if (type.includes('mp4')) return 'm4a'
  return 'webm'
}

export function useRecorder() {
  const [state, setState] = useState<'idle' | 'starting' | 'recording'>('idle')
  const [elapsed, setElapsed] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const recorder = useRef<MediaRecorder | null>(null)
  const chunks = useRef<Blob[]>([])
  const startedAt = useRef(0)
  const timer = useRef<ReturnType<typeof setInterval> | null>(null)

  const cleanup = useCallback(() => {
    if (timer.current) clearInterval(timer.current)
    timer.current = null
    recorder.current?.stream.getTracks().forEach((t) => t.stop())
    recorder.current = null
    chunks.current = []
    setElapsed(0)
    setState('idle')
  }, [])

  useEffect(() => cleanup, [cleanup])

  const start = useCallback(async () => {
    setError(null)
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === 'undefined') {
      setError('Запись голоса не поддерживается в этом браузере')
      return
    }
    setState('starting')
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      const type = pickType()
      const rec = new MediaRecorder(stream, type ? { mimeType: type } : undefined)
      chunks.current = []
      rec.ondataavailable = (e) => e.data.size > 0 && chunks.current.push(e.data)
      rec.start(250)
      recorder.current = rec
      startedAt.current = performance.now()
      timer.current = setInterval(() => setElapsed(performance.now() - startedAt.current), 200)
      setState('recording')
    } catch {
      setError('Нет доступа к микрофону')
      cleanup()
    }
  }, [cleanup])

  const stop = useCallback(
    () =>
      new Promise<Recording | null>((resolve) => {
        const rec = recorder.current
        if (!rec) return resolve(null)
        const durationMs = Math.min(performance.now() - startedAt.current, MAX_VOICE_MS)
        rec.onstop = () => {
          const type = rec.mimeType || 'audio/webm'
          const blob = new Blob(chunks.current, { type })
          cleanup()
          resolve(blob.size > 0 ? { blob, durationMs, name: `voice.${extensionFor(type)}` } : null)
        }
        rec.stop()
      }),
    [cleanup],
  )

  const cancel = useCallback(() => {
    const rec = recorder.current
    if (rec) {
      rec.onstop = null
      if (rec.state !== 'inactive') rec.stop()
    }
    cleanup()
  }, [cleanup])

  return { state, elapsed, error, start, stop, cancel, clearError: () => setError(null) }
}
