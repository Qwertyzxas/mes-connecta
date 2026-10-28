import type { AuthResponse } from './types'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: Record<string, string>

  constructor(status: number, code: string, message: string, fields: Record<string, string> = {}) {
    super(message)
    this.status = status
    this.code = code
    this.fields = fields
  }
}

export class NetworkError extends Error {}

let accessToken: string | null = null
let refreshing: Promise<AuthResponse> | null = null
let onUnauthorized: () => void = () => {}

export function getAccessToken(): string | null {
  return accessToken
}

export function setAccessToken(token: string | null) {
  accessToken = token
}

export function setUnauthorizedHandler(handler: () => void) {
  onUnauthorized = handler
}

async function send(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers)
  if (init.body !== undefined && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (accessToken) {
    headers.set('Authorization', `Bearer ${accessToken}`)
  }
  try {
    return await fetch(`/api${path}`, { ...init, headers, credentials: 'same-origin' })
  } catch {
    throw new NetworkError('network unavailable')
  }
}

async function parse<T>(res: Response): Promise<T> {
  if (res.status === 204) {
    return undefined as T
  }
  const body: unknown = await res.json().catch(() => null)
  if (!res.ok) {
    const err = (body as { error?: { code?: string; message?: string; fields?: Record<string, string> } } | null)?.error
    throw new ApiError(res.status, err?.code ?? 'unknown', err?.message ?? res.statusText, err?.fields)
  }
  return body as T
}

function withRefreshLock<T>(fn: () => Promise<T>): Promise<T> {
  if (typeof navigator !== 'undefined' && 'locks' in navigator) {
    return navigator.locks.request('connecta:refresh', fn)
  }
  return fn()
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

export function refreshSession(): Promise<AuthResponse> {
  refreshing ??= withRefreshLock(async () => {
    for (let attempt = 0; ; attempt++) {
      const res = await send('/auth/refresh', { method: 'POST' })
      if (res.status === 409 && attempt < 3) {
        await sleep(250 * (attempt + 1))
        continue
      }
      const data = await parse<AuthResponse>(res)
      accessToken = data.access_token
      return data
    }
  }).finally(() => {
    refreshing = null
  })
  return refreshing
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await send(path, init)
  if (res.status !== 401 || path.startsWith('/auth/')) {
    return parse<T>(res)
  }

  try {
    await refreshSession()
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      accessToken = null
      onUnauthorized()
    }
    throw err
  }

  const retried = await send(path, init)
  if (retried.status === 401) {
    accessToken = null
    onUnauthorized()
  }
  return parse<T>(retried)
}

export const json = (body: unknown): RequestInit => ({ method: 'POST', body: JSON.stringify(body) })
