import type { Chat, Message, Role } from './types'

export function chatTitle(chat: Chat): string {
  return chat.peer?.display_name ?? chat.title
}

export function chatAvatarId(chat: Chat): string {
  return chat.peer?.id ?? chat.id
}

export function canPost(chat: Chat): boolean {
  return chat.kind !== 'channel' || chat.role === 'owner' || chat.role === 'admin'
}

export function canManage(chat: Chat): boolean {
  return chat.kind !== 'direct' && (chat.role === 'owner' || chat.role === 'admin')
}

export const roleLabel: Record<Role, string> = {
  owner: 'владелец',
  admin: 'админ',
  member: '',
}

export function plural(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10
  const mod100 = n % 100
  if (mod10 === 1 && mod100 !== 11) return one
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few
  return many
}

export function membersText(chat: Chat): string {
  const n = chat.member_count
  if (chat.kind === 'channel') return `${n} ${plural(n, 'подписчик', 'подписчика', 'подписчиков')}`
  const base = `${n} ${plural(n, 'участник', 'участника', 'участников')}`
  return chat.online_count > 1 ? `${base}, ${chat.online_count} в сети` : base
}

export function systemText(message: Message, me: string): string {
  const meta = message.meta
  const actor = message.sender_id === me ? 'Вы' : message.sender.display_name
  const target = meta?.target_id === me ? 'вас' : (meta?.target_name ?? '')
  switch (meta?.action) {
    case 'created':
      return `${actor} ${message.sender_id === me ? 'создали' : 'создал(а)'} «${meta.title ?? ''}»`
    case 'added':
      return `${actor} ${message.sender_id === me ? 'добавили' : 'добавил(а)'} ${target}`
    case 'removed':
      return `${actor} ${message.sender_id === me ? 'удалили' : 'удалил(а)'} ${target}`
    case 'left':
      return `${actor} ${message.sender_id === me ? 'вышли' : 'вышел(ла)'} из группы`
    case 'renamed':
      return `${actor} ${message.sender_id === me ? 'переименовали' : 'переименовал(а)'} чат в «${meta.title ?? ''}»`
    default:
      return 'Служебное сообщение'
  }
}

export function previewText(chat: Chat, message: Message, me: string): string {
  if (message.kind === 'system') return systemText(message, me)
  const body = message.deleted_at ? 'Сообщение удалено' : message.body
  if (message.sender_id === me) return `Вы: ${body}`
  if (chat.kind === 'group') return `${message.sender.display_name}: ${body}`
  return body
}
