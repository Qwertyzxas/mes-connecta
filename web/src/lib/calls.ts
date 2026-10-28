import { create } from 'zustand'
import type { Room } from 'livekit-client'
import { getAccessToken } from './api'
import { callApi } from './chat-api'
import { describeError } from './errors'
import { showLocalNotification } from './push'
import { startRinging, stopRinging } from './ringer'
import type { Call, CallSession, CallerInfo, Chat, ServerEvent } from './types'
import { chatTitle } from './chat-display'

export type CallPhase = 'idle' | 'incoming' | 'connecting' | 'outgoing' | 'active' | 'ended'

type CallState = {
  phase: CallPhase
  call: Call | null
  chatId: string | null
  title: string
  avatarId: string
  video: boolean
  caller: CallerInfo | null
  micOn: boolean
  camOn: boolean
  error: string | null
  endedText: string | null
  room: Room | null
  connected: boolean
  audioBlocked: boolean
  version: number
}

const initial: CallState = {
  phase: 'idle',
  call: null,
  chatId: null,
  title: '',
  avatarId: '',
  video: false,
  caller: null,
  micOn: true,
  camOn: false,
  error: null,
  endedText: null,
  room: null,
  connected: false,
  audioBlocked: false,
  version: 0,
}

export const useCall = create<CallState>()(() => initial)

let room: Room | null = null
let meId = ''
let endTimer: ReturnType<typeof setTimeout> | null = null

const isLive = (call: Call) => call.status === 'ringing' || call.status === 'active'

useCall.subscribe((state, prev) => {
  if (state.phase === prev.phase) return
  if (state.phase === 'incoming') startRinging('incoming')
  else if (state.phase === 'outgoing') startRinging('outgoing')
  else stopRinging()
})

function bump() {
  useCall.setState((s) => ({ version: s.version + 1 }))
}

