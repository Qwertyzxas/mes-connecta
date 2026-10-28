import { ChatsPage } from '../pages/ChatsPage'
import { SettingsPage } from '../pages/SettingsPage'
import { expect, signUp, test } from '../support/fixtures'
import { uniqueUser } from '../support/users'

test.describe('Уведомления', () => {
  test('чат можно перевести в режим без уведомлений', async ({ page, openDevice }) => {
    const bobPage = await openDevice()
    const bob = uniqueUser({ displayName: 'Боб' })
    await signUp(bobPage, bob)
    await signUp(page, uniqueUser({ displayName: 'Алиса' }))
    const ui = new ChatsPage(page)

    await ui.startChatWith(bob.username)
    await ui.send('привет')
    await ui.openInfo()
    const toggle = ui.info.getByLabel('Без уведомлений')
    await expect(toggle).not.toBeChecked()
    await toggle.check()
    await expect(ui.chatRow('Боб').getByLabel('без уведомлений')).toHaveCount(1)

    await page.reload()
    await ui.openInfo()
    await expect(ui.info.getByLabel('Без уведомлений')).toBeChecked()
    await ui.info.getByLabel('Без уведомлений').uncheck()
    await expect(ui.chatRow('Боб').getByLabel('без уведомлений')).toHaveCount(0)
  })

  test('в настройках есть управление уведомлениями', async ({ page }) => {
    await signUp(page, uniqueUser())
    const settings = new SettingsPage(page)
    await settings.goto()
    const panel = page.getByRole('region', { name: 'Уведомления' })
    await expect(panel).toBeVisible()
    await expect(panel.getByRole('button', { name: 'Включить уведомления' })).toBeVisible()
  })
})
