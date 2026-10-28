import type { Page } from '@playwright/test'
import { ChatsPage } from '../pages/ChatsPage'
import { expect, signUp, test } from '../support/fixtures'
import { uniqueUser, type Credentials } from '../support/users'

type Party = { page: Page; ui: ChatsPage; user: Credentials }

const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAIAAAA7ljmRAAAAFUlEQVR4XmP8H1DBAANMcBYDAyoHAELmAc2eigZTAAAAAElFTkSuQmCC',
  'base64',
)

async function chatPair(page: Page, openDevice: () => Promise<Page>): Promise<[Party, Party]> {
  const bobPage = await openDevice()
  const bob = { page: bobPage, ui: new ChatsPage(bobPage), user: uniqueUser({ displayName: 'Боб' }) }
  const alice = { page, ui: new ChatsPage(page), user: uniqueUser({ displayName: 'Алиса' }) }
  await signUp(bob.page, bob.user)
  await signUp(alice.page, alice.user)
  await alice.ui.startChatWith(bob.user.username)
  return [alice, bob]
}

test.describe('Вложения', () => {
  test('фото с подписью приходит собеседнику и открывается на весь экран', async ({ page, openDevice }) => {
    const [alice, bob] = await chatPair(page, openDevice)

    await alice.ui.fileInput.setInputFiles({ name: 'cat.png', mimeType: 'image/png', buffer: PNG })
    await expect(alice.ui.staged).toHaveCount(1)
    await alice.ui.send('смотри, кот')
    await expect(alice.ui.staged).toHaveCount(0)
    await expect(alice.ui.bubble('смотри, кот').getByLabel('Отправлено')).toBeVisible()

    await bob.ui.chatRow('Алиса').click()
    const photo = bob.ui.bubble('смотри, кот').getByRole('img', { name: 'cat.png' })
    await expect(photo).toBeVisible()
    await expect.poll(() => photo.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(4)

    await bob.ui.bubble('смотри, кот').getByRole('button', { name: 'Открыть cat.png' }).click()
    const viewer = bob.page.getByRole('dialog', { name: 'Просмотр фото' })
    await expect(viewer).toBeVisible()
    await bob.page.keyboard.press('Escape')
    await expect(viewer).toBeHidden()

    await alice.ui.fileInput.setInputFiles({ name: 'second.png', mimeType: 'image/png', buffer: PNG })
    await alice.ui.composer.press('Enter')
    await expect(bob.ui.chatRow('Алиса')).toContainText('🖼 Фото')
  })

  test('файл скачивается с исходным именем', async ({ page, openDevice }) => {
    const [alice, bob] = await chatPair(page, openDevice)

    await alice.ui.fileInput.setInputFiles({ name: 'отчёт.txt', mimeType: 'text/plain', buffer: Buffer.from('квартальный отчёт') })
    await expect(alice.ui.staged).toContainText('отчёт.txt')
    await alice.ui.composer.press('Enter')

    await bob.ui.chatRow('Алиса').click()
    const card = bob.ui.log.getByTestId('file-card')
    await expect(card).toContainText('отчёт.txt')
    await expect(card).toHaveAttribute('href', /\/api\/files\/[0-9a-f-]{36}\?exp=\d+&sig=/)

    const download = bob.page.waitForEvent('download')
    await card.click()
    const file = await download
    expect(file.suggestedFilename()).toBe('отчёт.txt')
  })

  test('вложение можно убрать до отправки', async ({ page, openDevice }) => {
    const [alice] = await chatPair(page, openDevice)
    await alice.ui.fileInput.setInputFiles([
      { name: 'a.png', mimeType: 'image/png', buffer: PNG },
      { name: 'b.txt', mimeType: 'text/plain', buffer: Buffer.from('b') },
    ])
    await expect(alice.ui.staged).toHaveCount(2)
    await alice.page.getByRole('button', { name: 'Убрать a.png' }).click()
    await expect(alice.ui.staged).toHaveCount(1)
    await expect(alice.ui.sendButton).toBeEnabled()
  })

  test('голосовое записывается и проигрывается у собеседника', async ({ page, openDevice }) => {
    const [alice, bob] = await chatPair(page, openDevice)
    await alice.ui.send('сейчас запишу')
    await bob.ui.chatRow('Алиса').click()

    await alice.ui.micButton.click()
    const timer = alice.page.getByTestId('recording-time')
    await expect(timer).toHaveText('0:01', { timeout: 5_000 })
    await alice.page.getByRole('button', { name: 'Отправить голосовое' }).click()

    const voice = bob.ui.log.getByTestId('voice')
    await expect(voice).toBeVisible()
    await expect(voice).toContainText(/0:0[12]/)
    await expect(bob.ui.chatRow('Алиса')).toContainText('🎤 Голосовое сообщение')
    await expect(alice.ui.log.getByTestId('voice').locator('xpath=ancestor::div[contains(@class,"bubble-row")]').getByLabel('Отправлено')).toBeVisible()
  })

  test('сорвавшаяся загрузка повторяется без потери файла', async ({ page, openDevice }) => {
    const [alice, bob] = await chatPair(page, openDevice)
    await alice.page.route('**/api/chats/*/attachments*', (route) => route.abort('internetdisconnected'))

    await alice.ui.fileInput.setInputFiles({ name: 'retry.txt', mimeType: 'text/plain', buffer: Buffer.from('retry') })
    await alice.ui.composer.press('Enter')
    const failed = alice.ui.bubbles.filter({ hasText: 'retry.txt' })
    await expect(failed).toContainText('Не отправлено')

    await alice.page.unroute('**/api/chats/*/attachments*')
    await failed.getByRole('button', { name: 'Повторить' }).click()
    await expect(alice.ui.log.getByTestId('file-card')).toContainText('retry.txt')

    await bob.ui.chatRow('Алиса').click()
    await expect(bob.ui.log.getByTestId('file-card')).toHaveCount(1)
  })
})
