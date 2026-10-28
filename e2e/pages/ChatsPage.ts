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
  readonly composerContext: Locator
  readonly info: Locator
  readonly fileInput: Locator
  readonly micButton: Locator
  readonly staged: Locator
  readonly readOnlyNote: Locator

  constructor(readonly page: Page) {
    this.search = page.getByLabel('Поиск', { exact: true })
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
    this.composerContext = page.getByTestId('composer-context')
    this.info = page.getByRole('complementary', { name: 'Информация о чате' })
    this.fileInput = page.getByLabel('Прикрепить файл')
    this.micButton = page.getByRole('button', { name: 'Записать голосовое' })
    this.staged = page.getByRole('list', { name: 'Вложения к отправке' }).getByRole('listitem')
    this.readOnlyNote = page.getByRole('note')
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

  async messageAction(text: string, action: 'Ответить' | 'Копировать' | 'Изменить' | 'Удалить') {
    await this.bubble(text).getByRole('button', { name: 'Действия с сообщением' }).click()
    await this.page.getByRole('menuitem', { name: action }).click()
  }

  menuItems(): Locator {
    return this.page.getByRole('menu', { name: 'Действия с сообщением' }).getByRole('menuitem')
  }

  systemMessage(text: string): Locator {
    return this.log.locator('.system-msg').filter({ hasText: text })
  }

  member(name: string): Locator {
    return this.info.getByTestId('member').filter({ hasText: name })
  }

  async openInfo() {
    await this.page.getByRole('button', { name: 'Информация о чате' }).click()
    await this.info.waitFor()
  }

  async pickUsers(scope: Locator, usernames: string[]) {
    for (const username of usernames) {
      await scope.getByLabel('Найти участника').fill(username)
      await scope.getByRole('list', { name: 'Кого добавить' }).getByRole('button').filter({ hasText: `@${username}` }).click()
    }
  }

  async createGroup(title: string, usernames: string[]) {
    await this.page.getByRole('button', { name: 'Создать', exact: true }).click()
    await this.page.getByRole('menuitem', { name: 'Новая группа' }).click()
    const dialog = this.page.getByRole('dialog', { name: 'Новая группа' })
    await dialog.getByLabel('Название группы').fill(title)
    await this.pickUsers(dialog, usernames)
    await dialog.getByRole('button', { name: 'Создать группу' }).click()
    await this.conversation.waitFor()
  }

  async createChannel(title: string, handle: string) {
    await this.page.getByRole('button', { name: 'Создать', exact: true }).click()
    await this.page.getByRole('menuitem', { name: 'Новый канал' }).click()
    const dialog = this.page.getByRole('dialog', { name: 'Новый канал' })
    await dialog.getByLabel('Название канала').fill(title)
    await dialog.getByLabel('Адрес канала').fill(handle)
    await dialog.getByRole('button', { name: 'Создать канал' }).click()
    await this.conversation.waitFor()
  }

  async send(text: string) {
    await this.composer.fill(text)
    await this.composer.press('Enter')
  }
}
