import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Avatar } from './Avatar'
import { Button } from './Button'
import { Field } from './Field'
import { UserPicker } from './UserPicker'
import { chatApi } from '../lib/chat-api'
import { chatKey, chatsKey } from '../lib/chat-cache'
import { canManage, chatAvatarId, chatTitle, membersText, roleLabel } from '../lib/chat-display'
import { fingerprint, forgetChatKeys } from '../lib/e2e'
import { describeError } from '../lib/errors'
import { lastSeenText } from '../lib/format'
import type { Chat, Member, PublicUser } from '../lib/types'
import { useAuth } from '../store/auth'
import { useMessages } from '../store/messages'

const headings: Record<Chat['kind'], string> = {
  direct: 'Профиль',
  group: 'Группа',
  channel: 'Канал',
  secret: 'Секретный чат',
}

function KeySection({ chat }: { chat: Chat }) {
  const [code, setCode] = useState<string | null>(null)
  useEffect(() => {
    let alive = true
    void fingerprint(chat).then((c) => alive && setCode(c))
    return () => {
      alive = false
    }
  }, [chat])
  return (
    <section className="info__section" aria-label="Ключ шифрования">
      <h4>Ключ шифрования</h4>
      {code ? (
        <>
          <code className="key-code" data-testid="key-fingerprint">
            {code}
          </code>
          <p className="muted">Сравните код с собеседником. Если коды совпадают, переписку никто не подменяет.</p>
        </>
      ) : (
        <p className="muted">Код появится, когда собеседник примет приглашение.</p>
      )}
    </section>
  )
}

export function ChatInfo({ chat, onClose }: { chat: Chat; onClose: () => void }) {
  const title = chatTitle(chat)
  return (
    <aside className="info" aria-label="Информация о чате">
      <header className="info__head">
        <button type="button" className="icon-btn" onClick={onClose} aria-label="Закрыть информацию">
          <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
            <path d="M6 6l12 12M18 6 6 18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </svg>
        </button>
        <h2>{headings[chat.kind]}</h2>
      </header>
      <div className="info__body">
        <div className="info__hero">
          <Avatar id={chatAvatarId(chat)} name={title} size={88} online={chat.peer?.online} />
          <h3 className="info__title">{title}</h3>
          <p className="info__sub">
            {chat.peer
              ? `@${chat.peer.username} · ${lastSeenText(chat.peer.online, chat.peer.last_seen_at)}`
              : chat.handle
                ? `@${chat.handle} · ${membersText(chat)}`
                : membersText(chat)}
          </p>
        </div>
        <MuteSection chat={chat} />
        {chat.kind === 'secret' && <KeySection chat={chat} />}
        {(chat.kind === 'group' || chat.kind === 'channel') && <AboutSection chat={chat} />}
        {(chat.kind === 'group' || chat.kind === 'channel') && <MembersSection chat={chat} />}
        {chat.kind !== 'direct' && <LeaveSection chat={chat} />}
      </div>
    </aside>
  )
}

function MuteSection({ chat }: { chat: Chat }) {
  const qc = useQueryClient()
  const toggle = useMutation({
    mutationFn: (muted: boolean) => chatApi.mute(chat.id, muted),
    onMutate: (muted) => {
      const patch = (c: Chat) => (c.id === chat.id ? { ...c, muted } : c)
      qc.setQueryData<Chat>(chatKey(chat.id), (c) => (c ? patch(c) : c))
      qc.setQueryData<Chat[]>(chatsKey, (list) => list?.map(patch))
    },
    onError: () => {
      void qc.invalidateQueries({ queryKey: chatKey(chat.id) })
      void qc.invalidateQueries({ queryKey: chatsKey })
    },
  })
  const [checked, setChecked] = useState(chat.muted)
  useEffect(() => setChecked(chat.muted), [chat.muted])
  return (
    <section className="info__section">
      <label className="switch">
        <input
          type="checkbox"
          checked={checked}
          onChange={(e) => {
            setChecked(e.target.checked)
            toggle.mutate(e.target.checked)
          }}
        />
        <span>Без уведомлений</span>
      </label>
    </section>
  )
}

function AboutSection({ chat }: { chat: Chat }) {
  const qc = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(chat.title)
  const [about, setAbout] = useState(chat.about)
  const save = useMutation({
    mutationFn: () => chatApi.update(chat.id, { title: title.trim(), about: about.trim() }),
    onSuccess: (updated) => {
      qc.setQueryData(chatKey(chat.id), updated)
      void qc.invalidateQueries({ queryKey: chatsKey })
      setEditing(false)
    },
  })

  if (editing) {
    const submit = (e: FormEvent) => {
      e.preventDefault()
      save.mutate()
    }
    return (
      <form className="info__section form form--tight" onSubmit={submit} aria-label="Изменение чата">
        {save.isError && <div className="alert">{describeError(save.error).message}</div>}
        <Field label="Название" value={title} onChange={(e) => setTitle(e.target.value)} error={describeError(save.error).fields.title} />
        <Field label="Описание" value={about} onChange={(e) => setAbout(e.target.value)} />
        <div className="info__row">
          <Button type="submit" loading={save.isPending}>
            Сохранить
          </Button>
          <Button type="button" variant="ghost" onClick={() => setEditing(false)}>
            Отмена
          </Button>
        </div>
      </form>
    )
  }

  return (
    <section className="info__section">
      {chat.about && <p className="info__about">{chat.about}</p>}
      {canManage(chat) && (
        <Button
          variant="ghost"
          onClick={() => {
            setTitle(chat.title)
            setAbout(chat.about)
            setEditing(true)
          }}
        >
          Изменить название и описание
        </Button>
      )}
    </section>
  )
}

