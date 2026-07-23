import { defineConfig, devices } from '@playwright/test'

const httpPort = Number.parseInt(process.env.HOOMAIL_E2E_HTTP_PORT ?? '33100', 10)
const smtpPort = Number.parseInt(process.env.HOOMAIL_E2E_SMTP_PORT ?? '33125', 10)
const pop3Port = Number.parseInt(process.env.HOOMAIL_E2E_POP3_PORT ?? String(smtpPort + 1), 10)

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: [['html', { outputFolder: 'playwright-report', open: 'never' }]],
  outputDir: 'test-results',
  use: {
    baseURL: `http://127.0.0.1:${httpPort}`,
    locale: 'en-US',
    timezoneId: 'UTC',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'bun e2e/run-server.ts',
    url: `http://127.0.0.1:${httpPort}/api/mailboxes`,
    timeout: 120_000,
    reuseExistingServer: false,
    gracefulShutdown: { signal: 'SIGTERM', timeout: 10_000 },
    env: {
      HOOMAIL_E2E_HTTP_PORT: String(httpPort),
      HOOMAIL_E2E_SMTP_PORT: String(smtpPort),
      HOOMAIL_E2E_POP3_PORT: String(pop3Port),
    },
  },
})
