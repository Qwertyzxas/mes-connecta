import type { Locator, Page } from '@playwright/test'
import type { Credentials } from '../support/users'

export class RegisterPage {
  readonly heading: Locator
  readonly email: Locator
  readonly username: Locator
  readonly displayName: Locator
  readonly password: Locator
  readonly submit: Locator

  constructor(readonly page: Page) {
    this.heading = page.getByRole('heading', { name: 'Регистрация', level: 1 })
    this.email = page.getByLabel('Почта', { exact: true })
    this.username = page.getByLabel('Имя пользователя', { exact: true })
    this.displayName = page.getByLabel('Отображаемое имя', { exact: true })
    this.password = page.getByLabel('Пароль', { exact: true })
    this.submit = page.getByRole('button', { name: 'Создать аккаунт' })
  }

  async goto() {
    await this.page.goto('/register')
  }

  async register(user: Credentials) {
    await this.email.fill(user.email)
    await this.username.fill(user.username)
    await this.displayName.fill(user.displayName)
    await this.password.fill(user.password)
    await this.submit.click()
  }
}
