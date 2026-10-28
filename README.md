# Connecta

Go + React (PWA). Этап 1: регистрация и вход по почте и паролю.

## Быстрый старт (всё в Docker)

```bash
make up
```

Создаст `.env` со случайными секретами, сгенерирует `backend/go.sum` и поднимет Postgres, Redis, бэкенд и Caddy.
Откройте https://localhost (Caddy выдаст локальный сертификат). Для продакшна укажите `DOMAIN` в `.env`, и Caddy сам получит сертификат Let's Encrypt.

## Разработка

```bash
make setup        # .env, go mod tidy, npm install
make deps         # Postgres и Redis в Docker
make dev-backend  # API на :8080, миграции применяются при старте
make dev-web      # Vite на http://localhost:5173 с прокси /api
```

## Тесты

```bash
make test-backend  # unit и интеграционные тесты API на реальных Postgres и Redis
make test-e2e      # Playwright: десктоп и мобильный Chrome
```

В CI (GitHub Actions) на каждый пуш собираются бэкенд, фронтенд и Docker-образы, проверяется Caddyfile и гоняются оба набора тестов.

## API

| Метод  | Путь                 | Авторизация    |
|--------|----------------------|----------------|
| POST   | /api/auth/register   | —              |
| POST   | /api/auth/login      | —              |
| POST   | /api/auth/refresh    | cookie         |
| POST   | /api/auth/logout     | cookie         |
| GET    | /api/me              | Bearer         |
| GET    | /api/sessions        | Bearer         |
| DELETE | /api/sessions/{id}   | Bearer         |
| GET    | /api/health          | —              |

Ошибки: `{"error": {"code": "...", "message": "...", "fields": {...}}}`.

## Как устроена авторизация

- Пароли: argon2id (m=19 МиБ, t=2, p=1).
- Access-токен: JWT HS256 на 15 минут, хранится только в памяти вкладки.
- Refresh-токен: 32 случайных байта в HttpOnly + Secure + SameSite=Strict cookie на путь `/api/auth`, в базе лежит только SHA-256.
- Ротация refresh при каждом обновлении. Повторное использование старого токена позже 10 секунд отзывает сеанс. В пределах 10 секунд сервер отвечает 409, а клиент повторяет запрос (гонка вкладок).
- Отзыв сеанса сразу закрывает доступ: id сеанса попадает в Redis-список отозванных на время жизни access-токена.
- Вкладки обновляют токен по очереди через Web Locks API и синхронизируют вход и выход через BroadcastChannel.
- IP клиента берётся только из `X-Real-IP`, который выставляет Caddy, и только при `TRUST_PROXY=true`. Подделать IP заголовками и обойти лимиты нельзя.
- Rate limit в Redis: вход 30/15 мин на IP и 10/15 мин на почту, регистрация 10/час на IP.
