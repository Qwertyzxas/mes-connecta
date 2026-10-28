import { ApiError, NetworkError } from './api'

const codeMessages: Record<string, string> = {
  invalid_credentials: 'Неверная почта или пароль',
  email_taken: 'Эта почта уже зарегистрирована',
  username_taken: 'Это имя пользователя уже занято',
  rate_limited: 'Слишком много попыток. Подождите немного и попробуйте снова',
  session_expired: 'Сеанс истёк, войдите заново',
  validation_failed: 'Проверьте поля формы',
}

const fieldMessages: Record<string, string> = {
  'email:invalid': 'Некорректный адрес почты',
  'username:invalid': '3–32 символа: латиница, цифры и _',
  'display_name:too_long': 'Не длиннее 64 символов',
  'password:too_short': 'Минимум 8 символов',
  'password:too_long': 'Не длиннее 128 символов',
}

const fieldByCode: Record<string, string> = {
  email_taken: 'email',
  username_taken: 'username',
}

export type FormError = {
  message: string | null
  fields: Record<string, string>
}

export function fieldMessage(field: string, code: string): string {
  return fieldMessages[`${field}:${code}`] ?? 'Некорректное значение'
}

export function describeError(err: unknown): FormError {
  if (err instanceof NetworkError) {
    return { message: 'Нет связи с сервером', fields: {} }
  }
  if (err instanceof ApiError) {
    const fields: Record<string, string> = {}
    for (const [field, code] of Object.entries(err.fields)) {
      fields[field] = fieldMessage(field, code)
    }
    const field = fieldByCode[err.code]
    if (field) {
      fields[field] = codeMessages[err.code] ?? err.message
      return { message: null, fields }
    }
    return { message: codeMessages[err.code] ?? 'Что-то пошло не так. Попробуйте ещё раз', fields }
  }
  return { message: 'Что-то пошло не так. Попробуйте ещё раз', fields: {} }
}