function MembersSection({ chat }: { chat: Chat }) {
  const me = useAuth((s) => s.user?.id ?? '')
  const qc = useQueryClient()
  const members = useQuery({ queryKey: ['members', chat.id], queryFn: () => chatApi.members(chat.id) })
  const [adding, setAdding] = useState(false)
  const [picked, setPicked] = useState<PublicUser[]>([])
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['members', chat.id] })
    void qc.invalidateQueries({ queryKey: chatKey(chat.id) })
  }
  const add = useMutation({
    mutationFn: () => chatApi.addMembers(chat.id, picked.map((u) => u.id)),
    onSuccess: (list) => {
      qc.setQueryData(['members', chat.id], list)
      void qc.invalidateQueries({ queryKey: chatKey(chat.id) })
      setPicked([])
      setAdding(false)
    },
  })
  const manage = canManage(chat)
  const existing = new Set((members.data ?? []).map((m) => m.id))

  return (
    <section className="info__section" aria-label="Участники">
      <div className="info__section-head">
        <h4>{chat.kind === 'channel' ? 'Подписчики' : 'Участники'}</h4>
        {manage && !adding && (
          <button type="button" className="link-btn" onClick={() => setAdding(true)}>
            Добавить
          </button>
        )}
      </div>
      {adding && (
        <div className="info__add">
          <UserPicker selected={picked} onChange={setPicked} exclude={existing} />
          {add.isError && <p className="alert">{describeError(add.error).message}</p>}
          <div className="info__row">
            <Button onClick={() => add.mutate()} disabled={picked.length === 0} loading={add.isPending}>
              Добавить {picked.length > 0 ? picked.length : ''}
            </Button>
            <Button variant="ghost" onClick={() => setAdding(false)}>
              Отмена
            </Button>
          </div>
        </div>
      )}
      {members.isPending && <p className="muted">Загружаем…</p>}
      <ul className="members">
        {members.data?.map((m) => (
          <MemberRow key={m.id} chat={chat} member={m} me={me} onChanged={refresh} />
        ))}
      </ul>
    </section>
  )
}

function MemberRow({ chat, member, me, onChanged }: { chat: Chat; member: Member; me: string; onChanged: () => void }) {
  const [error, setError] = useState<string | null>(null)
  const act = useMutation({
    mutationFn: (action: 'promote' | 'demote' | 'remove') =>
      action === 'remove'
        ? chatApi.removeMember(chat.id, member.id)
        : chatApi.setRole(chat.id, member.id, action === 'promote' ? 'admin' : 'member'),
    onSuccess: onChanged,
    onError: (err) => setError(describeError(err).message),
  })
  const isOwner = chat.role === 'owner'
  const canRemove =
    member.id !== me && member.role !== 'owner' && (isOwner || (chat.role === 'admin' && member.role === 'member'))
  const canPromote = isOwner && member.id !== me && member.role !== 'owner'

  return (
    <li className="member" data-testid="member">
      <Avatar id={member.id} name={member.display_name} size={40} online={member.online} />
      <span className="row__main">
        <span className="row__title">
          {member.display_name}
          {member.id === me && <span className="muted"> (вы)</span>}
        </span>
        <span className="row__sub">{member.online ? 'в сети' : `@${member.username}`}</span>
        {error && <span className="field__error">{error}</span>}
      </span>
      {roleLabel[member.role] && <span className="role-badge">{roleLabel[member.role]}</span>}
      {(canPromote || canRemove) && (
        <span className="member__actions">
          {canPromote && (
            <button
              type="button"
              className="link-btn"
              disabled={act.isPending}
              onClick={() => act.mutate(member.role === 'admin' ? 'demote' : 'promote')}
            >
              {member.role === 'admin' ? 'Снять админа' : 'Сделать админом'}
            </button>
          )}
          {canRemove && (
            <button
              type="button"
              className="link-btn link-btn--danger"
              disabled={act.isPending}
              onClick={() => act.mutate('remove')}
              aria-label={`Удалить ${member.display_name}`}
            >
              Удалить
            </button>
          )}
        </span>
      )}
    </li>
  )
}

function LeaveSection({ chat }: { chat: Chat }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const forget = useMessages((s) => s.forget)
  const [confirming, setConfirming] = useState(false)
  const leave = useMutation({
    mutationFn: () => chatApi.leave(chat.id),
    onSuccess: () => {
      qc.setQueryData<Chat[]>(chatsKey, (list) => list?.filter((c) => c.id !== chat.id))
      qc.removeQueries({ queryKey: chatKey(chat.id) })
      forget(chat.id)
      if (chat.kind === 'secret') void forgetChatKeys(chat.id)
      void navigate('/')
    },
  })
  const label = chat.kind === 'group' ? 'Выйти из группы' : chat.kind === 'secret' ? 'Удалить секретный чат' : 'Покинуть канал'

  return (
    <section className="info__section">
      {leave.isError && <p className="alert">{describeError(leave.error).message}</p>}
      {confirming ? (
        <div className="info__row">
          <Button variant="danger" onClick={() => leave.mutate()} loading={leave.isPending}>
            {label}
          </Button>
          <Button variant="ghost" onClick={() => setConfirming(false)}>
            Отмена
          </Button>
        </div>
      ) : (
        <Button variant="danger" onClick={() => setConfirming(true)}>
          {label}
        </Button>
      )}
    </section>
  )
}
