const rtf = new Intl.RelativeTimeFormat('ru', { numeric: 'auto' })

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['week', 7 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
]

export function timeAgo(iso: string, now = Date.now()): string {
  const seconds = Math.round((new Date(iso).getTime() - now) / 1000)
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) {
      return rtf.format(Math.round(seconds / size), unit)
    }
  }
  return 'только что'
}

export function describeDevice(ua: string): string {
  const browser = /YaBrowser\//.test(ua)
    ? 'Яндекс Браузер'
    : /Edg\//.test(ua)
      ? 'Edge'
      : /OPR\//.test(ua)
        ? 'Opera'
        : /Firefox\//.test(ua)
          ? 'Firefox'
          : /Chrome\//.test(ua)
            ? 'Chrome'
            : /Safari\//.test(ua)
              ? 'Safari'
              : 'Браузер'

  const os = /Android/.test(ua)
    ? 'Android'
    : /iPhone|iPad|iPod/.test(ua)
      ? 'iOS'
      : /Windows/.test(ua)
        ? 'Windows'
        : /Mac OS X/.test(ua)
          ? 'macOS'
          : /Linux/.test(ua)
            ? 'Linux'
            : null

  return os ? `${browser}, ${os}` : browser
}

const timeFmt = new Intl.DateTimeFormat('ru', { hour: '2-digit', minute: '2-digit' })
const dayFmt = new Intl.DateTimeFormat('ru', { day: 'numeric', month: 'long' })
const dayYearFmt = new Intl.DateTimeFormat('ru', { day: 'numeric', month: 'long', year: 'numeric' })
const shortDateFmt = new Intl.DateTimeFormat('ru', { day: '2-digit', month: '2-digit', year: '2-digit' })

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
const DAY = 24 * 3600 * 1000

export function formatTime(iso: string): string {
  return timeFmt.format(new Date(iso))
}

export function dayKey(iso: string): number {
  return startOfDay(new Date(iso))
}

export function formatDay(iso: string, now = new Date()): string {
  const d = new Date(iso)
  const diff = Math.round((startOfDay(now) - startOfDay(d)) / DAY)
  if (diff === 0) return 'Сегодня'
  if (diff === 1) return 'Вчера'
  return d.getFullYear() === now.getFullYear() ? dayFmt.format(d) : dayYearFmt.format(d)
}

export function formatListTime(iso: string, now = new Date()): string {
  const d = new Date(iso)
  const diff = Math.round((startOfDay(now) - startOfDay(d)) / DAY)
  if (diff === 0) return timeFmt.format(d)
  if (diff === 1) return 'вчера'
  if (diff < 7) return new Intl.DateTimeFormat('ru', { weekday: 'short' }).format(d)
  return shortDateFmt.format(d)
}

export function lastSeenText(online: boolean, lastSeen: string | null, now = Date.now()): string {
  if (online) return 'в сети'
  if (!lastSeen) return 'был(а) давно'
  const seconds = (now - new Date(lastSeen).getTime()) / 1000
  if (seconds < 60) return 'был(а) только что'
  return `был(а) ${timeAgo(lastSeen, now)}`
}
