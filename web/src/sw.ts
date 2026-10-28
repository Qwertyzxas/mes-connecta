import { cleanupOutdatedCaches, createHandlerBoundToURL, precacheAndRoute, type PrecacheEntry } from 'workbox-precaching'
import { NavigationRoute, registerRoute } from 'workbox-routing'

declare const self: ServiceWorkerGlobalScope & { __WB_MANIFEST: Array<PrecacheEntry | string> }

type PushPayload = {
  type: 'message' | 'call'
  chat_id: string
  title: string
  body: string
  url: string
  tag: string
}

precacheAndRoute(self.__WB_MANIFEST)
cleanupOutdatedCaches()
registerRoute(new NavigationRoute(createHandlerBoundToURL('/index.html'), { denylist: [/^\/api\//] }))

self.addEventListener('install', () => {
  void self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})

self.addEventListener('push', (event) => {
  let data: PushPayload | null = null
  try {
    data = event.data?.json() as PushPayload
  } catch {
    data = null
  }
  if (!data) return

  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      const viewing = windows.some((w) => w.focused && new URL(w.url).pathname === data.url)
      if (viewing) return
      await self.registration.showNotification(data.title, {
        body: data.body,
        tag: data.tag,
        icon: '/icon-192.png',
        badge: '/icon-192.png',
        data: { url: data.url },
        requireInteraction: data.type === 'call',
      })
    })(),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const url = (event.notification.data as { url?: string } | null)?.url ?? '/'
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      const existing = windows.find((w) => new URL(w.url).origin === self.location.origin)
      if (existing) {
        await existing.focus()
        existing.postMessage({ type: 'navigate', url })
        return
      }
      await self.clients.openWindow(url)
    })(),
  )
})
