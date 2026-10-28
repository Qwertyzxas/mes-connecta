import { useState, type ChangeEvent, type FormEvent } from 'react'
import { Link } from 'react-router'
import { useMutation } from '@tanstack/react-query'
import { AuthShell } from '../components/AuthShell'
import { Button } from '../components/Button'
import { Field } from '../components/Field'
import { authApi } from '../lib/auth-api'
import { describeError, type FormError } from '../lib/errors'
import {
  collectErrors,
  normalizeUsername,
  validateDisplayName,
  validateEmail,
  validatePassword,
  validateUsername,
} from '../lib/validation'
import { useAuth } from '../store/auth'

type FormState = {
  email: string
  username: string
  display_name: string
  password: string
}

const initial: FormState = { email: '', username: '', display_name: '', password: '' }
const noErrors: FormError = { message: null, fields: {} }

export function RegisterPage() {
  const signIn = useAuth((s) => s.signIn)
  const [form, setForm] = useState<FormState>(initial)
  const [errors, setErrors] = useState<FormError>(noErrors)

  const register = useMutation({
    mutationFn: authApi.register,
    onSuccess: signIn,
    onError: (err) => setErrors(describeError(err)),
  })

  const update = (key: keyof FormState) => (e: ChangeEvent<HTMLInputElement>) => {
    setForm((f) => ({ ...f, [key]: e.target.value }))
    if (errors.fields[key]) {
      setErrors((prev) => {
        const fields = { ...prev.fields }
        delete fields[key]
        return { ...prev, fields }
      })
    }
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const fields = collectErrors({
      email: validateEmail(form.email),
      username: validateUsername(form.username),
      display_name: validateDisplayName(form.display_name),
      password: validatePassword(form.password),
    })
    setErrors({ message: null, fields })
    if (Object.keys(fields).length === 0) {
      register.mutate({
        email: form.email.trim(),
        username: normalizeUsername(form.username),
        display_name: form.display_name.trim(),
        password: form.password,
      })
    }
  }

  return (
    <AuthShell
      title="Регистрация"
      subtitle="Создайте аккаунт. Это бесплатно и займёт минуту."
      footer={
        <>
          Уже есть аккаунт? <Link to="/login">Войти</Link>
        </>
      }
    >
      <form className="form" onSubmit={submit} noValidate>
        {errors.message && (
          <div className="alert" role="alert">
            {errors.message}
          </div>
        )}
        <Field
          label="Почта"
          type="email"
          name="email"
          autoComplete="email"
          inputMode="email"
          autoFocus
          value={form.email}
          onChange={update('email')}
          error={errors.fields.email}
        />
        <Field
          label="Имя пользователя"
          name="username"
          prefix="@"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          value={form.username}
          onChange={update('username')}
          error={errors.fields.username}
          hint="По нему вас найдут. Латиница, цифры и _"
        />
        <Field
          label="Отображаемое имя"
          name="display_name"
          autoComplete="name"
          placeholder="Необязательно"
          value={form.display_name}
          onChange={update('display_name')}
          error={errors.fields.display_name}
        />
        <Field
          label="Пароль"
          type="password"
          name="password"
          autoComplete="new-password"
          value={form.password}
          onChange={update('password')}
          error={errors.fields.password}
          hint="Не меньше 8 символов"
        />
        <Button type="submit" loading={register.isPending}>
          Создать аккаунт
        </Button>
      </form>
    </AuthShell>
  )
}
