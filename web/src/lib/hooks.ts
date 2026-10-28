import { useEffect, useState } from 'react'

export function useNow(intervalMs: number, enabled = true): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!enabled) return
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs, enabled])
  return now
}

export function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(id)
  }, [value, delayMs])
  return debounced
}

export function useDocumentVisible(): boolean {
  const [visible, setVisible] = useState(() => document.visibilityState === 'visible')
  useEffect(() => {
    const update = () => setVisible(document.visibilityState === 'visible')
    document.addEventListener('visibilitychange', update)
    window.addEventListener('focus', update)
    return () => {
      document.removeEventListener('visibilitychange', update)
      window.removeEventListener('focus', update)
    }
  }, [])
  return visible
}
