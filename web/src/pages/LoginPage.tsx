import { useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { useMutation } from '@tanstack/react-query'
import { AuthShell } from '../components/AuthShell'
import { Button } from '../components/Button'
import { Field } from '../components/Field'
import { authApi } from '../lib/auth-api'
import { describeError, type FormError } from '../lib/errors'
import { collectErrors, validateEmail } from '../lib/validation'
import { useAuth } from '../store/auth'

const noErrors: FormError = { message: null, fields: {} }

export function LoginPage() {
  const signIn = useAuth((s) => s.signIn)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState<FormError>(noErrors)

  const login = useMutation({
    mutationFn: authApi.login,
    onSuccess: signIn,
    onError: (err) => setErrors(describeError(err)),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const fields = collectErrors({
      email: validateEmail(email),
      password: password ? null : 'Введите пароль',
    })
    setErrors({ message: null, fields })
    if (Object.keys(fields).length === 0) {
      login.mutate({ email: email.trim(), password })
    }
  }

  return (
    <AuthShell
      title="Вход"
      subtitle="С возвращением! Войдите, чтобы продолжить общение."
      footer={
        <>
          Нет аккаунта? <Link to="/register">Зарегистрироваться</Link>
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
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          error={errors.fields.email}
        />
        <Field
          label="Пароль"
          type="password"
          name="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          error={errors.fields.password}
        />
        <Button type="submit" loading={login.isPending}>
          Войти
        </Button>
      </form>
    </AuthShell>
  )
}
