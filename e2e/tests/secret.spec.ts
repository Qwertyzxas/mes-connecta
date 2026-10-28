import type { Page } from '@playwright/test'
import { ChatsPage } from '../pages/ChatsPage'
import { LoginPage } from '../pages/LoginPage'
import { SettingsPage } from '../pages/SettingsPage'
import { expect, signIn, signUp, test } from '../support/fixtures'
import { uniqueUser, type Credentials } from '../support/users'

type Party = { page: Page; ui: ChatsPage; user: Credentials }

const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAIAAAA7ljmRAAAAFUlEQVR4XmP8H1DBAANMcBYDAyoHAELmAc2eigZTAAAAAElFTkSuQmCC',
  'base64',
)

async function people(page: Page, openDevice: () => Promise<Page>): Promise<[Party, Party]> {
  const bobPage = await openDevice()
  const alice = { page, ui: new ChatsPage(page), user: uniqueUser({ displayName: 'Алиса' }) }
  const bob = { page: bobPage, ui: new ChatsPage(bobPage), user: uniqueUser({ displayName: 'Боб' }) }
  await signUp(bob.page, bob.user)
  await signUp(alice.page, alice.user)
  return [alice, bob]
}

async function accepted(page: Page, openDevice: () => Promise<Page>): Promise<[Party, Party]> {
  const [alice, bob] = await people(page, openDevice)
  await alice.ui.createSecretChat(bob.user.username)
  await bob.ui.secretRow('Алиса').click()
  await bob.page.getByRole('region', { name: 'Приглашение в секретный чат' }).getByRole('button', { name: 'Принять' }).click()
  await expect(bob.ui.composer).toBeVisible()
  await expect(alice.ui.composer).toBeVisible()
  return [alice, bob]
}

