import { useEffect } from 'react'
import { NavLink, Outlet, useLocation, useParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Sidebar } from '../components/Sidebar'
import { chatApi } from '../lib/chat-api'
import { chatsKey, handleServerEvent, openEvent } from '../lib/chat-cache'
import { setSecretUser } from '../lib/e2e'
import { refreshPushState } from '../lib/push'
import { realtime } from '../lib/realtime'
import { useAuth } from '../store/auth'
import { CallsPanel } from './CallsPanel'
import { ContactsPanel } from './ContactsPanel'
import { SettingsPage } from './SettingsPage'

type Section = 'chats' | 'calls' | 'contacts' | 'settings'

function sectionOf(pathname: string): Section {
  if (pathname.startsWith('/calls')) return 'calls'
  if (pathname.startsWith('/contacts')) return 'contacts'
  if (pathname.startsWith('/settings')) return 'settings'
  return 'chats'
}

function TabIcon({ section }: { section: Section }) {
  const common = { fill: 'none', stroke: 'currentColor', strokeWidth: 1.8, strokeLinecap: 'round', strokeLinejoin: 'round' } as const
  return (
    <svg viewBox="0 0 24 24" width="24" height="24" aria-hidden="true">
      {section === 'chats' && <path d="M4 5.5h16v10H9l-5 4v-14Z" {...common} />}
      {section === 'calls' && (
        <path
          d="M6.6 3.5h2.6l1.4 4.2-2 1.6a12 12 0 0 0 6.1 6.1l1.6-2 4.2 1.4v2.6a2 2 0 0 1-2.2 2A16.6 16.6 0 0 1 4.6 5.7a2 2 0 0 1 2-2.2Z"
          {...common}
        />
      )}
      {section === 'contacts' && (
        <>
          <circle cx="12" cy="8.5" r="3.5" {...common} />
          <path d="M5 20c0-3.6 3.1-6 7-6s7 2.4 7 6" {...common} />
        </>
      )}
      {section === 'settings' && (
        <>
          <circle cx="12" cy="12" r="3" {...common} />
          <path
            d="M12 2.8v2.4M12 18.8v2.4M2.8 12h2.4M18.8 12h2.4M5.5 5.5l1.7 1.7M16.8 16.8l1.7 1.7M5.5 18.5l1.7-1.7M16.8 7.2l1.7-1.7"
            {...common}
          />
        </>
      )}
    </svg>
  )
}

const tabs: { section: Section; to: string; label: string }[] = [
  { section: 'chats', to: '/', label: 'Чаты' },
  { section: 'calls', to: '/calls', label: 'Звонки' },
  { section: 'contacts', to: '/contacts', label: 'Контакты' },
  { section: 'settings', to: '/settings', label: 'Настройки' },
]

function TabBar({ active }: { active: Section }) {
  const chats = useQuery({ queryKey: chatsKey, queryFn: chatApi.list })
  const unread = (chats.data ?? []).filter((c) => !c.muted && c.unread > 0).length
  return (
    <nav className="tabbar" aria-label="Разделы">
      {tabs.map((tab) => (
        <NavLink
          key={tab.section}
          to={tab.to}
          className={`tabbar__item${active === tab.section ? ' tabbar__item--active' : ''}`}
          aria-current={active === tab.section ? 'page' : undefined}
        >
          <span className="tabbar__icon">
            <TabIcon section={tab.section} />
            {tab.section === 'chats' && unread > 0 && (
              <span className="tabbar__badge" aria-label={`Чатов с новыми сообщениями: ${unread}`}>
                {unread > 99 ? '99+' : unread}
              </span>
            )}
          </span>
          <span className="tabbar__label">{tab.label}</span>
        </NavLink>
      ))}
    </nav>
  )
}

export function ChatsLayout() {
  const { chatId } = useParams()
  const { pathname } = useLocation()
  const section = sectionOf(pathname)
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
    <div className={`chats${chatId ? ' chats--open' : ''} chats--${section}`}>
      <aside className="sidebar">
        <div className="sidebar__panel">
          {section === 'chats' && <Sidebar />}
          {section === 'calls' && <CallsPanel />}
          {section === 'contacts' && <ContactsPanel />}
          {section === 'settings' && <SettingsPage />}
        </div>
        <TabBar active={section} />
      </aside>
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
