import { APIRequestContext, Browser, BrowserContext, Page, request, test as base, expect } from '@playwright/test'

// Page smoke for every SPA route in the sweep's route inventory. A page
// "passes" when it renders inside #route-root, stays on its URL (or, when
// unauthenticated on a protected route, redirects to /login) and throws no
// uncaught JS error (blank-screen regressions).
//
// Auth: /api/auth/login is hit once over HTTP; the access/refresh tokens are
// pre-seeded into localStorage exactly the way AuthContext does
// (web/src/auth/AuthContext.tsx: ACCESS_KEY 'auth.access', REFRESH_KEY
// 'auth.refresh', saved JSON-stringified via lib/storage.ts).

const ADMIN_USER = process.env.E2E_ADMIN_USER ?? 'admin'
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD ?? ''
const BASE_URL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8989'

const PROTECTED_PAGES = [
  '/',
  '/search',
  '/settings',
  '/history',
  '/favorites',
  '/playlists',
  '/playlists/999999',
  '/library',
  '/discover',
  '/watchlist',
  '/stats',
  '/local',
  '/downloads',
]

const PUBLIC_PAGES = [
  '/login',
  '/register',
  '/verify-email',
  '/forgot-password',
  '/reset-password',
  // No OAuth params: the callback page must render its error state, not crash.
  '/auth/google/callback',
]

const test = base.extend<{ authedPage: Page }>({
  authedPage: async ({ browser }, use) => {
    const ctx = await newAuthedContext(browser)
    const page = await ctx.newPage()
    await use(page)
    await ctx.close()
  },
})

let api: APIRequestContext

async function loginTokens(): Promise<{ access: string; refresh: string }> {
  const res = await api.post('/api/auth/login', {
    data: { username: ADMIN_USER, password: ADMIN_PASSWORD, remember: false },
  })
  if (res.status() !== 200) {
    throw new Error(`seed login failed: status=${res.status()} body=${await res.text()}`)
  }
  return (await res.json()) as { access: string; refresh: string }
}

// newAuthedContext seeds the SPA session the way AuthContext persists it.
async function newAuthedContext(browser: Browser): Promise<BrowserContext> {
  const { access, refresh } = await loginTokens()
  const ctx = await browser.newContext()
  await ctx.addInitScript(
    ({ access, refresh }) => {
      localStorage.setItem('jackui:auth.access', JSON.stringify(access))
      localStorage.setItem('jackui:auth.refresh', JSON.stringify(refresh))
    },
    { access, refresh },
  )
  return ctx
}

// collectPageErrors attaches an uncaught-JS-error recorder to a page.
function collectPageErrors(page: Page): string[] {
  const errors: string[] = []
  page.on('pageerror', (err) => errors.push(String(err)))
  return errors
}

async function expectPageRendered(page: Page, path: string) {
  const errors = collectPageErrors(page)
  await page.goto(path)
  await expect(page.locator('#route-root')).toBeVisible()
  expect(errors, `uncaught JS errors on ${path}`).toEqual([])
}

test.beforeAll(async () => {
  // Standalone request context: baseURL must be passed explicitly (the
  // config-level use.baseURL does not propagate here).
  api = await request.newContext({ baseURL: BASE_URL })
})

test.afterAll(async () => {
  await api.dispose()
})

test.describe('protected pages (authenticated session)', () => {
  for (const path of PROTECTED_PAGES) {
    test(`renders ${path}`, async ({ authedPage }) => {
      await expectPageRendered(authedPage, path)
      await expect(authedPage).toHaveURL(new RegExp(path.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
    })
  }
})

test.describe('protected pages (anonymous → /login)', () => {
  for (const path of PROTECTED_PAGES) {
    test(`${path} redirects anonymous visitors to login`, async ({ page }) => {
      await page.goto(path)
      await expect(page).toHaveURL(/\/login/)
    })
  }
})

test.describe('public pages', () => {
  for (const path of PUBLIC_PAGES) {
    test(`renders ${path}`, async ({ page }) => {
      await expectPageRendered(page, path)
      await expect(page).toHaveURL(new RegExp(path.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
    })
  }
})

test.describe('login journey', () => {
  test('admin logs in through the UI form', async ({ page }) => {
    await page.goto('/login')
    await page.locator('input[type="text"]').first().fill(ADMIN_USER)
    await page.locator('input[type="password"]').first().fill(ADMIN_PASSWORD)
    await page.locator('button[type="submit"]').first().click()
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('#route-root')).toBeVisible()
  })
})
