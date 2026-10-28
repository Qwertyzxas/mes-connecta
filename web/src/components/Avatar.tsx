type AvatarProps = {
  id: string
  name: string
  size?: number
  online?: boolean
}

const palettes = [
  ['#2f9bf0', '#7a5be6'],
  ['#7a5be6', '#d4568f'],
  ['#18b6a4', '#2f9bf0'],
  ['#f08a3c', '#d4568f'],
  ['#4fbf6a', '#18b6a4'],
  ['#d4568f', '#f08a3c'],
]

function hash(value: string): number {
  let h = 0
  for (let i = 0; i < value.length; i++) h = (h * 31 + value.charCodeAt(i)) | 0
  return Math.abs(h)
}

export function initials(name: string): string {
  return (
    name
      .split(/\s+/)
      .filter(Boolean)
      .map((part) => part[0])
      .join('')
      .slice(0, 2)
      .toUpperCase() || '?'
  )
}

export function Avatar({ id, name, size = 44, online }: AvatarProps) {
  const [from, to] = palettes[hash(id) % palettes.length]!
  return (
    <span className="avatar-wrap" style={{ width: size, height: size }}>
      <span
        className="avatar-img"
        style={{ background: `linear-gradient(135deg, ${from}, ${to})`, fontSize: size * 0.38 }}
        aria-hidden="true"
      >
        {initials(name)}
      </span>
      {online && <span className="avatar-online" aria-label="в сети" />}
    </span>
  )
}
