type Ring = 'incoming' | 'outgoing'

let ctx: AudioContext | null = null
let timer: ReturnType<typeof setInterval> | null = null
let current: Ring | null = null

function tone(audio: AudioContext, kind: Ring) {
  const notes = kind === 'incoming' ? [660, 880] : [425]
  const start = audio.currentTime + 0.02
  notes.forEach((freq, i) => {
    const osc = audio.createOscillator()
    const gain = audio.createGain()
    const t = start + i * 0.42
    osc.type = 'sine'
    osc.frequency.value = freq
    gain.gain.setValueAtTime(0.0001, t)
    gain.gain.exponentialRampToValueAtTime(0.12, t + 0.03)
    gain.gain.setValueAtTime(0.12, t + (kind === 'incoming' ? 0.32 : 1.0))
    gain.gain.exponentialRampToValueAtTime(0.0001, t + (kind === 'incoming' ? 0.4 : 1.1))
    osc.connect(gain).connect(audio.destination)
    osc.start(t)
    osc.stop(t + 1.2)
  })
}

export function startRinging(kind: Ring) {
  if (current === kind) return
  stopRinging()
  try {
    ctx = new AudioContext()
  } catch {
    return
  }
  current = kind
  const audio = ctx
  tone(audio, kind)
  timer = setInterval(() => tone(audio, kind), kind === 'incoming' ? 2000 : 4000)
  if (kind === 'incoming' && 'vibrate' in navigator) navigator.vibrate?.([300, 200, 300])
}

export function stopRinging() {
  current = null
  if (timer) clearInterval(timer)
  timer = null
  void ctx?.close().catch(() => {})
  ctx = null
}
