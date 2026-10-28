import { api } from './api'
import type { Attachment, AttachmentKind, Chat, Message, ReplyPreview } from './types'

const DB_NAME = 'connecta-e2e'
const STORE = 'chats'
const INFO = new TextEncoder().encode('connecta-secret-v1')

type KeyRecord = {
  chatId: string
  publicKey: string
  privateKey?: CryptoKey
  key?: CryptoKey
}

export type SecretFile = {
  id: string
  kind: AttachmentKind
  name: string
  type: string
  size: number
  key: string
  iv: string
  width?: number
  height?: number
  duration_ms?: number
}

export type SecretPayload = {
  v: 1
  t: string
  f?: SecretFile[]
}

export type SecretAttachment = Attachment & { secret: { key: string; iv: string } }

let dbPromise: Promise<IDBDatabase> | null = null
let secretUser = ''

export function setSecretUser(id: string) {
  secretUser = id
}

export function currentSecretUser() {
  return secretUser
}
const records = new Map<string, KeyRecord | null>()
const chats = new Map<string, Chat>()
const derived = new Map<string, Promise<CryptoKey | null>>()
const plainCache = new Map<string, SecretPayload | null>()

function openDb(): Promise<IDBDatabase> {
  dbPromise ??= new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1)
    req.onupgradeneeded = () => req.result.createObjectStore(STORE, { keyPath: 'chatId' })
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
  return dbPromise
}

async function tx<T>(mode: IDBTransactionMode, fn: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await openDb()
  return new Promise((resolve, reject) => {
    const t = db.transaction(STORE, mode)
    const req = fn(t.objectStore(STORE))
    t.oncomplete = () => resolve(req.result)
    t.onerror = () => reject(t.error)
    t.onabort = () => reject(t.error)
  })
}

async function loadRecord(chatId: string): Promise<KeyRecord | null> {
  if (records.has(chatId)) return records.get(chatId) ?? null
  const record = ((await tx('readonly', (s) => s.get(chatId))) as KeyRecord | undefined) ?? null
  records.set(chatId, record)
  return record
}

async function saveRecord(record: KeyRecord) {
  records.set(record.chatId, record)
  await tx('readwrite', (s) => s.put(record))
}

export function toBase64(bytes: ArrayBuffer | Uint8Array): string {
  const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes)
  let s = ''
  for (let i = 0; i < view.length; i += 0x8000) s += String.fromCharCode(...view.subarray(i, i + 0x8000))
  return btoa(s)
}

export function fromBase64(value: string): Uint8Array<ArrayBuffer> {
  const raw = atob(value)
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

export type KeyPair = { privateKey: CryptoKey; publicKey: string }

export async function generateKeyPair(): Promise<KeyPair> {
  const pair = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits'])
  const raw = await crypto.subtle.exportKey('raw', pair.publicKey)
  return { privateKey: pair.privateKey, publicKey: toBase64(raw) }
}

export async function storeKeyPair(chatId: string, pair: KeyPair) {
  await saveRecord({ chatId, publicKey: pair.publicKey, privateKey: pair.privateKey })
}

async function derive(chatId: string, privateKey: CryptoKey, peerKey: string): Promise<CryptoKey> {
  const peer = await crypto.subtle.importKey('raw', fromBase64(peerKey), { name: 'ECDH', namedCurve: 'P-256' }, false, [])
  const bits = await crypto.subtle.deriveBits({ name: 'ECDH', public: peer }, privateKey, 256)
  const hkdf = await crypto.subtle.importKey('raw', bits, 'HKDF', false, ['deriveKey'])
  return crypto.subtle.deriveKey(
    { name: 'HKDF', hash: 'SHA-256', salt: new TextEncoder().encode(chatId), info: INFO },
    hkdf,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt', 'decrypt'],
  )
}

export function registerChats(list: Chat[] | undefined) {
  for (const chat of list ?? []) registerChat(chat)
}

export function registerChat(chat: Chat) {
  if (chat.kind !== 'secret') return
  const prev = chats.get(chat.id)
  chats.set(chat.id, chat)
  if (prev && !prev.peer_key && chat.peer_key) derived.delete(chat.id)
}

export function isSecret(chatId: string): boolean {
  return chats.get(chatId)?.kind === 'secret'
}

export async function hasKeys(chatId: string): Promise<boolean> {
  const record = await loadRecord(chatId).catch(() => null)
  return Boolean(record?.key || record?.privateKey)
}

export function chatKey(chatId: string): Promise<CryptoKey | null> {
  let pending = derived.get(chatId)
  if (!pending) {
    pending = (async () => {
      const record = await loadRecord(chatId).catch(() => null)
      if (!record) return null
      if (record.key) return record.key
      if (!chats.get(chatId)?.peer_key) {
        const fresh = await api<Chat>(`/chats/${chatId}`).catch(() => null)
        if (fresh) registerChat(fresh)
      }
      const peerKey = chats.get(chatId)?.peer_key
      if (!record.privateKey || !peerKey) return null
      const key = await derive(chatId, record.privateKey, peerKey)
      await saveRecord({ chatId, publicKey: record.publicKey, key })
      return key
    })().catch(() => null)
    derived.set(chatId, pending)
    void pending.then((key) => {
      if (!key && derived.get(chatId) === pending) derived.delete(chatId)
    })
  }
  return pending
}

function aad(chatId: string, senderId: string) {
  return new TextEncoder().encode(`${chatId}|${senderId}`)
}

export async function encryptPayload(chatId: string, senderId: string, payload: SecretPayload): Promise<string> {
  const key = await chatKey(chatId)
  if (!key) throw new Error('Нет ключа шифрования для этого чата')
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const data = new TextEncoder().encode(JSON.stringify(payload))
  const ct = await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: aad(chatId, senderId) }, key, data)
  const out = new Uint8Array(iv.length + ct.byteLength)
  out.set(iv)
  out.set(new Uint8Array(ct), iv.length)
  const body = toBase64(out)
  plainCache.set(`${chatId}|${senderId}|${body}`, payload)
  return body
}

