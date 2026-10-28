import { test as base, expect, type Browser, type Page } from '@playwright/test'
import { ChatsPage } from '../pages/ChatsPage'
import { LoginPage } from '../pages/LoginPage'
import { RegisterPage } from '../pages/RegisterPage'
import { SettingsPage } from '../pages/SettingsPage'
import { randomIp, uniqueUser, type Credentials } from './users'

type Fixtures = {
  loginPage: LoginPage
  registerPage: RegisterPage
  chatsPage: ChatsPage
  settingsPage: SettingsPage
  user: Credentials
  registeredUser: Credentials
  openDevice: () => Promise<Page>
}

export async function signUp(page: Page, user: Credentials) {
  const res = await page.request.post('/api/auth/register', {
    data: {
      email: user.email,
      username: user.username,
      display_name: user.displayName,
      password: user.password,
    },
  })
  expect(res.status(), await res.text()).toBe(201)
  await page.goto('/')
  await expect(new ChatsPage(page).search).toBeVisible()
}

export const test = base.extend<Fixtures>({
  extraHTTPHeaders: async ({}, use) => {
    await use({ 'X-Real-IP': randomIp() })
  },

  loginPage: async ({ page }, use) => use(new LoginPage(page)),
  registerPage: async ({ page }, use) => use(new RegisterPage(page)),
  chatsPage: async ({ page }, use) => use(new ChatsPage(page)),
  settingsPage: async ({ page }, use) => use(new SettingsPage(page)),

  user: async ({}, use) => use(uniqueUser()),

  registeredUser: async ({ user, registerPage, chatsPage }, use) => {
    await registerPage.goto()
    await registerPage.register(user)
    await expect(chatsPage.search).toBeVisible()
    await use(user)
  },

  openDevice: async ({ browser, baseURL, isMobile, viewport }, use) => {
    const contexts: Awaited<ReturnType<Browser['newContext']>>[] = []
    await use(async () => {
      const context = await browser.newContext({
        baseURL,
        locale: 'ru-RU',
        serviceWorkers: 'block',
        permissions: ['microphone', 'notifications'],
        isMobile,
        viewport,
        extraHTTPHeaders: { 'X-Real-IP': randomIp() },
      })
      contexts.push(context)
      return context.newPage()
    })
    await Promise.all(contexts.map((c) => c.close()))
  },
})

export { expect }
