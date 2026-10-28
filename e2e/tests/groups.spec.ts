import type { Page } from '@playwright/test'
import { ChatsPage } from '../pages/ChatsPage'
import { expect, signUp, test } from '../support/fixtures'
import { uniqueUser, type Credentials } from '../support/users'

type Party = { page: Page; ui: ChatsPage; user: Credentials }

async function party(page: Page, name: string): Promise<Party> {
  const user = uniqueUser({ displayName: name })
  await signUp(page, user)
  return { page, ui: new ChatsPage(page), user }
}

test.describe('Группы', () => {
  test('создание группы и переписка втроём', async ({ page, openDevice }) => {
    const bob = await party(await openDevice(), 'Боб')
    const carol = await party(await openDevice(), 'Карина')
    const alice = await party(page, 'Алиса')

    await alice.ui.createGroup('Команда', [bob.user.username, carol.user.username])
    await expect(alice.ui.peerName).toHaveText('Команда')
    await expect(alice.ui.peerStatus).toContainText('3 участника')
    await expect(alice.ui.systemMessage('Вы создали «Команда»')).toBeVisible()

    await alice.ui.send('Всем привет')
    const row = bob.ui.chatRow('Команда')
    await expect(row).toContainText('Алиса: Всем привет')
    await expect(row.getByLabel('Непрочитанных: 1')).toBeVisible()

    await row.click()
    await expect(bob.ui.systemMessage('Алиса создал(а) «Команда»')).toBeVisible()
    await expect(bob.ui.bubble('Всем привет').locator('.bubble__author')).toHaveText('Алиса')

    await carol.ui.chatRow('Команда').click()
    await carol.ui.send('Привет от Карины')
    await expect(alice.ui.bubble('Привет от Карины').locator('.bubble__author')).toHaveText('Карина')
    await expect(bob.ui.bubble('Привет от Карины')).toBeVisible()
    await expect(alice.ui.bubble('Всем привет').getByLabel('Прочитано')).toBeVisible()
  })

  test('владелец управляет участниками', async ({ page, openDevice }) => {
    const bob = await party(await openDevice(), 'Боб')
    const carol = await party(await openDevice(), 'Карина')
    const dan = await party(await openDevice(), 'Даня')
    const alice = await party(page, 'Алиса')

    await alice.ui.createGroup('Проект', [bob.user.username, carol.user.username])
    await alice.ui.send('старт')
    await carol.ui.chatRow('Проект').click()
    await expect(carol.ui.bubble('старт')).toBeVisible()

    await alice.ui.openInfo()
    await expect(alice.ui.info.getByTestId('member')).toHaveCount(3)
    await expect(alice.ui.member('Алиса').locator('.role-badge')).toHaveText('владелец')

    await alice.ui.info.getByRole('button', { name: 'Добавить', exact: true }).click()
    await alice.ui.pickUsers(alice.ui.info, [dan.user.username])
    await alice.ui.info.getByRole('button', { name: /^Добавить 1/ }).click()
    await expect(alice.ui.info.getByTestId('member')).toHaveCount(4)
    await expect(dan.ui.chatRow('Проект')).toContainText('Алиса добавил(а) вас')

    await alice.ui.member('Боб').getByRole('button', { name: 'Сделать админом' }).click()
    await expect(alice.ui.member('Боб').locator('.role-badge')).toHaveText('админ')

    await alice.ui.member('Карина').getByRole('button', { name: 'Удалить Карина' }).click()
    await expect(alice.ui.info.getByTestId('member')).toHaveCount(3)
    await expect(carol.page).toHaveURL('/')
    await expect(carol.ui.chatRow('Проект')).toHaveCount(0)

    await alice.ui.info.getByRole('button', { name: 'Закрыть информацию' }).click()
    await expect(alice.ui.systemMessage('Вы удалили Карина')).toBeVisible()
    await expect(alice.ui.systemMessage('Вы добавили Даня')).toBeVisible()
  })

  test('участник выходит из группы', async ({ page, openDevice }) => {
    const bob = await party(await openDevice(), 'Боб')
    const alice = await party(page, 'Алиса')
    await alice.ui.createGroup('Клуб', [bob.user.username])
    await alice.ui.send('добро пожаловать')

    await bob.ui.chatRow('Клуб').click()
    await bob.ui.openInfo()
    await bob.ui.info.getByRole('button', { name: 'Выйти из группы' }).click()
    await bob.ui.info.getByRole('button', { name: 'Выйти из группы' }).click()

    await expect(bob.page).toHaveURL('/')
    await expect(bob.ui.chatRow('Клуб')).toHaveCount(0)
    await expect(alice.ui.systemMessage('Боб вышел(ла) из группы')).toBeVisible()
    await expect(alice.ui.peerStatus).toContainText('1 участник')
  })
})

test.describe('Каналы', () => {
  test('подписка на публичный канал и чтение постов', async ({ page, openDevice }) => {
    const bob = await party(await openDevice(), 'Боб')
    const alice = await party(page, 'Алиса')
    const handle = `news_${Date.now().toString(36)}`

    await alice.ui.createChannel('Новости Connecta', handle)
    await expect(alice.ui.peerStatus).toHaveText('1 подписчик')
    await alice.ui.send('Выпуск 1')

    await bob.ui.search.fill(handle)
    const found = bob.page.getByRole('list', { name: 'Найденные каналы' }).getByRole('button').filter({ hasText: 'Новости Connecta' })
    await expect(found).toContainText('Подписаться')
    await found.click()

    await expect(bob.ui.peerName).toHaveText('Новости Connecta')
    await expect(bob.ui.bubble('Выпуск 1')).toBeVisible()
    await expect(bob.ui.readOnlyNote).toHaveText('В канале публикуют только администраторы')
    await expect(bob.ui.composer).toHaveCount(0)
    await expect(alice.ui.peerStatus).toHaveText('2 подписчика')

    await alice.ui.send('Выпуск 2')
    await expect(bob.ui.bubble('Выпуск 2')).toBeVisible()
    await bob.ui.bubble('Выпуск 2').getByRole('button', { name: 'Действия с сообщением' }).click()
    await expect(bob.ui.menuItems()).toHaveText(['Копировать'])
  })
})