export async function decryptPayload(chatId: string, senderId: string, body: string): Promise<SecretPayload | null> {
  const cacheKey = `${chatId}|${senderId}|${body}`
  if (plainCache.has(cacheKey)) return plainCache.get(cacheKey) ?? null
  const key = await chatKey(chatId)
  if (!key) return null
  try {
    const raw = fromBase64(body)
    const pt = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: raw.subarray(0, 12), additionalData: aad(chatId, senderId) },
      key,
      raw.subarray(12),
    )
    const payload = JSON.parse(new TextDecoder().decode(pt)) as SecretPayload
    if (typeof payload.t !== 'string') throw new Error('bad payload')
    plainCache.set(cacheKey, payload)
    return payload
  } catch {
    plainCache.set(cacheKey, null)
    return null
  }
}

export async function encryptBlob(blob: Blob): Promise<{ blob: Blob; key: string; iv: string }> {
  const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, true, ['encrypt', 'decrypt'])
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const ct = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, await blob.arrayBuffer())
  const raw = await crypto.subtle.exportKey('raw', key)
  return { blob: new Blob([ct], { type: 'application/octet-stream' }), key: toBase64(raw), iv: toBase64(iv) }
}

export async function decryptBlob(data: ArrayBuffer, key: string, iv: string, type: string): Promise<Blob> {
  const k = await crypto.subtle.importKey('raw', fromBase64(key), 'AES-GCM', false, ['decrypt'])
  const pt = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: fromBase64(iv) }, k, data)
  return new Blob([pt], { type })
}

function secretAttachments(message: Message, files: SecretFile[] | undefined): SecretAttachment[] {
  const urls = new Map(message.attachments.map((a) => [a.id, a.url]))
  return (files ?? [])
    .filter((f) => urls.has(f.id))
    .map((f) => ({
      id: f.id,
      kind: f.kind,
      name: f.name,
      content_type: f.type,
      size: f.size,
      width: f.width,
      height: f.height,
      duration_ms: f.duration_ms,
      url: urls.get(f.id) ?? '',
      secret: { key: f.key, iv: f.iv },
    }))
}

async function openReply(chatId: string, reply: ReplyPreview | null): Promise<ReplyPreview | null> {
  if (!reply || reply.deleted || !reply.body) return reply
  const payload = await decryptPayload(chatId, reply.sender_id, reply.body)
  const text = payload ? payload.t || (payload.f?.length ? '📎 Вложение' : '') : '🔒 Сообщение'
  return { ...reply, body: [...text].slice(0, 200).join('') }
}

export async function openMessage(message: Message): Promise<Message> {
  if (!message.e2e) return message
  if (message.deleted_at || message.kind !== 'text') return { ...message, reply_to: null }
  const payload = await decryptPayload(message.chat_id, message.sender_id, message.body)
  const reply_to = await openReply(message.chat_id, message.reply_to)
  if (!payload) return { ...message, body: '', undecryptable: true, attachments: [], reply_to }
  return { ...message, body: payload.t, attachments: secretAttachments(message, payload.f), reply_to }
}

export async function openMessages(list: Message[]): Promise<Message[]> {
  return Promise.all(list.map(openMessage))
}

export async function openChat(chat: Chat): Promise<Chat> {
  registerChat(chat)
  if (!chat.last_message?.e2e) return chat
  return { ...chat, last_message: await openMessage(chat.last_message) }
}

export async function openChats(list: Chat[]): Promise<Chat[]> {
  registerChats(list)
  return Promise.all(list.map(openChat))
}

export function payloadFor(message: Message, text: string): SecretPayload {
  const files: SecretFile[] = (message.attachments as SecretAttachment[])
    .filter((a) => a.secret)
    .map((a) => ({
      id: a.id,
      kind: a.kind,
      name: a.name,
      type: a.content_type,
      size: a.size,
      key: a.secret.key,
      iv: a.secret.iv,
      width: a.width,
      height: a.height,
      duration_ms: a.duration_ms,
    }))
  return files.length ? { v: 1, t: text, f: files } : { v: 1, t: text }
}

export async function fingerprint(chat: Chat): Promise<string | null> {
  if (!chat.my_key || !chat.peer_key) return null
  const material = [chat.my_key, chat.peer_key].sort().join('|')
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(material)))
  const hex = [...digest.subarray(0, 16)].map((b) => b.toString(16).padStart(2, '0').toUpperCase()).join('')
  return hex.match(/.{4}/g)!.join(' ')
}

export async function forgetChatKeys(chatId: string) {
  records.delete(chatId)
  derived.delete(chatId)
  chats.delete(chatId)
  await tx('readwrite', (s) => s.delete(chatId)).catch(() => {})
}

export async function clearAllKeys() {
  records.clear()
  derived.clear()
  chats.clear()
  plainCache.clear()
  await tx('readwrite', (s) => s.clear()).catch(() => {})
}
