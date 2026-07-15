import { createContext, useContext, useState, useEffect, useCallback, useMemo, ReactNode } from 'react'
import api, { passkeyAuthenticate, clearMediaToken } from '../api/client'
import { load, save, remove } from '../lib/storage'
import { isIncognito, resetIncognitoFlag, clearIncognitoData } from '../lib/incognito'
import { setRevealHidden } from '../lib/reveal'
import { clearPlaylistSnapshot } from '../components/player/playlistSnapshot'
import { REFRESH_MAX_ATTEMPTS, httpStatusOf, isAuthRejection, refreshBackoffMs } from './refreshPolicy'

export type Role = 'admin' | 'user' | 'guest'

export type AuthUser = {
  readonly id: number
  readonly username: string
  readonly email?: string
  readonly role: Role
  readonly status?: 'active' | 'pending' | 'disabled'
  readonly emailVerified?: boolean
  readonly mfaEnabled?: boolean
  readonly createdAt: string
}

type TokenBundle = {
  readonly access: string
  readonly refresh: string
  readonly expiresAt: string
  readonly user: AuthUser
}

type AuthContextValue = {
  readonly user: AuthUser | null
  readonly loading: boolean
  readonly enabled: boolean // server has auth turned on
  readonly isAdmin: boolean
  readonly isGuest: boolean
  readonly isAuthenticated: boolean
  readonly login: (username: string, password: string, remember: boolean, totp?: string) => Promise<void>
  readonly loginWithPasskey: (username: string, remember: boolean) => Promise<void>
  readonly logout: () => Promise<void>
  readonly refresh: () => Promise<void>
}

const Ctx = createContext<AuthContextValue | null>(null)
const ACCESS_KEY = 'auth.access'
const REFRESH_KEY = 'auth.refresh'

