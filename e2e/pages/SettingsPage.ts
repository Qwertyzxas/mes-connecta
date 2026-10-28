import type { Locator, Page } from '@playwright/test'

export class SettingsPage {
  readonly name: Locator
  readonly meta: Locator
  readonly logout: Locator
  readonly sessions: Locator
  readonly currentSession: Locator
  readonly otherSessions: Locator

  constructor(readonly page: Page) {
    this.name = page.getByRole('heading', { level: 1 })
    this.meta = page.locator('.profile__meta')
    this.logout = page.getByRole('button', { name: 'Выйти' })
    this.sessions = page.locator('.sessions > li')
    this.currentSession = this.sessions.filter({ hasText: 'Это устройство' })
    this.otherSessions = this.sessions.filter({ hasNotText: 'Это устройство' })
  }

  async goto() {
    await this.page.goto('/settings')
  }

  async revokeOtherSession() {
    await this.otherSessions.first().getByRole('button', { name: 'Завершить' }).click()
  }
}
