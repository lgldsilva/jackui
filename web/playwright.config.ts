import { defineConfig, devices } from '@playwright/test'

// SPA page-smoke config. The stack under test is external (never started by
// the suite): point E2E_BASE_URL at it, e.g.
//   E2E_BASE_URL=http://127.0.0.1:8989 npx playwright test
// `make e2e-smoke` (scripts/e2e-smoke.sh) builds and boots a disposable one.
const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8989'

export default defineConfig({
  testDir: './e2e',
  // Playwright wipes its outputDir at startup; scripts/e2e-smoke.sh keeps the
  // server log and the smoke binary one level up, in test-results/.
  outputDir: './test-results/playwright',
  timeout: 30_000,
  expect: { timeout: 7_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    ...devices['Desktop Chrome'],
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
})
