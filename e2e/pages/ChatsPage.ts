import type { Locator, Page } from '@playwright/test'

export class ChatsPage {
  readonly search: Locator
  readonly chatList: Locator
  readonly searchResults: Locator
  readonly emptyState: Locator
  readonly settingsLink: Locator
  readonly conversation: Locator
  readonly peerName: Locator
  readonly peerStatus: Locator
  readonly backButton: Locator
  readonly log: Locator
  readonly bubbles: Locator
  readonly composer: Locator
  readonly sendButton: Locator

  constructor(readonly page: Page) {
    this.search = page.getByLabel('Поиск людей')
    this.chatList = page.getByRole('list', { name: 'Чаты' })
    this.searchResults = page.getByRole('list', { name: 'Найденные пользователи' })
    this.emptyState = page.getByText('Чатов пока нет.')
    this.settingsLink = page.getByRole('link', { name: 'Настройки' })
    this.conversation = page.getByRole('region', { name: /^Чат с / })
    this.peerName = this.conversation.getByRole('heading', { level: 1 })
    this.peerStatus = page.getByTestId('peer-status')
    this.backButton = page.getByRole('link', { name: 'Назад к чатам' })
    this.log = page.getByRole('log', { name: 'Сообщения' })
    this.bubbles = this.log.locator('.bubble-row')
    this.composer = page.getByLabel('Сообщение', { exact: true })
    this.sendButton = page.getByRole('button', { name: 'Отправить' })
  }

  async goto() {
    await this.page.goto('/')
  }

  chatRow(displayName: string): Locator {
    return this.page.locator('ul[aria-label="Чаты"] a').filter({ hasText: displayName })
  }

  bubble(text: string): Locator {
    return this.bubbles.filter({ hasText: text })
  }

  async startChatWith(username: string) {
    await this.search.fill(username)
    await this.searchResults.getByRole('button').filter({ hasText: `@${username}` }).click()
    await this.conversation.waitFor()
  }

  async send(text: string) {
    await this.composer.fill(text)
    await this.composer.press('Enter')
  }
}
