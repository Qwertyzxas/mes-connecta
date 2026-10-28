import { useEffect } from 'react'
import { Outlet, useParams } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { Sidebar } from '../components/Sidebar'
import { handleServerEvent, openEvent } from '../lib/chat-cache'
import { setSecretUser } from '../lib/e2e'
import { refreshPushState } from '../lib/push'
import { realtime } from '../lib/realtime'
import { useAuth } from '../store/auth'

export function ChatsLayout() {
  const { chatId } = useParams()
  const me = useAuth((s) => s.user?.id)
  const qc = useQueryClient()

  useEffect(() => {
    if (!me) return
    setSecretUser(me)
    let queue = Promise.resolve()
    const unsubscribe = realtime.subscribe((event) => {
      queue = queue
        .then(() => openEvent(event))
        .then((opened) => handleServerEvent(qc, me, opened))
        .catch(() => {})
    })
    realtime.start()
    void refreshPushState()
    return unsubscribe
  }, [me, qc])

  return (
    <div className={`chats${chatId ? ' chats--open' : ''}`}>
      <Sidebar />
      <main className="chats__main">
        <Outlet />
      </main>
    </div>
  )
}

export function NoChatSelected() {
  return (
    <section className="conversation conversation--empty">
      <img src="/logo-mark.png" alt="" width={88} height={88} className="conversation__mark" />
      <p>Выберите чат или найдите собеседника по @username</p>
    </section>
  )
}