export function AuthProvider({ children }: { readonly children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null)
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(true) // assume enabled until config arrives

  // Inject Authorization header on every API request
  useEffect(() => {
    const reqInt = api.interceptors.request.use((config) => {
      const access = load<string>(ACCESS_KEY, '')
      if (access) config.headers.Authorization = `Bearer ${access}`
      return config
    })
    return () => api.interceptors.request.eject(reqInt)
  }, [])

  // Auto-refresh on 401, but only once per request to avoid loops
  useEffect(() => {
    const respInt = api.interceptors.response.use(
      (res) => res,
      async (error) => {
        const original = error.config
        // Don't try to refresh when the refresh call ITSELF 401s — that would
        // recurse into another refresh (request storm). Let it fall through to
        // logout instead.
        const isRefreshCall = typeof original?.url === 'string' && original.url.includes('/auth/refresh')
        if (error.response?.status === 401 && !original._retry && !isRefreshCall) {
          original._retry = true
          try {
            await refreshTokens()
            const access = load<string>(ACCESS_KEY, '')
            original.headers.Authorization = `Bearer ${access}`
            return api(original)
          } catch (refreshErr) {
            // Only log out on a GENUINE auth rejection. A transient refresh
            // failure (backend down during a deploy, network blip) must NOT drop
            // the session — that was forcing a re-login on every deploy.
            if (refreshAuthFailed(refreshErr)) await logout()
          }
        }
        throw error
      },
    )
    return () => api.interceptors.response.eject(respInt)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // On mount: check if auth is enabled, then try to restore session
  useEffect(() => {
    const restore = async () => {
      try {
        const { data: cfg } = await api.get<{ enabled: boolean }>('/auth/config')
        setEnabled(cfg.enabled)
        if (!cfg.enabled) {
          setLoading(false)
          return
        }
      } catch {
        setEnabled(false)
        setLoading(false)
        return
      }

      const access = load<string>(ACCESS_KEY, '')
      if (access) {
        try {
          const { data } = await api.get<AuthUser>('/auth/me')
          setUser(data)
        } catch {
          try {
            await refreshTokens()
            const { data } = await api.get<AuthUser>('/auth/me')
            setUser(data)
          } catch (e) {
            // Only drop the session on a genuine auth rejection. On a transient
            // failure (backend mid-deploy) keep the tokens so a retry/reload
            // recovers instead of forcing a re-login.
            if (refreshAuthFailed(e)) clearTokens()
          }
        }
      }
      setLoading(false)
    }
    restore()
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const login = useCallback(async (username: string, password: string, remember: boolean, totp?: string) => {
    const { data } = await api.post<TokenBundle>('/auth/login', { username, password, remember, totp: totp || '' })
    save(ACCESS_KEY, data.access)
    save(REFRESH_KEY, data.refresh)
    setUser(data.user)
  }, [])

  const loginWithPasskey = useCallback(async (username: string, remember: boolean) => {
    const data = await passkeyAuthenticate(username, remember)
    save(ACCESS_KEY, data.access)
    save(REFRESH_KEY, data.refresh)
    setUser(data.user)
  }, [])

  const logout = useCallback(async () => {
    // Purge server-side incognito rows while the access token is still valid
    // (belt-and-suspenders with Logout's Optional claims path).
    if (isIncognito()) {
      try { await clearIncognitoData() } catch { /* ignore */ }
    }
    const refresh = load<string>(REFRESH_KEY, '')
    try { if (refresh) await api.post('/auth/logout', { refresh }) } catch { /* ignore */ }
    clearTokens()
    setUser(null)
  }, [])

  const refresh = useCallback(async () => { await refreshTokens() }, [])

  const ctx = useMemo(() => ({
    user,
    loading,
    enabled,
    isAdmin: !enabled || user?.role === 'admin', // when auth disabled, treat as admin
    isGuest: enabled && user?.role === 'guest',
    isAuthenticated: !enabled || user !== null,
    login,
    loginWithPasskey,
    logout,
    refresh,
  }), [user, loading, enabled, login, loginWithPasskey, logout, refresh])

  return (
    <Ctx.Provider value={ctx}>
      {children}
    </Ctx.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useAuth must be used inside <AuthProvider>')
  return ctx
}

// ─── helpers (also usable outside React) ───────────────────────────────────

export function getAccessToken(): string {
  return load<string>(ACCESS_KEY, '')
}

export function getRefreshToken(): string {
  return load<string>(REFRESH_KEY, '')
}

// Single-flight refresh: concurrent 401s must share ONE refresh call. The
// backend ROTATES the refresh token on every /auth/refresh (consumes the old,
// issues a new one), so if N parallel requests each refresh, the 2nd+ send an
// already-consumed token → 401 → spurious logout. Sharing one in-flight promise
// makes all callers await the same rotation.
let refreshInFlight: Promise<void> | null = null

// RefreshError distinguishes WHY a refresh failed. authFailed=true means the
// session is genuinely invalid (no refresh token, or the server returned
// 401/403) → log out. authFailed=false means a TRANSIENT failure (backend down
// during a deploy, network blip, 5xx) survived all retries → keep the session;
// the next request recovers once the backend is back.
class RefreshError extends Error {
  constructor(readonly authFailed: boolean) {
    super(authFailed ? 'refresh rejected' : 'refresh failed transiently')
  }
}

function refreshAuthFailed(e: unknown): boolean {
  return e instanceof RefreshError && e.authFailed
}

async function refreshTokens(): Promise<void> {
  if (refreshInFlight) return refreshInFlight
  refreshInFlight = doRefresh().finally(() => { refreshInFlight = null })
  return refreshInFlight
}

async function doRefresh(): Promise<void> {
  const refresh = load<string>(REFRESH_KEY, '')
  if (!refresh) throw new RefreshError(true)
  // Retry transient failures: the backend's rotation grace window makes a
  // re-sent (possibly already-consumed) token reissue a fresh pair instead of
  // revoking, so retrying across a deploy's restart window is safe.
  for (let attempt = 0; attempt < REFRESH_MAX_ATTEMPTS; attempt++) {
    try {
      const { data } = await api.post<TokenBundle>('/auth/refresh', { refresh })
      save(ACCESS_KEY, data.access)
      save(REFRESH_KEY, data.refresh)
      return
    } catch (e) {
      if (isAuthRejection(httpStatusOf(e))) throw new RefreshError(true)
      if (attempt === REFRESH_MAX_ATTEMPTS - 1) throw new RefreshError(false)
      await new Promise((r) => setTimeout(r, refreshBackoffMs(attempt)))
    }
  }
  throw new RefreshError(false)
}

function clearTokens() {
  remove(ACCESS_KEY)
  remove(REFRESH_KEY)
  // Invalida o media token cacheado (módulo http) junto do logout/limpeza de auth,
  // pra que a próxima sessão pegue um token fresco em vez de reusar o da sessão antiga.
  clearMediaToken()
  // Private-session hygiene: do not leave the next browser user with the
  // previous session's playlist, open curtain, or inherited incognito toggle.
  clearPlaylistSnapshot()
  resetIncognitoFlag()
  setRevealHidden(false)
}
