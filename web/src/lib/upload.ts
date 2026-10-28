import { ApiError, NetworkError, getAccessToken, refreshSession } from './api'
import type { Attachment, AttachmentKind } from './types'

export type UploadOptions = {
  kind: AttachmentKind
  name: string
  durationMs?: number
  onProgress?: (fraction: number) => void
}

function once(chatId: string, blob: Blob, opts: UploadOptions): Promise<Attachment> {
  return new Promise((resolve, reject) => {
    const q = new URLSearchParams({ kind: opts.kind, name: opts.name })
    if (opts.durationMs !== undefined) q.set('duration_ms', String(Math.max(1, Math.round(opts.durationMs))))
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `/api/chats/${chatId}/attachments?${q}`)
    const token = getAccessToken()
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.setRequestHeader('Content-Type', blob.type || 'application/octet-stream')
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) opts.onProgress?.(e.loaded / e.total)
    }
    xhr.onerror = () => reject(new NetworkError('upload failed'))
    xhr.onload = () => {
      let body: unknown = null
      try {
        body = JSON.parse(xhr.responseText)
      } catch {
        body = null
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body as Attachment)
        return
      }
      const err = (body as { error?: { code?: string; message?: string; fields?: Record<string, string> } } | null)?.error
      reject(new ApiError(xhr.status, err?.code ?? 'unknown', err?.message ?? xhr.statusText, err?.fields))
    }
    xhr.send(blob)
  })
}

export async function uploadAttachment(chatId: string, blob: Blob, opts: UploadOptions): Promise<Attachment> {
  try {
    return await once(chatId, blob, opts)
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      await refreshSession()
      return once(chatId, blob, opts)
    }
    throw err
  }
}

const imageTypes = new Set(['image/jpeg', 'image/png', 'image/gif', 'image/webp'])

export function kindOf(file: File): AttachmentKind {
  return imageTypes.has(file.type) ? 'image' : 'file'
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} Б`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} КБ`
  return `${(bytes / 1024 / 1024).toFixed(1)} МБ`
}

export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}
