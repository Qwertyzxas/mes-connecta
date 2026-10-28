import type { ReactNode } from 'react'
import { Logo } from './Logo'

type AuthShellProps = {
  title: string
  subtitle: string
  children: ReactNode
  footer: ReactNode
}

export function AuthShell({ title, subtitle, children, footer }: AuthShellProps) {
  return (
    <main className="auth">
      <div className="auth__card">
        <Logo />
        <h1 className="auth__title">{title}</h1>
        <p className="auth__subtitle">{subtitle}</p>
        {children}
      </div>
      <p className="auth__footer">{footer}</p>
    </main>
  )
}
