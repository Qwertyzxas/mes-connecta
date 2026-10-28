import { create } from 'zustand'
import { ApiError } from './api'
import { chatApi } from './chat-api'
import type { ServerEvent } from './types'

type ConnectionStatus = 'idle' | 'connecting' | 'online' | 'offline'

export const useConnection = create<{ status: ConnectionStatus }>()(() => ({ status: 'idle' }))

type Listener = (event: ServerEvent) => void

const MAX_DELAY = 30_000

class RealtimeClient {
  private ws: WebSocket | null = null
  private listeners = new Set<Listener>()
  private attempts = 0
  private timer: ReturnType<typeof setTimeout> | null = null
  private running = false
  private connecting = false

  constructor() {
    if (typeof window !== 'undefined') {
      window.addEventListener('online', () => this.reconnectNow())
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') this.reconnectNow()
      })
    }
  }

  subscribe(listener: Listener) {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  start() {
    if (this.running) return
    this.running = true
    void this.connect()
  }

  stop() {
    this.running = false
    this.clearTimer()
    this.attempts = 0
    const ws = this.ws
    this.ws = null
    ws?.close(1000)
    useConnection.setState({ status: 'idle' })
  }

  sendTyping(chatId: string) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'typing', chat_id: chatId }))
    }
  }

  private reconnectNow() {
    if (!this.running || this.connecting) return
    if (this.ws && this.ws.readyState <= WebSocket.OPEN) return
    this.clearTimer()
    void this.connect()
  }

  private clearTimer() {
    if (this.timer) {
      clearTimeout(this.timer)
      this.timer = null
    }
  }

  private schedule() {
    if (!this.running) return
    this.clearTimer()
    const base = Math.min(MAX_DELAY, 500 * 2 ** this.attempts)
    const delay = base / 2 + Math.random() * (base / 2)
    this.attempts++
    useConnection.setState({ status: 'offline' })
    this.timer = setTimeout(() => void this.connect(), delay)
  }

  private async connect() {
    if (!this.running || this.connecting) return
    this.connecting = true
    useConnection.setState({ status: 'connecting' })

    let ticket: string
    try {
      ticket = (await chatApi.wsTicket()).ticket
    } catch (err) {
      this.connecting = false
      if (err instanceof ApiError && err.status === 401) {
        this.stop()
        return
      }
      this.schedule()
      return
    }
    if (!this.running) {
      this.connecting = false
      return
    }

    const protocol = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${protocol}://${location.host}/api/ws?ticket=${encodeURIComponent(ticket)}`)
    this.ws = ws
    this.connecting = false

    ws.onmessage = (msg) => {
      let event: ServerEvent
      try {
        event = JSON.parse(String(msg.data)) as ServerEvent
      } catch {
        return
      }
      if (event.type === 'ready') {
        this.attempts = 0
        useConnection.setState({ status: 'online' })
      }
      for (const listener of this.listeners) listener(event)
    }
    ws.onclose = () => {
      if (this.ws !== ws) return
      this.ws = null
      this.schedule()
    }
  }
}

export const realtime = new RealtimeClient()
