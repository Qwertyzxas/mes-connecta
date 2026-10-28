import { create } from 'zustand'
import { api, json } from './api'

export type PushState = 'unsupported' | 'denied' | 'off' | 'on' | 'busy'

export const usePush = create<{ state: PushState; error: string | null }>()(() => ({ state: 'off', error: null }))

export function pushSupported(): boolean {
  return typeof window !== 'undefined' && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

function keyToBytes(base64: string): Uint8Array<ArrayBuffer> {
  const padded = base64.replace(/-/g, '+').replace(/_/g, '/').padEnd(Math.ceil(base64.length / 4) * 4, '=')
  const raw = atob(padded)
  const bytes = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i)
  return bytes
}

async function registration(): Promise<ServiceWorkerRegistration | null> {
  if (!pushSupported()) return null
  const existing = await navigator.serviceWorker.getRegistration()
  if (!existing) return null
  return navigator.serviceWorker.ready
}

export async function refreshPushState() {
  if (!pushSupported()) return usePush.setState({ state: 'unsupported' })
  if (Notification.permission === 'denied') return usePush.setState({ state: 'denied' })
  const reg = await registration()
  const sub = await reg?.pushManager.getSubscription()
  usePush.setState({ state: sub ? 'on' : 'off' })
  if (sub) await sendSubscription(sub).catch(() => {})
}

async function sendSubscription(sub: PushSubscription) {
  const data = sub.toJSON()
  await api<void>('/push/subscriptions', json({ endpoint: data.endpoint, keys: data.keys }))
}

export async function enablePush() {
  if (!pushSupported()) return usePush.setState({ state: 'unsupported' })
  usePush.setState({ state: 'busy', error: null })
  try {
    const permission = await Notification.requestPermission()
    if (permission !== 'granted') {
      usePush.setState({ state: permission === 'denied' ? 'denied' : 'off' })
      return
    }
    const reg = await registration()
    if (!reg) throw new Error('Приложение ещё не установилось. Обновите страницу и попробуйте снова')
    const { public_key } = await api<{ public_key: string }>('/push/key')
    const sub =
      (await reg.pushManager.getSubscription()) ??
      (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyToBytes(public_key) }))
    await sendSubscription(sub)
    usePush.setState({ state: 'on' })
  } catch (err) {
    usePush.setState({ state: 'off', error: err instanceof Error ? err.message : 'Не удалось включить уведомления' })
  }
}

export async function disablePush() {
  usePush.setState({ state: 'busy', error: null })
  try {
    const reg = await registration()
    const sub = await reg?.pushManager.getSubscription()
    if (sub) {
      await api<void>('/push/subscriptions', { method: 'DELETE', body: JSON.stringify({ endpoint: sub.endpoint }) })
      await sub.unsubscribe()
    }
  } finally {
    usePush.setState({ state: 'off' })
  }
}

export async function showLocalNotification(title: string, body: string, url: string, tag: string) {
  if (!pushSupported() || Notification.permission !== 'granted') return
  const reg = await registration()
  await reg?.showNotification(title, { body, tag, icon: '/icon-192.png', data: { url } })
}
