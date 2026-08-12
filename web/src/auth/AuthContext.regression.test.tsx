// Regression: depois de um deploy o browser pode guardar refresh tokens que o
// backend não aceita mais. O bootstrap (restore) faz GET /auth/me → 401 →
// interceptor → POST /auth/refresh → 401 → logout() → DELETE /user/incognito
// → 401 → (código antigo) o interceptor refreshes de novo → logout() → …
// RECURSÃO MÚTUA INFINITA — a UI nunca chegava na tela de login (observado ao
// vivo como storm de DELETE incognito + POST refresh a cada ~100ms nos logs do
// proxy). As chamadas de cleanup são marcadas com sessionLifecycle()
// (skipAuthRefresh): seus 401 falham direto pro try/catch do chamador e o
// fluxo completa com exatamente uma requisição de cada etapa.
//
// O mock segue o padrão do projeto (vi.mock de ../api/client — ver
// useJackettSetup.test.tsx): a lógica sob teste — interceptor de 401→refresh,
// logout, restore, shouldAttemptRefresh/sessionLifecycle — é o código REAL do
// AuthContext/incognito; só a camada de rede é simulada por um mini-axios que
// despacha 401 pelo handler de rejeição, como o axios real faz.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import { AuthProvider, useAuth } from './AuthContext'

// ─── Mock do cliente HTTP ───────────────────────────────────────────────────
const mocks = vi.hoisted(() => {
  const calls = new Map<string, number>()
  let requestInterceptor: ((config: unknown) => unknown) | undefined
  let responseInterceptor: ((err: unknown) => unknown) | undefined
  let route: (url: string, config: Record<string, unknown>) => { status: number; data: unknown } = () => ({ status: 404, data: {} })

  // Mesma semântica do sessionLifecycle real (http.ts): marca skipAuthRefresh.
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

  // api é callable (AuthContext faz `api(original)` no retry pós-refresh).
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
    // Browser segura tokens de antes do deploy + incognito ligado (pra logout()
    // exercitar o cleanup server-side — o caminho da recursão).
    localStorage.setItem('jackui:auth.access', JSON.stringify('stale-access'))
    localStorage.setItem('jackui:auth.refresh', JSON.stringify('stale-refresh'))
    localStorage.setItem('jackui:incognito', JSON.stringify(true))

    // /auth/config responde 200 (auth habilitada); TODO o resto 401 (sessão
    // morta — o cenário pós-deploy que disparava o bug).
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

    // Sem storm: exatamente uma chamada de cada etapa do fluxo.
    expect(mocks.calls.get('/auth/config')).toBe(1)
    expect(mocks.calls.get('/auth/me')).toBe(1)
    expect(mocks.calls.get('/auth/refresh')).toBe(1)
    expect(mocks.calls.get('/user/incognito')).toBe(1)
    expect(mocks.calls.get('/auth/logout')).toBe(1)

    // Tokens velhos limpos — estado pronto pra tela de login.
    expect(localStorage.getItem('jackui:auth.access')).toBeNull()
    expect(localStorage.getItem('jackui:auth.refresh')).toBeNull()

    // Sanidade: total de requisições minúsculo (o loop teria explodido isso).
    const total = [...mocks.calls.values()].reduce((a, b) => a + b, 0)
    expect(total).toBeLessThanOrEqual(6)
  })

  it('skips server cleanup when incognito is off but still lands on login', async () => {
    localStorage.setItem('jackui:auth.access', JSON.stringify('stale-access'))
    localStorage.setItem('jackui:auth.refresh', JSON.stringify('stale-refresh'))
    // Sem flag de incognito → logout() não chama DELETE /user/incognito.

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
