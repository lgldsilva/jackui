// Regression: after a deploy the browser may hold refresh tokens the
// backend no longer accepts. The bootstrap (restore) does GET /auth/me → 401 →
// interceptor → POST /auth/refresh → 401 → logout() → DELETE /user/incognito
// → 401 → (old code) the interceptor refreshes again → logout() → …
// INFINITE MUTUAL RECURSION — the UI never reached the login screen (observed
// live as a storm of DELETE incognito + POST refresh every ~100ms in the
// proxy logs). The cleanup calls are marked with sessionLifecycle()
// (skipAuthRefresh): their 401s fail straight into the caller's try/catch and the
// flow completes with exactly one request per step.
//
// The mock follows the project pattern (vi.mock of ../api/client — see
// useJackettSetup.test.tsx): the logic under test — 401→refresh interceptor,
// logout, restore, shouldAttemptRefresh/sessionLifecycle — is the REAL
// AuthContext/incognito code; only the network layer is simulated by a mini-axios that
// dispatches 401 through the rejection handler, like real axios does.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import { AuthProvider, useAuth } from './AuthContext'

// ─── HTTP client mock ───────────────────────────────────────────────────────
const mocks = vi.hoisted(() => {
  const calls = new Map<string, number>()
  let requestInterceptor: ((config: unknown) => unknown) | undefined
  let responseInterceptor: ((err: unknown) => unknown) | undefined
  let route: (url: string, config: Record<string, unknown>) => { status: number; data: unknown } = () => ({ status: 404, data: {} })

  // Same semantics as the real sessionLifecycle (http.ts): marks skipAuthRefresh.
  const sessionLifecycle = (config: Record<string, unknown> = {}) => ({ ...config, skipAuthRefresh: true })

  function dispatch(url: string, method: string, config?: Record<string, unknown>, data?: unknown): Promise<unknown> {
    calls.set(url, (calls.get(url) ?? 0) + 1)
    const cfg = { url, method, headers: {}, data, ...config }
    if (requestInterceptor) requestInterceptor(cfg)
    const res = route(url, cfg)
    if (res.status >= 200 && res.status < 300) return Promise.resolve({ data: res.data })
    const err = new Error(`HTTP ${res.status}`) as Error & { config?: unknown; response?: unknown }
    err.config = cfg
    err.response = { status: res.status, data: res.data }
    if (responseInterceptor) {
      try {
        const out = responseInterceptor(err)
        return out instanceof Promise ? out : Promise.resolve(out)
      } catch (e) {
        return Promise.reject(e)
      }
    }
    return Promise.reject(err)
  }

  // api is callable (AuthContext does `api(original)` on the post-refresh retry).
  const apiMock = Object.assign(
    (config: Record<string, unknown>) => dispatch(String(config.url ?? ''), String(config.method ?? 'get'), config),
    {
      interceptors: {
        request: {
          use: (fn: (c: unknown) => unknown) => { requestInterceptor = fn },
          eject: () => {},
        },
        response: {
          use: (_ok: (r: unknown) => unknown, err: (e: unknown) => unknown) => { responseInterceptor = err },
          eject: () => {},
        },
      },
      get: (url: string, config?: Record<string, unknown>) => dispatch(url, 'get', config),
      post: (url: string, data?: unknown, config?: Record<string, unknown>) => dispatch(url, 'post', config, data),
      delete: (url: string, config?: Record<string, unknown>) => dispatch(url, 'delete', config),
    },
  )

  return {
    calls,
    apiMock,
    sessionLifecycle,
    setRoute: (r: typeof route) => { route = r },
  }
})

vi.mock('../api/client', () => ({
  default: mocks.apiMock,
  api: mocks.apiMock,
  sessionLifecycle: mocks.sessionLifecycle,
  clearMediaToken: () => {},
  passkeyAuthenticate: vi.fn(),
}))

function Probe() {
  const { user, loading } = useAuth()
  return <div>{loading ? 'loading…' : user ? user.username : 'anon'}</div>
}

describe('stale session bootstrap (no refresh-token recursion)', () => {
  afterEach(() => {
    cleanup()
    localStorage.clear()
    mocks.calls.clear()
    mocks.setRoute(() => ({ status: 404, data: {} }))
  })

  it('completes with a bounded number of requests and lands on the login state', async () => {
    // Browser holds pre-deploy tokens + incognito on (so logout()
    // exercises the server-side cleanup — the recursion path).
    localStorage.setItem('jackui:auth.access', JSON.stringify('stale-access'))
    localStorage.setItem('jackui:auth.refresh', JSON.stringify('stale-refresh'))
    localStorage.setItem('jackui:incognito', JSON.stringify(true))

    // /auth/config answers 200 (auth enabled); EVERYTHING else 401 (dead
    // session — the post-deploy scenario that triggered the bug).
    mocks.setRoute((url) =>
      url === '/auth/config'
        ? { status: 200, data: { enabled: true } }
        : { status: 401, data: { error: 'unauthorized' } },
    )

    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    )

    await waitFor(() => expect(screen.getByText('anon')).toBeInTheDocument())

    // No storm: exactly one call per flow step.
    expect(mocks.calls.get('/auth/config')).toBe(1)
    expect(mocks.calls.get('/auth/me')).toBe(1)
    expect(mocks.calls.get('/auth/refresh')).toBe(1)
    expect(mocks.calls.get('/user/incognito')).toBe(1)
    expect(mocks.calls.get('/auth/logout')).toBe(1)

    // Old tokens cleaned — state ready for the login screen.
    expect(localStorage.getItem('jackui:auth.access')).toBeNull()
    expect(localStorage.getItem('jackui:auth.refresh')).toBeNull()

    // Sanity: tiny total request count (the loop would have exploded this).
    const total = [...mocks.calls.values()].reduce((a, b) => a + b, 0)
    expect(total).toBeLessThanOrEqual(6)
  })

  it('skips server cleanup when incognito is off but still lands on login', async () => {
    localStorage.setItem('jackui:auth.access', JSON.stringify('stale-access'))
    localStorage.setItem('jackui:auth.refresh', JSON.stringify('stale-refresh'))
    // Without the incognito flag → logout() doesn't call DELETE /user/incognito.

    mocks.setRoute((url) =>
      url === '/auth/config'
        ? { status: 200, data: { enabled: true } }
        : { status: 401, data: { error: 'unauthorized' } },
    )

    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    )

    await waitFor(() => expect(screen.getByText('anon')).toBeInTheDocument())

    expect(mocks.calls.get('/user/incognito')).toBeUndefined()
    expect(mocks.calls.get('/auth/refresh')).toBe(1)
    expect(mocks.calls.get('/auth/logout')).toBe(1)
    expect(localStorage.getItem('jackui:auth.refresh')).toBeNull()
  })
})
