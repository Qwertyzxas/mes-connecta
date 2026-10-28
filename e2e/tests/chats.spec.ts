import type { Page } from '@playwright/test'
import { ChatsPage } from '../pages/ChatsPage'
import { expect, signUp, test } from '../support/fixtures'
import { uniqueUser, type Credentials } from '../support/users'

type Party = { page: Page; ui: ChatsPage; user: Credentials }

async function twoUsers(page: Page, openDevice: () => Promise<Page>): Promise<[Party, Party]> {
  const alice = { page, ui: new ChatsPage(page), user: uniqueUser({ displayName: 'Алиса' }) }
  const bobPage = await openDevice()
  const bob = { page: bobPage, ui: new ChatsPage(bobPage), user: uniqueUser({ displayName: 'Боб' }) }
  await signUp(bob.page, bob.user)
  await signUp(alice.page, alice.user)
  return [alice, bob]
}

test.describe('Личные чаты', () => {
  test('переписка в реальном времени с отметками о прочтении', async ({ page, openDevice }) => {
    const [alice, bob] = await twoUsers(page, openDevice)

    await alice.ui.startChatWith(bob.user.username)
    await expect(alice.ui.peerName).toHaveText('Боб')
    await expect(alice.ui.peerStatus).toHaveText('в сети')
    await expect(alice.page).toHaveURL(/\/chat\/[0-9a-f-]{36}$/)

    await alice.ui.send('Привет, Боб!')
    const sent = alice.ui.bubble('Привет, Боб!')
    await expect(sent).toBeVisible()
    await expect(sent.getByLabel('Отправлено')).toBeVisible()

    const row = bob.ui.chatRow('Алиса')
    await expect(row).toContainText('Привет, Боб!')
    await expect(row.getByLabel('Непрочитанных: 1')).toBeVisible()

    await row.click()
    await expect(bob.ui.bubble('Привет, Боб!')).toBeVisible()
    await expect(row.getByLabel(/Непрочитанных/)).toHaveCount(0)
    await expect(sent.getByLabel('Прочитано')).toBeVisible()

    await bob.ui.send('Привет, Алиса 👋')
    await expect(alice.ui.bubble('Привет, Алиса 👋')).toBeVisible()
    await expect(alice.ui.chatRow('Боб')).toContainText('Привет, Алиса 👋')
  })

  test('собеседник видит, что ему печатают', async ({ page, openDevice }) => {
    const [alice, bob] = await twoUsers(page, openDevice)
    await alice.ui.startChatWith(bob.user.username)
    await alice.ui.send('Есть минутка?')
    await bob.ui.chatRow('Алиса').click()
    await expect(bob.ui.bubble('Есть минутка?')).toBeVisible()

    await alice.ui.composer.pressSequentially('Сейчас расскажу', { delay: 20 })
    await expect(bob.ui.peerStatus).toHaveText('печатает…')
    await expect(bob.ui.chatRow('Алиса')).toContainText('печатает…')

    await alice.ui.composer.press('Enter')
    await expect(bob.ui.bubble('Сейчас расскажу')).toBeVisible()
    await expect(bob.ui.peerStatus).toHaveText('в сети')
  })

  test('собеседник уходит из сети', async ({ page, openDevice }) => {
    const [alice, bob] = await twoUsers(page, openDevice)
    await alice.ui.startChatWith(bob.user.username)
    await expect(alice.ui.peerStatus).toHaveText('в сети')

    await bob.page.close()
    await expect(alice.ui.peerStatus).toHaveText('был(а) только что')
  })

  test('история сохраняется после перезагрузки', async ({ page, openDevice }) => {
    const [alice, bob] = await twoUsers(page, openDevice)
    await alice.ui.startChatWith(bob.user.username)
    for (const text of ['раз', 'два', 'три']) {
      await alice.ui.send(text)
      await expect(alice.ui.bubble(text).getByLabel('Отправлено')).toBeVisible()
    }

    await alice.page.reload()
    await expect(alice.ui.bubbles).toHaveCount(3)
    await expect(alice.ui.bubbles.nth(0)).toContainText('раз')
    await expect(alice.ui.bubbles.nth(2)).toContainText('три')
    await expect(alice.ui.log.getByRole('separator')).toHaveText('Сегодня')
  })

  test('Enter отправляет, Shift+Enter переносит строку', async ({ page, openDevice }) => {
    const [alice, bob] = await twoUsers(page, openDevice)
    await alice.ui.startChatWith(bob.user.username)

    await expect(alice.ui.sendButton).toBeDisabled()
    await alice.ui.composer.fill('   ')
    await expect(alice.ui.sendButton).toBeDisabled()

    await alice.ui.composer.fill('первая строка')
    await alice.ui.composer.press('Shift+Enter')
    await alice.ui.composer.pressSequentially('вторая строка')
    await alice.ui.composer.press('Enter')

    const bubble = alice.ui.bubbles.last()
    await expect(bubble.locator('.bubble__text')).toHaveText('первая строка\nвторая строка')
    await expect(alice.ui.composer).toHaveValue('')
  })

  test('сообщение, отправленное без сети, можно повторить', async ({ page, openDevice }) => {
    const [alice, bob] = await twoUsers(page, openDevice)
    await alice.ui.startChatWith(bob.user.username)

    await alice.page.route('**/api/chats/*/messages', (route) =>
      route.request().method() === 'POST' ? route.abort('internetdisconnected') : route.continue(),
    )
    await alice.ui.send('потерянное')
    const failed = alice.ui.bubble('потерянное')
    await expect(failed).toContainText('Не отправлено')

    await alice.page.unroute('**/api/chats/*/messages')
    await failed.getByRole('button', { name: 'Повторить' }).click()
    await expect(failed.getByLabel('Отправлено')).toBeVisible()

    await bob.ui.chatRow('Алиса').click()
    await expect(bob.ui.bubble('потерянное')).toHaveCount(1)
  })

  test('поиск не находит себя и подсказывает формат', async ({ page }) => {
    const me = uniqueUser()
    await signUp(page, me)
    const ui = new ChatsPage(page)

    await ui.search.fill(me.username)
    await expect(page.getByText(`Никого не нашли по @${me.username}`)).toBeVisible()

    await ui.search.fill('имя с пробелом')
    await expect(page.getByText('Имя пользователя: латиница, цифры и _')).toBeVisible()
  })

  test('на телефоне чат открывается поверх списка', async ({ page, openDevice, isMobile }) => {
    test.skip(!isMobile, 'только мобильная раскладка')
    const [alice, bob] = await twoUsers(page, openDevice)

    await alice.ui.startChatWith(bob.user.username)
    await expect(alice.ui.chatList).toBeHidden()
    await expect(alice.ui.composer).toBeVisible()

    await alice.ui.send('с телефона')
    await alice.ui.backButton.click()
    await expect(alice.page).toHaveURL('/')
    await expect(alice.ui.chatRow('Боб')).toContainText('с телефона')
    await expect(alice.ui.conversation).toBeHidden()
  })
})
