import type { Locator, Page } from '@playwright/test'

export class LoginPage {
  readonly heading: Locator
  readonly email: Locator
  readonly password: Locator
  readonly submit: Locator
  readonly formError: Locator
  readonly registerLink: Locator

  constructor(readonly page: Page) {
    this.heading = page.getByRole('heading', { name: 'Вход', level: 1 })
    this.email = page.getByLabel('Почта', { exact: true })
    this.password = page.getByLabel('Пароль', { exact: true })
    this.submit = page.getByRole('button', { name: 'Войти' })
    this.formError = page.locator('.alert')
    this.registerLink = page.getByRole('link', { name: 'Зарегистрироваться' })
  }

  async goto() {
    await this.page.goto('/login')
  }

  async login(email: string, password: string) {
    await this.email.fill(email)
    await this.password.fill(password)
    await this.submit.click()
  }
}
