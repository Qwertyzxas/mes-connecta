import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from './Button'
import { Field } from './Field'
import { Modal } from './Modal'
import { UserPicker } from './UserPicker'
import { chatApi } from '../lib/chat-api'
import { generateKeyPair, storeKeyPair } from '../lib/e2e'
import { upsertChat } from '../lib/chat-cache'
import { describeError } from '../lib/errors'
import type { Chat, PublicUser } from '../lib/types'
import { normalizeUsername } from '../lib/validation'

const handleRe = /^[a-z][a-z0-9_]{4,31}$/

function useOpenCreated(onClose: () => void) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  return (chat: Chat) => {
    upsertChat(qc, chat)
    onClose()
    void navigate(`/chat/${chat.id}`)
  }
}

export function NewGroupDialog({ onClose }: { onClose: () => void }) {
  const [title, setTitle] = useState('')
  const [members, setMembers] = useState<PublicUser[]>([])
  const [titleError, setTitleError] = useState<string>()
  const opened = useOpenCreated(onClose)
  const create = useMutation({
    mutationFn: () => chatApi.createGroup(title.trim(), members.map((m) => m.id)),
    onSuccess: opened,
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const t = title.trim()
    if (!t) return setTitleError('Введите название')
    if ([...t].length > 64) return setTitleError('Не длиннее 64 символов')
    setTitleError(undefined)
    create.mutate()
  }

  return (
    <Modal
      title="Новая группа"
      onClose={onClose}
      footer={
        <Button type="submit" form="new-group" loading={create.isPending}>
          Создать группу
        </Button>
      }
    >
      <form id="new-group" className="form form--tight" onSubmit={submit} noValidate>
        {create.isError && <div className="alert">{describeError(create.error).message}</div>}
        <Field label="Название группы" value={title} onChange={(e) => setTitle(e.target.value)} error={titleError} />
        <UserPicker selected={members} onChange={setMembers} />
      </form>
    </Modal>
  )
}

export function NewChannelDialog({ onClose }: { onClose: () => void }) {
  const [title, setTitle] = useState('')
  const [about, setAbout] = useState('')
  const [isPublic, setIsPublic] = useState(true)
  const [handle, setHandle] = useState('')
  const [errors, setErrors] = useState<Record<string, string>>({})
  const opened = useOpenCreated(onClose)
  const create = useMutation({
    mutationFn: () =>
      chatApi.createChannel({
        title: title.trim(),
        about: about.trim(),
        is_public: isPublic,
        handle: isPublic ? normalizeUsername(handle) : '',
      }),
    onSuccess: opened,
    onError: (err) => {
      const { fields } = describeError(err)
      if (Object.keys(fields).length) setErrors(fields)
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const next: Record<string, string> = {}
    if (!title.trim()) next.title = 'Введите название'
    if (isPublic && !handleRe.test(normalizeUsername(handle))) {
      next.handle = '5–32 символа: латиница, цифры и _, начинается с буквы'
    }
    setErrors(next)
    if (Object.keys(next).length === 0) create.mutate()
  }

  return (
    <Modal
      title="Новый канал"
      onClose={onClose}
      footer={
        <Button type="submit" form="new-channel" loading={create.isPending}>
          Создать канал
        </Button>
      }
    >
      <form id="new-channel" className="form form--tight" onSubmit={submit} noValidate>
        {create.isError && !Object.keys(errors).length && (
          <div className="alert">{describeError(create.error).message}</div>
        )}
        <Field label="Название канала" value={title} onChange={(e) => setTitle(e.target.value)} error={errors.title} />
        <Field label="Описание" value={about} onChange={(e) => setAbout(e.target.value)} placeholder="Необязательно" />
        <label className="switch">
          <input type="checkbox" checked={isPublic} onChange={(e) => setIsPublic(e.target.checked)} />
          <span>Публичный канал: его можно найти в поиске</span>
        </label>
        {isPublic && (
          <Field
            label="Адрес канала"
            prefix="@"
            value={handle}
            onChange={(e) => setHandle(e.target.value)}
            autoCapitalize="none"
            spellCheck={false}
            error={errors.handle}
          />
        )}
      </form>
    </Modal>
  )
}

export function NewSecretDialog({ onClose }: { onClose: () => void }) {
  const [picked, setPicked] = useState<PublicUser[]>([])
  const opened = useOpenCreated(onClose)
  const peer = picked.at(-1)
  const create = useMutation({
    mutationFn: async () => {
      const pair = await generateKeyPair()
      const chat = await chatApi.createSecret(peer!.id, pair.publicKey)
      await storeKeyPair(chat.id, pair)
      return chat
    },
    onSuccess: opened,
  })

  return (
    <Modal
      title="Секретный чат"
      onClose={onClose}
      footer={
        <Button onClick={() => create.mutate()} disabled={!peer} loading={create.isPending}>
          Начать секретный чат
        </Button>
      }
    >
      <div className="form form--tight">
        <p className="muted">
          Сообщения и файлы шифруются на устройствах. Сервер видит только зашифрованные данные. Чат будет доступен только
          на этом устройстве и на одном устройстве собеседника.
        </p>
        {create.isError && <div className="alert">{describeError(create.error).message}</div>}
        <UserPicker selected={peer ? [peer] : []} onChange={(list) => setPicked(list.slice(-1))} />
      </div>
    </Modal>
  )
}