export function resolveRtcUrl(url: string): string {
  if (!url.startsWith('/')) return url
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}${url}`
}

function dropRoom() {
  const r = room
  room = null
  useCall.setState({ room: null, connected: false })
  if (r) void r.disconnect()
}

function finish(text: string | null) {
  dropRoom()
  if (endTimer) clearTimeout(endTimer)
  if (!text) {
    useCall.setState({ ...initial })
    return
  }
  useCall.setState({ phase: 'ended', endedText: text })
  endTimer = setTimeout(() => {
    endTimer = null
    if (useCall.getState().phase === 'ended') useCall.setState({ ...initial })
  }, 1800)
}

function endText(call: Call, me: string): string {
  switch (call.status) {
    case 'declined':
      return call.started_by === me ? 'Абонент отклонил звонок' : 'Звонок отклонён'
    case 'missed':
      return call.started_by === me ? 'Нет ответа' : 'Звонок завершён'
    default:
      return 'Звонок завершён'
  }
}

async function connect(session: CallSession, video: boolean) {
  const { Room, RoomEvent } = await import('livekit-client')
  const r = new Room({ adaptiveStream: true, dynacast: true })
  room = r
  useCall.setState({ room: r, micOn: true, camOn: video })
  for (const ev of [
    RoomEvent.ParticipantConnected,
    RoomEvent.ParticipantDisconnected,
    RoomEvent.TrackSubscribed,
    RoomEvent.TrackUnsubscribed,
    RoomEvent.TrackMuted,
    RoomEvent.TrackUnmuted,
    RoomEvent.LocalTrackPublished,
    RoomEvent.LocalTrackUnpublished,
    RoomEvent.ActiveSpeakersChanged,
  ]) {
    r.on(ev, bump)
  }
  r.on(RoomEvent.AudioPlaybackStatusChanged, () => useCall.setState({ audioBlocked: !r.canPlaybackAudio }))
  r.on(RoomEvent.Disconnected, () => {
    if (room === r) void hangUp()
  })

  try {
    await r.connect(resolveRtcUrl(session.url), session.token)
  } catch {
    if (room !== r) return
    useCall.setState({ error: 'Не удалось подключиться к звонку' })
    await hangUp('Не удалось подключиться')
    return
  }
  if (room !== r) {
    void r.disconnect()
    return
  }
  useCall.setState({ connected: true, audioBlocked: !r.canPlaybackAudio })
  try {
    await r.localParticipant.setMicrophoneEnabled(true)
  } catch {
    useCall.setState({ micOn: false, error: 'Нет доступа к микрофону' })
  }
  if (video) {
    try {
      await r.localParticipant.setCameraEnabled(true)
    } catch {
      useCall.setState({ camOn: false, error: 'Нет доступа к камере' })
    }
  }
  bump()
}

export async function startCall(chat: Chat, video: boolean) {
  if (useCall.getState().phase !== 'idle') return
  useCall.setState({
    ...initial,
    phase: 'connecting',
    chatId: chat.id,
    title: chatTitle(chat),
    avatarId: chat.peer?.id ?? chat.id,
    video,
    camOn: video,
  })
  let session: CallSession
  try {
    session = await callApi.start(chat.id, video)
  } catch (err) {
    finish(describeError(err).message ?? 'Не удалось позвонить')
    return
  }
  if (useCall.getState().phase !== 'connecting') {
    void callApi.leave(session.call.id).catch(() => {})
    return
  }
  const withVideo = session.call.started_by !== meId ? session.call.video || video : video
  useCall.setState({
    call: session.call,
    video: withVideo,
    camOn: withVideo,
    phase: session.call.status === 'ringing' ? 'outgoing' : 'active',
  })
  await connect(session, withVideo)
}

export async function acceptCall() {
  const { call, phase } = useCall.getState()
  if (phase !== 'incoming' || !call) return
  useCall.setState({ phase: 'connecting' })
  let session: CallSession
  try {
    session = await callApi.join(call.id)
  } catch (err) {
    finish(describeError(err).message ?? 'Не удалось ответить')
    return
  }
  if (useCall.getState().phase !== 'connecting') {
    void callApi.leave(session.call.id).catch(() => {})
    return
  }
  useCall.setState({ call: session.call, phase: 'active', camOn: session.call.video })
  await connect(session, session.call.video)
}

export async function declineCall() {
  const { call, phase } = useCall.getState()
  if (phase !== 'incoming' || !call) return
  useCall.setState({ ...initial })
  await callApi.decline(call.id).catch(() => {})
}

export async function hangUp(text = 'Звонок завершён') {
  const { call, phase } = useCall.getState()
  if (phase === 'idle' || phase === 'ended') return
  if (phase === 'incoming') return declineCall()
  finish(text)
  if (call) await callApi.leave(call.id).catch(() => {})
}

export async function toggleMic() {
  const r = room
  if (!r) return
  const next = !useCall.getState().micOn
  try {
    await r.localParticipant.setMicrophoneEnabled(next)
    useCall.setState({ micOn: next, error: null })
  } catch {
    useCall.setState({ micOn: false, error: 'Нет доступа к микрофону' })
  }
  bump()
}

export async function toggleCamera() {
  const r = room
  if (!r) return
  const next = !useCall.getState().camOn
  try {
    await r.localParticipant.setCameraEnabled(next)
    useCall.setState({ camOn: next, error: null })
  } catch {
    useCall.setState({ camOn: false, error: 'Нет доступа к камере' })
  }
  bump()
}

export async function enableAudio() {
  await room?.startAudio().catch(() => {})
  useCall.setState({ audioBlocked: room ? !room.canPlaybackAudio : false })
}

function ringFor(call: Call, title: string, avatarId: string, caller: CallerInfo | null) {
  useCall.setState({
    ...initial,
    phase: 'incoming',
    call,
    chatId: call.chat_id,
    title,
    avatarId,
    video: call.video,
    caller,
  })
  if (document.visibilityState === 'hidden') {
    void showLocalNotification(
      title,
      call.video ? 'Входящий видеозвонок' : 'Входящий звонок',
      `/chat/${call.chat_id}`,
      `call-${call.id}`,
    )
  }
}

export function handleCallEvent(me: string, event: ServerEvent) {
  meId = me
  const state = useCall.getState()
  if (event.type === 'call.incoming') {
    if (event.caller.id === me || state.phase !== 'idle' || event.call.participants.includes(me)) return
    const title = event.call.chat_kind === 'direct' ? event.caller.display_name : event.chat_title
    const avatarId = event.call.chat_kind === 'direct' ? event.caller.id : event.call.chat_id
    ringFor(event.call, title, avatarId, event.caller)
    return
  }
  if (event.type !== 'call.updated' || state.call?.id !== event.call.id) return
  const call = event.call
  if (state.phase === 'incoming') {
    if (!isLive(call) || call.participants.includes(me)) useCall.setState({ ...initial })
    else useCall.setState({ call })
    return
  }
  if (!isLive(call)) {
    useCall.setState({ call })
    finish(endText(call, me))
    return
  }
  useCall.setState({ call, phase: state.phase === 'outgoing' && call.status === 'active' ? 'active' : state.phase })
}

export async function syncLiveCalls(me: string, chats: Chat[] | undefined) {
  meId = me
  if (useCall.getState().phase !== 'idle') return
  let live: Call[]
  try {
    live = await callApi.live()
  } catch {
    return
  }
  const ringing = live.find(
    (c) => c.status === 'ringing' && c.started_by !== me && !c.participants.includes(me) && c.chat_kind === 'direct',
  )
  if (!ringing || useCall.getState().phase !== 'idle') return
  const chat = chats?.find((c) => c.id === ringing.chat_id)
  ringFor(ringing, chat ? chatTitle(chat) : 'Входящий звонок', chat?.peer?.id ?? ringing.chat_id, null)
}

export function setCallUser(me: string) {
  meId = me
}

export function resetCalls() {
  if (endTimer) clearTimeout(endTimer)
  endTimer = null
  const { call, phase } = useCall.getState()
  if (call && (phase === 'active' || phase === 'outgoing' || phase === 'connecting')) {
    void callApi.leave(call.id).catch(() => {})
  }
  dropRoom()
  stopRinging()
  useCall.setState({ ...initial })
}

export function leaveOnUnload() {
  const { call, phase } = useCall.getState()
  if (!call || phase === 'idle' || phase === 'ended' || phase === 'incoming') return
  const token = getAccessToken()
  if (!token) return
  void fetch(`/api/calls/${call.id}/leave`, {
    method: 'POST',
    keepalive: true,
    headers: { Authorization: `Bearer ${token}` },
  }).catch(() => {})
}
