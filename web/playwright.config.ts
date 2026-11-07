import { defineConfig, devices } from '@playwright/test'

// SPA page-smoke config. The stack under test is external (never started by
// the suite): point E2E_BASE_URL at it, e.g.
//   E2E_BASE_URL=http://127.0.0.1:8989 npx playwright test
const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8989'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 7_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: {
    baseURL,
    ...devices['Desktop Chrome'],
  },
})
