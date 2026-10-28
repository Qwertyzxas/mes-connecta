import type { Page } from '@playwright/test'
import { CallPage } from '../pages/CallPage'
import { ChatsPage } from '../pages/ChatsPage'
import { expect, signUp, test } from '../support/fixtures'
import { uniqueUser, type Credentials } from '../support/users'

type Party = { page: Page; ui: ChatsPage; call: CallPage; user: Credentials }

const timer = /^\d+:\d{2}$/

async function pair(page: Page, openDevice: () => Promise<Page>): Promise<[Party, Party]> {
  const bobPage = await openDevice()
  const alice = { page, ui: new ChatsPage(page), call: new CallPage(page), user: uniqueUser({ displayName: 'Алиса' }) }
  const bob = { page: bobPage, ui: new ChatsPage(bobPage), call: new CallPage(bobPage), user: uniqueUser({ displayName: 'Боб' }) }
  await signUp(bob.page, bob.user)
  await signUp(alice.page, alice.user)
  await alice.ui.startChatWith(bob.user.username)
  await alice.ui.send('Созвонимся?')
  await bob.ui.chatRow('Алиса').click()
  await expect(bob.ui.bubble('Созвонимся?')).toBeVisible()
  return [alice, bob]
}

test.describe('Звонки', () => {
  test.describe.configure({ timeout: 60_000 })

  test('аудиозвонок: вызов, ответ, разговор и завершение', async ({ page, openDevice }) => {
    const [alice, bob] = await pair(page, openDevice)

    await alice.call.audioCall.click()
    await expect(alice.call.screen).toBeVisible()
    await expect(alice.call.status).toHaveText('Вызов…')
    await expect(alice.call.audioCall).toBeDisabled()

    await expect(bob.call.incoming).toBeVisible()
    await expect(bob.call.incoming).toHaveAccessibleName('Входящий звонок')
    await expect(bob.call.incoming).toContainText('Алиса')
    await bob.call.accept.click()

    for (const side of [alice, bob]) {
      await expect(side.call.status).toHaveText(timer, { timeout: 15_000 })
    }
    await expect(alice.call.participant('Боб')).toBeVisible({ timeout: 15_000 })
    await expect(bob.call.participant('Алиса')).toBeVisible({ timeout: 15_000 })

    await alice.call.muteMic.click()
    await expect(alice.call.unmuteMic).toBeVisible()
    await expect(bob.call.participant('Алиса')).toContainText('без звука')
    await alice.call.unmuteMic.click()
    await expect(bob.call.participant('Алиса')).not.toContainText('без звука')

    await bob.call.hangUp.click()
    await expect(bob.call.screen).toBeHidden({ timeout: 5_000 })
    await expect(alice.call.screen).toBeHidden({ timeout: 10_000 })

    await expect(alice.call.messages.last()).toHaveText(/^Исходящий звонок · \d+:\d{2}$/)
    await expect(bob.call.messages.last()).toHaveText(/^Входящий звонок · \d+:\d{2}$/)
    await expect(alice.call.audioCall).toBeEnabled()
  })

  test('видеозвонок передаёт камеру собеседнику', async ({ page, openDevice }) => {
    const [alice, bob] = await pair(page, openDevice)

    await alice.call.videoCall.click()
    await expect(bob.call.incoming).toHaveAccessibleName('Входящий видеозвонок')
    await bob.call.accept.click()

    await expect(alice.call.self.locator('video')).toBeVisible({ timeout: 15_000 })
    await expect(bob.call.participant('Алиса').locator('video')).toBeVisible({ timeout: 15_000 })
    await expect(alice.call.participant('Боб').locator('video')).toBeVisible({ timeout: 15_000 })

    await bob.call.cameraOff.click()
    await expect(bob.call.cameraOn).toBeVisible()
    await expect(alice.call.participant('Боб').locator('video')).toBeHidden({ timeout: 10_000 })

    await alice.call.hangUp.click()
    await expect(bob.call.screen).toBeHidden({ timeout: 10_000 })
    await expect(bob.call.messages.last()).toHaveText(/^Входящий видеозвонок · /)
  })

  test('отклонённый звонок', async ({ page, openDevice }) => {
    const [alice, bob] = await pair(page, openDevice)

    await alice.call.videoCall.click()
    await expect(bob.call.incoming).toBeVisible()
    await bob.call.decline.click()
    await expect(bob.call.incoming).toBeHidden()

    await expect(alice.call.status).toHaveText('Абонент отклонил звонок')
    await expect(alice.call.screen).toBeHidden({ timeout: 5_000 })
    await expect(alice.call.messages.last()).toHaveText('Видеозвонок отклонён')
    await expect(bob.call.messages.last()).toHaveText('Отклонённый видеозвонок')
  })

  test('отменённый вызов становится пропущенным', async ({ page, openDevice }) => {
    const [alice, bob] = await pair(page, openDevice)

    await alice.call.audioCall.click()
    await expect(bob.call.incoming).toBeVisible()
    await expect(alice.call.status).toHaveText('Вызов…')
    await alice.call.hangUp.click()

    await expect(bob.call.incoming).toBeHidden()
    await expect(alice.call.messages.last()).toHaveText('Отменённый звонок')
    await expect(bob.call.messages.last()).toHaveText('Пропущенный звонок')
    await expect(bob.ui.chatRow('Алиса')).toContainText('Пропущенный звонок')
  })

  test('входящий звонок приходит после перезагрузки страницы', async ({ page, openDevice }) => {
    const [alice, bob] = await pair(page, openDevice)
    await alice.call.audioCall.click()
    await expect(bob.call.incoming).toBeVisible()

    await bob.page.reload()
    await expect(bob.call.incoming).toBeVisible()
    await bob.call.accept.click()
    await expect(alice.call.status).toHaveText(timer, { timeout: 15_000 })
    await alice.call.hangUp.click()
    await expect(bob.call.screen).toBeHidden({ timeout: 10_000 })
  })

  test('в каналах нет кнопок звонка', async ({ page }) => {
    const ui = new ChatsPage(page)
    const call = new CallPage(page)
    await signUp(page, uniqueUser())
    await ui.createChannel('Объявления', `news_${Math.random().toString(36).slice(2, 10)}`)
    await expect(ui.conversation).toBeVisible()
    await expect(call.audioCall).toHaveCount(0)
    await expect(call.videoCall).toHaveCount(0)
  })
})
