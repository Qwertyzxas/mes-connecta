import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Avatar } from '../components/Avatar'
import { chatApi } from '../lib/chat-api'
import { chatsKey, upsertChat } from '../lib/chat-cache'
import { describeError } from '../lib/errors'
import { lastSeenText } from '../lib/format'
import { useDebounced } from '../lib/hooks'
import type { PublicUser } from '../lib/types'
import { normalizeUsername } from '../lib/validation'

export function ContactsPanel() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const term = normalizeUsername(useDebounced(query, 250))
  const chats = useQuery({ queryKey: chatsKey, queryFn: chatApi.list })
  const found = useQuery({
    queryKey: ['users', term],
    queryFn: () => chatApi.searchUsers(term),
    enabled: term.length > 0,
  })

  const contacts = useMemo(() => {
    const byId = new Map<string, PublicUser>()
    for (const chat of chats.data ?? []) {
      if (chat.kind === 'direct' && chat.peer) byId.set(chat.peer.id, chat.peer)
    }
    return [...byId.values()].sort((a, b) => a.display_name.localeCompare(b.display_name, 'ru'))
  }, [chats.data])

  const open = useMutation({
    mutationFn: (user: PublicUser) => chatApi.openDirect(user.id),
    onSuccess: (chat) => {
      upsertChat(qc, chat)
      void navigate(`/chat/${chat.id}`)
    },
  })

  const filtered = query.trim()
    ? contacts.filter((c) =>
        `${c.display_name} ${c.username}`.toLowerCase().includes(query.trim().toLowerCase().replace(/^@/, '')),
      )
    : contacts
  const others = (found.data ?? []).filter((u) => !contacts.some((c) => c.id === u.id))

  const row = (user: PublicUser) => (
    <li key={user.id}>
      <button type="button" className="row" onClick={() => open.mutate(user)} disabled={open.isPending}>
        <Avatar id={user.id} name={user.display_name} online={user.online} />
        <span className="row__main">
          <span className="row__line">
            <span className="row__title">{user.display_name}</span>
          </span>
          <span className="row__sub">
            @{user.username} · {lastSeenText(user.online, user.last_seen_at)}
          </span>
        </span>
      </button>
    </li>
  )

  return (
    <div className="panel-view">
      <header className="panel-view__head">
        <h2 className="panel-view__title">Контакты</h2>
      </header>
      <div className="sidebar__search">
        <input
          type="search"
          className="search-input"
          placeholder="Имя или @username"
          aria-label="Поиск контактов"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
        />
      </div>
      {open.isError && <p className="alert">{describeError(open.error).message}</p>}
      {filtered.length > 0 && (
        <ul className="list" aria-label="Контакты">
          {filtered.map(row)}
        </ul>
      )}
      {others.length > 0 && (
        <>
          <p className="list-caption">Другие люди</p>
          <ul className="list" aria-label="Найденные люди">
            {others.map(row)}
          </ul>
        </>
      )}
      {chats.isSuccess && contacts.length === 0 && !query.trim() && (
        <div className="sidebar__empty sidebar__empty--intro">
          <p>Здесь появятся люди, с которыми вы переписываетесь.</p>
          <p>Найдите собеседника по имени или @username.</p>
        </div>
      )}
      {query.trim() && filtered.length === 0 && found.isSuccess && others.length === 0 && (
        <p className="sidebar__empty">Никого не нашли</p>
      )}
    </div>
  )
}
