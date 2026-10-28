import { test as base, expect, type Browser, type Page } from '@playwright/test'
import { HomePage } from '../pages/HomePage'
import { LoginPage } from '../pages/LoginPage'
import { RegisterPage } from '../pages/RegisterPage'
import { randomIp, uniqueUser, type Credentials } from './users'

type Fixtures = {
  loginPage: LoginPage
  registerPage: RegisterPage
  homePage: HomePage
  user: Credentials
  registeredUser: Credentials
  openDevice: () => Promise<Page>
}

export const test = base.extend<Fixtures>({
  extraHTTPHeaders: async ({}, use) => {
    await use({ 'X-Real-IP': randomIp() })
  },

  loginPage: async ({ page }, use) => use(new LoginPage(page)),
  registerPage: async ({ page }, use) => use(new RegisterPage(page)),
  homePage: async ({ page }, use) => use(new HomePage(page)),

  user: async ({}, use) => use(uniqueUser()),

  registeredUser: async ({ user, registerPage, homePage }, use) => {
    await registerPage.goto()
    await registerPage.register(user)
    await expect(homePage.name).toHaveText(user.displayName)
    await use(user)
  },

  openDevice: async ({ browser, baseURL }, use) => {
    const contexts: Awaited<ReturnType<Browser['newContext']>>[] = []
    await use(async () => {
      const context = await browser.newContext({
        baseURL,
        locale: 'ru-RU',
        serviceWorkers: 'block',
        extraHTTPHeaders: { 'X-Real-IP': randomIp() },
      })
      contexts.push(context)
      return context.newPage()
    })
    await Promise.all(contexts.map((c) => c.close()))
  },
})

export { expect }
