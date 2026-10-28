export function LockIcon({ size = 16 }: { size?: number }) {
  return (
    <svg className="lock-icon" viewBox="0 0 24 24" width={size} height={size} aria-label="секретный чат" role="img">
      <rect x="5" y="10.5" width="14" height="10" rx="2.5" fill="currentColor" />
      <path d="M8 10.5V8a4 4 0 0 1 8 0v2.5" fill="none" stroke="currentColor" strokeWidth="2" />
    </svg>
  )
}
