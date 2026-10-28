import type { Page } from '@playwright/test'
import { ChatsPage } from '../pages/ChatsPage'
import { expect, signUp, test } from '../support/fixtures'
import { uniqueUser, type Credentials } from '../support/users'

type Party = { page: Page; ui: ChatsPage; user: Credentials }

async function openedChat(page: Page, openDevice: () => Promise<Page>, firstMessage: string): Promise<[Party, Party]> {
  const alice = { page, ui: new ChatsPage(page), user: uniqueUser({ displayName: 'Алиса' }) }
  const bobPage = await openDevice()
  const bob = { page: bobPage, ui: new ChatsPage(bobPage), user: uniqueUser({ displayName: 'Боб' }) }
  await signUp(bob.page, bob.user)
  await signUp(alice.page, alice.user)
  await alice.ui.startChatWith(bob.user.username)
  await alice.ui.send(firstMessage)
  await expect(alice.ui.bubble(firstMessage).getByLabel('Отправлено')).toBeVisible()
  await bob.ui.chatRow('Алиса').click()
  await expect(bob.ui.bubble(firstMessage)).toBeVisible()
  return [alice, bob]
}

test.describe('Действия с сообщениями', () => {
  test('ответ цитирует исходное сообщение у обоих', async ({ page, openDevice }) => {
    const [alice, bob] = await openedChat(page, openDevice, 'Во сколько встречаемся?')

    await bob.ui.messageAction('Во сколько встречаемся?', 'Ответить')
    await expect(bob.ui.composerContext).toContainText('В ответ Алиса')
    await expect(bob.ui.composerContext).toContainText('Во сколько встречаемся?')
    await bob.ui.send('В семь')
    await expect(bob.ui.composerContext).toBeHidden()

    for (const ui of [bob.ui, alice.ui]) {
      const quote = ui.bubble('В семь').locator('.quote')
      await expect(quote).toContainText('Во сколько встречаемся?')
    }
    await expect(alice.ui.bubble('В семь').locator('.quote__author')).toHaveText('Вы')
    await expect(bob.ui.bubble('В семь').locator('.quote__author')).toHaveText('Алиса')
  })

  test('ответ можно отменить клавишей Esc', async ({ page, openDevice }) => {
    const [alice] = await openedChat(page, openDevice, 'привет')
    await alice.ui.messageAction('привет', 'Ответить')
    await expect(alice.ui.composerContext).toBeVisible()
    await alice.ui.composer.press('Escape')
    await expect(alice.ui.composerContext).toBeHidden()
  })

  test('правка видна собеседнику с пометкой «изменено»', async ({ page, openDevice }) => {
    const [alice, bob] = await openedChat(page, openDevice, 'встречаемя в семь')

    await alice.ui.messageAction('встречаемя в семь', 'Изменить')
    await expect(alice.ui.composerContext).toContainText('Редактирование')
    await expect(alice.ui.composer).toHaveValue('встречаемя в семь')
    await alice.ui.composer.fill('встречаемся в семь')
    await alice.ui.composer.press('Enter')

    for (const ui of [alice.ui, bob.ui]) {
      const bubble = ui.bubble('встречаемся в семь')
      await expect(bubble).toBeVisible()
      await expect(bubble.locator('.bubble__edited')).toHaveText('изменено')
    }
    await expect(alice.ui.bubbles).toHaveCount(1)
    await expect(alice.ui.composer).toHaveValue('')
  })

  test('стрелка вверх открывает правку последнего своего сообщения', async ({ page, openDevice }) => {
    const [alice] = await openedChat(page, openDevice, 'первое')
    await alice.ui.send('второе')
    await expect(alice.ui.bubble('второе').getByLabel('Отправлено')).toBeVisible()

    await alice.ui.composer.press('ArrowUp')
    await expect(alice.ui.composer).toHaveValue('второе')
    await alice.ui.composer.press('Escape')
    await expect(alice.ui.composer).toHaveValue('')
  })

  test('удаление у всех оставляет след в переписке', async ({ page, openDevice }) => {
    const [alice, bob] = await openedChat(page, openDevice, 'случайно отправил пароль')

    await alice.ui.messageAction('случайно отправил пароль', 'Удалить')
    await alice.page.getByRole('dialog', { name: 'Удаление сообщения' }).getByRole('button', { name: 'Удалить' }).click()

    for (const ui of [alice.ui, bob.ui]) {
      await expect(ui.bubbles).toHaveCount(1)
      await expect(ui.bubbles.first()).toContainText('Сообщение удалено')
      await expect(ui.log).not.toContainText('пароль')
    }
    await expect(bob.ui.chatRow('Алиса')).toContainText('Сообщение удалено')

    await bob.page.reload()
    await expect(bob.ui.bubbles.first()).toContainText('Сообщение удалено')
  })

  test('чужое сообщение можно только процитировать и скопировать', async ({ page, openDevice }) => {
    const [, bob] = await openedChat(page, openDevice, 'моё сообщение')
    await bob.ui.bubble('моё сообщение').getByRole('button', { name: 'Действия с сообщением' }).click()
    await expect(bob.ui.menuItems()).toHaveText(['Ответить', 'Копировать'])
  })

  test('правка, сделанная пока собеседник был офлайн, подтягивается после переподключения', async ({ page, openDevice }) => {
    const [alice, bob] = await openedChat(page, openDevice, 'черновик')

    await bob.page.context().setOffline(true)
    await alice.ui.messageAction('черновик', 'Изменить')
    await alice.ui.composer.fill('финальная версия')
    await alice.ui.composer.press('Enter')
    await expect(alice.ui.bubble('финальная версия')).toBeVisible()

    await bob.page.context().setOffline(false)
    await expect(bob.ui.bubble('финальная версия')).toBeVisible({ timeout: 20_000 })
    await expect(bob.ui.bubbles).toHaveCount(1)
  })
})
