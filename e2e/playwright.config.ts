import { defineConfig, devices } from '@playwright/test'

const isCI = Boolean(process.env.CI)

const backendEnv = {
  HTTP_ADDR: ':8080',
  DATABASE_URL: process.env.DATABASE_URL ?? '',
  REDIS_URL: process.env.REDIS_URL ?? 'redis://localhost:6379/1',
  JWT_SECRET: process.env.JWT_SECRET ?? 'e2e-secret-e2e-secret-e2e-secret-e2e',
  COOKIE_SECURE: 'false',
  TRUST_PROXY: 'true',
  S3_ENDPOINT: process.env.S3_ENDPOINT ?? 'localhost:9000',
  S3_ACCESS_KEY: process.env.S3_ACCESS_KEY ?? 'connecta',
  S3_SECRET_KEY: process.env.S3_SECRET_KEY ?? 'connecta-secret',
  S3_BUCKET: 'connecta-e2e',
}

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: isCI,
  retries: isCI ? 1 : 0,
  workers: isCI ? 2 : undefined,
  reporter: isCI
    ? [['github'], ['html', { open: 'never' }], ['json', { outputFile: 'results.json' }]]
    : [['list'], ['html', { open: 'never' }]],
  timeout: 30_000,
  expect: { timeout: 7_000 },
  use: {
    baseURL: 'http://localhost:4173',
    locale: 'ru-RU',
    serviceWorkers: 'block',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    permissions: ['microphone', 'notifications'],
    launchOptions: {
      args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'],
    },
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['Pixel 7'] } },
  ],
  webServer: [
    {
      command: 'go run ./cmd/server',
      cwd: '../backend',
      url: 'http://localhost:8080/api/health',
      env: backendEnv,
      timeout: 180_000,
      reuseExistingServer: !isCI,
      stdout: 'ignore',
      stderr: 'pipe',
    },
    {
      command: 'npm run build && npm run preview -- --port 4173 --strictPort',
      cwd: '../web',
      url: 'http://localhost:4173',
      timeout: 180_000,
      reuseExistingServer: !isCI,
    },
  ],
})
