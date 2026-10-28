import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import { Avatar } from '../components/Avatar'
import { Button } from '../components/Button'
import { authApi } from '../lib/auth-api'
import { describeError } from '../lib/errors'
import { describeDevice, timeAgo } from '../lib/format'
import type { Session } from '../lib/types'
import { useAuth } from '../store/auth'

export function SettingsPage() {
  const user = useAuth((s) => s.user)
  const signOut = useAuth((s) => s.signOut)
  const queryClient = useQueryClient()

  const sessions = useQuery({ queryKey: ['sessions'], queryFn: authApi.sessions })

  const revoke = useMutation({
    mutationFn: authApi.revokeSession,
    onSuccess: (_, id) => {
      queryClient.setQueryData<Session[]>(['sessions'], (list) => list?.filter((s) => s.id !== id))
    },
  })

  const logout = useMutation({ mutationFn: signOut })

  if (!user) return null

  return (
    <div className="home">
      <header className="home__bar">
        <Link to="/" className="back-link">
          ← К чатам
        </Link>
        <Button variant="ghost" onClick={() => logout.mutate()} loading={logout.isPending}>
          Выйти
        </Button>
      </header>

      <main className="home__content">
        <section className="profile">
          <Avatar id={user.id} name={user.display_name} size={56} />
          <div>
            <h1 className="profile__name">{user.display_name}</h1>
            <p className="profile__meta">
              @{user.username} · {user.email}
            </p>
          </div>
        </section>

        <section className="panel">
          <div className="panel__head">
            <h2>Активные сеансы</h2>
            <span className="panel__count">{sessions.data?.length ?? ''}</span>
          </div>

          {sessions.isPending && <p className="muted">Загружаем…</p>}
          {sessions.isError && <p className="alert">{describeError(sessions.error).message}</p>}
          {revoke.isError && <p className="alert">{describeError(revoke.error).message}</p>}

          <ul className="sessions">
            {sessions.data?.map((s) => (
              <li key={s.id} className="session">
                <div>
                  <p className="session__device">
                    {describeDevice(s.user_agent)}
                    {s.current && <span className="badge">Это устройство</span>}
                  </p>
                  <p className="session__meta">
                    {s.ip || 'IP неизвестен'} · {s.current ? 'сейчас' : timeAgo(s.last_used_at)}
                  </p>
                </div>
                {!s.current && (
                  <Button
                    variant="danger"
                    onClick={() => revoke.mutate(s.id)}
                    loading={revoke.isPending && revoke.variables === s.id}
                  >
                    Завершить
                  </Button>
                )}
              </li>
            ))}
          </ul>
        </section>

      </main>
    </div>
  )
}
