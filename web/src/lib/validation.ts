import { fieldMessage } from './errors'

const emailRe = /^[^\s@]+@[^\s@]+\.[^\s@.]+$/
const usernameRe = /^[a-z0-9_]{3,32}$/

export const normalizeUsername = (value: string) => value.trim().replace(/^@/, '').toLowerCase()

export function validateEmail(email: string): string | null {
  return emailRe.test(email.trim()) ? null : fieldMessage('email', 'invalid')
}

export function validateUsername(username: string): string | null {
  return usernameRe.test(normalizeUsername(username)) ? null : fieldMessage('username', 'invalid')
}

export function validateDisplayName(name: string): string | null {
  return [...name.trim()].length > 64 ? fieldMessage('display_name', 'too_long') : null
}

export function validatePassword(password: string): string | null {
  const length = [...password].length
  if (length < 8) return fieldMessage('password', 'too_short')
  if (length > 128) return fieldMessage('password', 'too_long')
  return null
}

export function collectErrors(checks: Record<string, string | null>): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const [field, error] of Object.entries(checks)) {
    if (error) errors[field] = error
  }
  return errors
}
