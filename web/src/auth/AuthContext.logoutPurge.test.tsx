// Logout purge: clearTokens() must sweep user-scoped UI keys (saved searches,
// bootRoute, library/download filters) so the next browser user does not
// inherit the previous session's view state — while device/shared settings
// (media mode preference, locale) survive.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import { AuthProvider, useAuth, purgeUserScopedUiKeys } from './AuthContext'
import { save } from '../lib/storage'

// ── direct: the purge itself ────────────────────────────────────────────────
describe('purgeUserScopedUiKeys', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => localStorage.clear())

  it('removes exactly the user-scoped keys and keeps settings', () => {
    save('pinnedSearches', ['ubuntu'])
    save('lastRoute', '/library')
    save('library.filter', 'recent')
    save('downloads.completedFilter', 'done')
    // Device/shared settings that must survive the sweep.
    save('mediaModePref', 'ask')
    localStorage.setItem('jackui_language', '"pt"')
    localStorage.setItem('jackui:unrelated.future.key', '"keep"')

    purgeUserScopedUiKeys()

    expect(localStorage.getItem('jackui:pinnedSearches')).toBeNull()
    expect(localStorage.getItem('jackui:lastRoute')).toBeNull()
    expect(localStorage.getItem('jackui:library.filter')).toBeNull()
    expect(localStorage.getItem('jackui:downloads.completedFilter')).toBeNull()
    // Settings untouched.
    expect(localStorage.getItem('jackui:mediaModePref')).toBe('"ask"')
    expect(localStorage.getItem('jackui_language')).toBe('"pt"')
    expect(localStorage.getItem('jackui:unrelated.future.key')).toBe('"keep"')
  })
})

// ── integration: the purge runs on logout ───────────────────────────────────
const mocks = vi.hoisted(() => {
  let requestInterceptor: ((config: unknown) => unknown) | undefined
  let route: (url: string) => { status: number; data: unknown } = () => ({ status: 404, data: {} })

  function dispatch(url: string, method: string, config?: Record<string, unknown>, data?: unknown): Promise<unknown> {
    const cfg = { url, method, headers: {}, data, ...config }
    if (requestInterceptor) requestInterceptor(cfg)
    const res = route(url)
    if (res.status >= 200 && res.status < 300) return Promise.resolve({ data: res.data })
    const err = new Error(`HTTP ${res.status}`) as Error & { response?: unknown }
    err.response = { status: res.status, data: res.data }
    return Promise.reject(err)
  }

  const apiMock = Object.assign(
    (config: Record<string, unknown>) => dispatch(String(config.url ?? ''), String(config.method ?? 'get'), config),
    {
      interceptors: {
        request: { use: (fn: (c: unknown) => unknown) => { requestInterceptor = fn }, eject: () => {} },
        response: { use: () => {}, eject: () => {} },
      },
      get: (url: string, config?: Record<string, unknown>) => dispatch(url, 'get', config),
      post: (url: string, data?: unknown, config?: Record<string, unknown>) => dispatch(url, 'post', config, data),
      delete: (url: string, config?: Record<string, unknown>) => dispatch(url, 'delete', config),
    },
  )

  const sessionLifecycle = (config: Record<string, unknown> = {}) => ({ ...config, skipAuthRefresh: true })

  return { apiMock, sessionLifecycle, setRoute: (r: typeof route) => { route = r } }
})

vi.mock('../api/client', () => ({
  default: mocks.apiMock,
  api: mocks.apiMock,
  sessionLifecycle: mocks.sessionLifecycle,
  clearMediaToken: () => {},
  passkeyAuthenticate: vi.fn(),
}))

function Probe() {
  const { user, loading, login, logout } = useAuth()
  return (
    <div>
      <span>{loading ? 'loading…' : user ? user.username : 'anon'}</span>
      <button onClick={() => void login('alice', 'pw', false)}>login</button>
      <button onClick={() => void logout()}>logout</button>
    </div>
  )
}

describe('logout sweeps user-scoped UI keys', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => {
    cleanup()
    localStorage.clear()
    mocks.setRoute(() => ({ status: 404, data: {} }))
  })

  it('logout() clears the user keys but leaves settings alone', async () => {
    const userEvent = (await import('@testing-library/user-event')).default
    const user = userEvent.setup()
    mocks.setRoute((url) => {
      if (url === '/auth/config') return { status: 200, data: { enabled: true } }
      if (url === '/auth/login') {
        return {
          status: 200,
          data: {
            access: 'a', refresh: 'r',
            user: { id: 1, username: 'alice', role: 'user', createdAt: '2026-01-01' },
          },
        }
      }
      if (url === '/auth/logout') return { status: 200, data: {} }
      return { status: 404, data: {} }
    })

    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    )
    await waitFor(() => expect(screen.getByText('anon')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'login' }))
    await waitFor(() => expect(screen.getByText('alice')).toBeInTheDocument())

    // User-scoped UI state accumulated during the session…
    save('pinnedSearches', ['arch'])
    save('lastRoute', '/history')
    save('library.filter', 'movies')
    save('downloads.completedFilter', 'failed')
    // …and a device setting that must survive.
    save('mediaModePref', 'audio')

    await user.click(screen.getByRole('button', { name: 'logout' }))
    await waitFor(() => expect(screen.getByText('anon')).toBeInTheDocument())

    expect(localStorage.getItem('jackui:pinnedSearches')).toBeNull()
    expect(localStorage.getItem('jackui:lastRoute')).toBeNull()
    expect(localStorage.getItem('jackui:library.filter')).toBeNull()
    expect(localStorage.getItem('jackui:downloads.completedFilter')).toBeNull()
    expect(localStorage.getItem('jackui:mediaModePref')).toBe('"audio"')
  })
})
