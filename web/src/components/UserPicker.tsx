import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Avatar } from './Avatar'
import { chatApi } from '../lib/chat-api'
import { useDebounced } from '../lib/hooks'
import type { PublicUser } from '../lib/types'
import { normalizeUsername } from '../lib/validation'

const searchRe = /^[a-z0-9_]{1,32}$/

type UserPickerProps = {
  selected: PublicUser[]
  onChange: (users: PublicUser[]) => void
  exclude?: Set<string>
}

export function UserPicker({ selected, onChange, exclude }: UserPickerProps) {
  const [query, setQuery] = useState('')
  const q = normalizeUsername(useDebounced(query, 200))
  const results = useQuery({
    queryKey: ['user-search', q],
    queryFn: () => chatApi.searchUsers(q),
    enabled: searchRe.test(q),
    staleTime: 10_000,
  })
  const chosen = new Set(selected.map((u) => u.id))
  const visible = (results.data ?? []).filter((u) => !exclude?.has(u.id))

  const toggle = (u: PublicUser) => {
    onChange(chosen.has(u.id) ? selected.filter((s) => s.id !== u.id) : [...selected, u])
  }

  return (
    <div className="picker">
      {selected.length > 0 && (
        <ul className="chips" aria-label="Выбранные участники">
          {selected.map((u) => (
            <li key={u.id} className="chip">
              <Avatar id={u.id} name={u.display_name} size={22} />
              <span>{u.display_name}</span>
              <button type="button" onClick={() => toggle(u)} aria-label={`Убрать ${u.display_name}`}>
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <input
        type="search"
        className="search-input"
        placeholder="Найти по @username"
        aria-label="Найти участника"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        autoCapitalize="none"
        spellCheck={false}
      />
      {searchRe.test(q) && (
        <ul className="list picker__results" aria-label="Кого добавить">
          {results.isPending && <li className="sidebar__empty">Ищем…</li>}
          {results.isSuccess && visible.length === 0 && <li className="sidebar__empty">Никого не нашли</li>}
          {visible.map((u) => (
            <li key={u.id}>
              <button type="button" className="row" onClick={() => toggle(u)} aria-pressed={chosen.has(u.id)}>
                <Avatar id={u.id} name={u.display_name} online={u.online} />
                <span className="row__main">
                  <span className="row__title">{u.display_name}</span>
                  <span className="row__sub">@{u.username}</span>
                </span>
                <span className={`check${chosen.has(u.id) ? ' check--on' : ''}`} aria-hidden="true" />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