test.describe('Секретные чаты', () => {
  test('приглашение, принятие и переписка, которую сервер не может прочитать', async ({ page, openDevice }) => {
    const [alice, bob] = await people(page, openDevice)

    await alice.ui.createSecretChat(bob.user.username)
    await expect(alice.page.getByTestId('secret-intro')).toBeVisible()
    await expect(alice.ui.readOnlyNote).toHaveText('Ждём, пока Боб примет приглашение в секретный чат')
    await expect(alice.ui.peerStatus).toHaveText('Ожидаем подтверждения')
    await expect(alice.page.getByRole('button', { name: 'Аудиозвонок' })).toHaveCount(0)

    const invite = bob.ui.secretRow('Алиса')
    await expect(invite).toContainText('Приглашение в секретный чат')
    await invite.click()
    const gate = bob.page.getByRole('region', { name: 'Приглашение в секретный чат' })
    await expect(gate).toContainText('Алиса приглашает вас в секретный чат')
    await gate.getByRole('button', { name: 'Принять' }).click()
    await expect(bob.ui.composer).toBeVisible()
    await expect(alice.ui.composer).toBeVisible()

    const posted = alice.page.waitForRequest((r) => r.method() === 'POST' && /\/api\/chats\/[^/]+\/messages$/.test(r.url()))
    await alice.ui.send('Совершенно секретно')
    const request = await posted
    const sent = request.postDataJSON() as { body: string }
    expect(sent.body).not.toContain('Совершенно')
    expect(sent.body).toMatch(/^[A-Za-z0-9+/]+=*$/)

    await expect(bob.ui.bubble('Совершенно секретно')).toBeVisible()
    await expect(alice.ui.bubble('Совершенно секретно').getByLabel('Прочитано')).toBeVisible()
    await expect(bob.ui.secretRow('Алиса')).toContainText('Совершенно секретно')

    await alice.ui.messageAction('Совершенно секретно', 'Изменить')
    await alice.ui.composer.fill('Совсем секретно')
    await alice.ui.composer.press('Enter')
    await expect(bob.ui.bubble('Совсем секретно')).toContainText('изменено')

    await bob.ui.messageAction('Совсем секретно', 'Ответить')
    await bob.ui.send('Принято')
    await expect(alice.ui.bubble('Принято').locator('.quote')).toContainText('Совсем секретно')

    await bob.page.reload()
    await expect(bob.ui.bubble('Совсем секретно').first()).toBeVisible()
    await expect(bob.ui.bubble('Принято').locator('.quote')).toContainText('Совсем секретно')

    await alice.ui.openInfo()
    await bob.ui.openInfo()
    const aliceKey = alice.page.getByTestId('key-fingerprint')
    await expect(aliceKey).toHaveText(/^([0-9A-F]{4} ){7}[0-9A-F]{4}$/)
    await expect(bob.page.getByTestId('key-fingerprint')).toHaveText((await aliceKey.textContent()) ?? '')
  })

  test('фото шифруется перед загрузкой и открывается у собеседника', async ({ page, openDevice }) => {
    const [alice, bob] = await accepted(page, openDevice)

    const upload = alice.page.waitForRequest((r) => r.method() === 'POST' && r.url().includes('/attachments?'))
    await alice.ui.fileInput.setInputFiles({ name: 'cat.png', mimeType: 'image/png', buffer: PNG })
    await alice.ui.send('кот под грифом')
    const url = new URL((await upload).url())
    expect(url.searchParams.get('kind')).toBe('file')
    expect(url.searchParams.get('name')).toBe('encrypted')

    const photo = bob.ui.bubble('кот под грифом').getByRole('img', { name: 'cat.png' })
    await expect(photo).toBeVisible()
    await expect.poll(() => photo.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(4)
    await expect(photo).toHaveAttribute('src', /^blob:/)
  })

  test('чат живёт только на одном устройстве каждого собеседника', async ({ page, openDevice }) => {
    const [alice, bob] = await people(page, openDevice)
    const bobPhone = new ChatsPage(await openDevice())
    await signIn(bobPhone.page, bob.user)
    const aliceTablet = new ChatsPage(await openDevice())
    await signIn(aliceTablet.page, alice.user)

    await alice.ui.createSecretChat(bob.user.username)
    await expect(bobPhone.secretRow('Алиса')).toBeVisible()
    await expect(bob.ui.secretRow('Алиса')).toBeVisible()

    await bob.ui.secretRow('Алиса').click()
    await bob.page.getByRole('region', { name: 'Приглашение в секретный чат' }).getByRole('button', { name: 'Принять' }).click()
    await expect(bob.ui.composer).toBeVisible()

    await expect(bobPhone.secretRow('Алиса')).toHaveCount(0)
    await aliceTablet.page.reload()
    await expect(aliceTablet.search).toBeVisible()
    await expect(aliceTablet.secretRow('Боб')).toHaveCount(0)
  })

  test('отказ и удаление убирают чат у обоих', async ({ page, openDevice }) => {
    const [alice, bob] = await people(page, openDevice)

    await alice.ui.createSecretChat(bob.user.username)
    await bob.ui.secretRow('Алиса').click()
    await bob.page.getByRole('region', { name: 'Приглашение в секретный чат' }).getByRole('button', { name: 'Отклонить' }).click()
    await expect(bob.ui.secretRow('Алиса')).toHaveCount(0)
    await expect(alice.ui.secretRow('Боб')).toHaveCount(0)

    await alice.ui.createSecretChat(bob.user.username)
    await bob.ui.secretRow('Алиса').click()
    await bob.page.getByRole('region', { name: 'Приглашение в секретный чат' }).getByRole('button', { name: 'Принять' }).click()
    await expect(alice.ui.composer).toBeVisible()
    await alice.ui.send('сейчас удалю')
    await expect(bob.ui.bubble('сейчас удалю')).toBeVisible()

    await alice.ui.openInfo()
    await alice.ui.info.getByRole('button', { name: 'Удалить секретный чат' }).click()
    await alice.ui.info.getByRole('button', { name: 'Удалить секретный чат' }).click()
    await expect(alice.ui.secretRow('Боб')).toHaveCount(0)
    await expect(bob.ui.secretRow('Алиса')).toHaveCount(0)
  })

  test('выход из аккаунта уничтожает секретный чат', async ({ page, openDevice }) => {
    const [alice, bob] = await accepted(page, openDevice)
    await expect(alice.ui.secretRow('Боб')).toHaveCount(1)

    const settings = new SettingsPage(bob.page)
    await bob.page.goto('/settings')
    await settings.logout.click()
    await expect(new LoginPage(bob.page).heading).toBeVisible()

    await expect(alice.ui.secretRow('Боб')).toHaveCount(0)
    await signIn(bob.page, bob.user)
    await expect(bob.ui.secretRow('Алиса')).toHaveCount(0)
  })
})
