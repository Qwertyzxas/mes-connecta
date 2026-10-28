import { HomePage } from '../pages/HomePage'
import { LoginPage } from '../pages/LoginPage'
import { expect, test } from '../support/fixtures'
import { uniqueUser } from '../support/users'

test.describe('Регистрация', () => {
  test('новый пользователь попадает в профиль', async ({ page, registeredUser, homePage }) => {
    await expect(page).toHaveURL('/')
    await expect(homePage.meta).toContainText(`@${registeredUser.username}`)
    await expect(homePage.meta).toContainText(registeredUser.email)
    await expect(homePage.sessions).toHaveCount(1)
    await expect(homePage.currentSession).toHaveCount(1)
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

  test('занятая почта подсвечивается на поле', async ({ registeredUser, homePage, registerPage, loginPage }) => {
    await homePage.logout.click()
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
  })

  test('выход и повторный вход', async ({ page, registeredUser, homePage, loginPage }) => {
    await homePage.logout.click()
    await expect(page).toHaveURL('/login')

    await loginPage.login(registeredUser.email, registeredUser.password)
    await expect(page).toHaveURL('/')
    await expect(homePage.name).toHaveText(registeredUser.displayName)
  })

  test('неверный пароль показывает общую ошибку', async ({ registeredUser, homePage, loginPage }) => {
    await homePage.logout.click()
    await loginPage.login(registeredUser.email, 'wrong password')

    await expect(loginPage.formError).toHaveText('Неверная почта или пароль')
    await expect(loginPage.page).toHaveURL('/login')
  })

  test('сеанс переживает перезагрузку страницы', async ({ page, registeredUser, homePage }) => {
    await page.reload()
    await expect(homePage.name).toHaveText(registeredUser.displayName)
    await page.reload()
    await expect(homePage.name).toHaveText(registeredUser.displayName)
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
  test('завершение сеанса на другом устройстве выкидывает его', async ({ page, registeredUser, homePage, openDevice }) => {
    const laptop = await openDevice()
    const laptopLogin = new LoginPage(laptop)
    const laptopHome = new HomePage(laptop)
    await laptopLogin.goto()
    await laptopLogin.login(registeredUser.email, registeredUser.password)
    await expect(laptopHome.name).toHaveText(registeredUser.displayName)

    await page.reload()
    await expect(homePage.sessions).toHaveCount(2)
    await homePage.revokeOtherSession()
    await expect(homePage.sessions).toHaveCount(1)
    await expect(homePage.currentSession).toHaveCount(1)

    await laptop.reload()
    await expect(laptop).toHaveURL('/login')
  })

  test('выход в одной вкладке разлогинивает остальные', async ({ page, context, registeredUser, homePage }) => {
    const secondTab = await context.newPage()
    const secondHome = new HomePage(secondTab)
    await secondHome.goto()
    await expect(secondHome.name).toHaveText(registeredUser.displayName)

    await homePage.logout.click()
    await expect(page).toHaveURL('/login')
    await expect(secondTab).toHaveURL('/login')
  })
})
