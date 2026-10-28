import { ChatsPage } from '../pages/ChatsPage'
import { expect, signUp, test } from '../support/fixtures'
import { uniqueUser } from '../support/users'

test.describe('Разделы', () => {
  test('нижние вкладки переключают чаты, звонки, контакты и настройки', async ({ page, openDevice }) => {
    const ui = new ChatsPage(page)
    const friend = uniqueUser({ displayName: 'Вера' })
    const friendPage = await openDevice()
    await signUp(friendPage, friend)
    const me = uniqueUser({ displayName: 'Глеб' })
    await signUp(page, me)
    await ui.startChatWith(friend.username)
    await ui.send('привет')
    await expect(ui.bubble('привет').getByLabel('Отправлено')).toBeVisible()

    await page.goto('/')
    const tabs = page.getByRole('navigation', { name: 'Разделы' })
    await tabs.getByRole('link', { name: 'Звонки' }).click()
    await expect(page).toHaveURL('/calls')
    await expect(page.getByText('Звонков пока нет.')).toBeVisible()

    await tabs.getByRole('link', { name: 'Контакты' }).click()
    await expect(page).toHaveURL('/contacts')
    const contacts = page.getByRole('list', { name: 'Контакты' })
    await expect(contacts).toContainText('Вера')
    await contacts.getByRole('button').filter({ hasText: 'Вера' }).click()
    await expect(ui.bubble('привет')).toBeVisible()

    await page.goto('/')
    await tabs.getByRole('link', { name: 'Настройки' }).click()
    await expect(page).toHaveURL('/settings')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Глеб')
    await tabs.getByRole('link', { name: 'Чаты' }).click()
    await expect(ui.chatRow('Вера')).toBeVisible()
  })
})
