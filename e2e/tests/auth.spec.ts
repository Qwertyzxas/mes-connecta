import { LoginPage } from '../pages/LoginPage'
import { SettingsPage } from '../pages/SettingsPage'
import { expect, test } from '../support/fixtures'
import { uniqueUser } from '../support/users'

test.describe('Регистрация', () => {
  test('новый пользователь попадает в пустой список чатов', async ({ page, registeredUser, chatsPage, settingsPage }) => {
    await expect(page).toHaveURL('/')
    await expect(chatsPage.emptyState).toBeVisible()

    await chatsPage.settingsLink.click()
    await expect(page).toHaveURL('/settings')
    await expect(settingsPage.name).toHaveText(registeredUser.displayName)
    await expect(settingsPage.meta).toContainText(`@${registeredUser.username}`)
    await expect(settingsPage.meta).toContainText(registeredUser.email)
    await expect(settingsPage.sessions).toHaveCount(1)
    await expect(settingsPage.currentSession).toHaveCount(1)
  })

  test('клиентская валидация не пускает пустую форму', async ({ registerPage }) => {
    let apiCalls = 0
    registerPage.page.on('request', (req) => {
      if (req.url().includes('/api/auth/register')) apiCalls++
    })

    await registerPage.goto()
    await registerPage.submit.click()

    await expect(registerPage.email).toHaveAccessibleDescription('Некорректный адрес почты')
    await expect(registerPage.username).toHaveAccessibleDescription('3–32 символа: латиница, цифры и _')
    await expect(registerPage.password).toHaveAccessibleDescription('Минимум 8 символов')
    expect(apiCalls).toBe(0)
  })

  test('ошибка поля исчезает, когда его исправляют', async ({ registerPage }) => {
    await registerPage.goto()
    await registerPage.submit.click()
    await expect(registerPage.email).toHaveAttribute('aria-invalid', 'true')

    await registerPage.email.fill('fixed@example.com')
    await expect(registerPage.email).toHaveAttribute('aria-invalid', 'false')
  })

  test('занятая почта подсвечивается на поле', async ({ registeredUser, settingsPage, registerPage, loginPage }) => {
    await settingsPage.goto()
    await settingsPage.logout.click()
    await expect(loginPage.heading).toBeVisible()

    await registerPage.goto()
    await registerPage.register(uniqueUser({ email: registeredUser.email.toUpperCase() }))

    await expect(registerPage.email).toHaveAccessibleDescription('Эта почта уже зарегистрирована')
    await expect(registerPage.page).toHaveURL('/register')
  })
})

test.describe('Вход', () => {
  test('гостя перенаправляет на страницу входа', async ({ page, loginPage }) => {
    await page.goto('/')
    await expect(page).toHaveURL('/login')
    await expect(loginPage.heading).toBeVisible()

    await page.goto('/settings')
    await expect(page).toHaveURL('/login')
  })

  test('выход и повторный вход', async ({ page, registeredUser, settingsPage, loginPage, chatsPage }) => {
    await settingsPage.goto()
    await settingsPage.logout.click()
    await expect(page).toHaveURL('/login')

    await loginPage.login(registeredUser.email, registeredUser.password)
    await expect(page).toHaveURL('/')
    await expect(chatsPage.search).toBeVisible()
  })

  test('неверный пароль показывает общую ошибку', async ({ registeredUser, settingsPage, loginPage }) => {
    await settingsPage.goto()
    await settingsPage.logout.click()
    await loginPage.login(registeredUser.email, 'wrong password')

    await expect(loginPage.formError).toHaveText('Неверная почта или пароль')
    await expect(loginPage.page).toHaveURL('/login')
  })

  test('сеанс переживает перезагрузку страницы', async ({ page, registeredUser, chatsPage, settingsPage }) => {
    await page.reload()
    await expect(chatsPage.search).toBeVisible()
    await settingsPage.goto()
    await page.reload()
    await expect(settingsPage.name).toHaveText(registeredUser.displayName)
  })

  test('пароль можно показать и скрыть', async ({ loginPage }) => {
    await loginPage.goto()
    await loginPage.password.fill('secret-value')
    await expect(loginPage.password).toHaveAttribute('type', 'password')

    await loginPage.page.getByRole('button', { name: 'Показать пароль' }).click()
    await expect(loginPage.password).toHaveAttribute('type', 'text')
    await loginPage.page.getByRole('button', { name: 'Скрыть пароль' }).click()
    await expect(loginPage.password).toHaveAttribute('type', 'password')
  })
})

test.describe('Сеансы', () => {
  test('завершение сеанса на другом устройстве выкидывает его', async ({ registeredUser, settingsPage, openDevice }) => {
    const laptop = await openDevice()
    const laptopLogin = new LoginPage(laptop)
    await laptopLogin.goto()
    await laptopLogin.login(registeredUser.email, registeredUser.password)
    await expect(laptop).toHaveURL('/')

    await settingsPage.goto()
    await expect(settingsPage.sessions).toHaveCount(2)
    await settingsPage.revokeOtherSession()
    await expect(settingsPage.sessions).toHaveCount(1)
    await expect(settingsPage.currentSession).toHaveCount(1)

    await laptop.reload()
    await expect(laptop).toHaveURL('/login')
  })

  test('выход в одной вкладке разлогинивает остальные', async ({ page, context, registeredUser, settingsPage }) => {
    const secondTab = await context.newPage()
    const secondSettings = new SettingsPage(secondTab)
    await secondSettings.goto()
    await expect(secondSettings.name).toHaveText(registeredUser.displayName)

    await settingsPage.goto()
    await settingsPage.logout.click()
    await expect(page).toHaveURL('/login')
    await expect(secondTab).toHaveURL('/login')
  })
})
