import { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react'
import api, { passkeyAuthenticate } from '../api/client'
import { load, save, remove } from '../lib/storage'

export type Role = 'admin' | 'user' | 'guest'

export interface AuthUser {
  id: number
  username: string
  email?: string
  role: Role
  status?: 'active' | 'pending' | 'disabled'
  emailVerified?: boolean
  mfaEnabled?: boolean
  createdAt: string
}

interface TokenBundle {
  access: string
  refresh: string
  expiresAt: string
  user: AuthUser
}

interface AuthContextValue {
  user: AuthUser | null
  loading: boolean
  enabled: boolean // server has auth turned on
  isAdmin: boolean
  isGuest: boolean
  isAuthenticated: boolean
  login: (username: string, password: string, remember: boolean, totp?: string) => Promise<void>
  loginWithPasskey: (username: string, remember: boolean) => Promise<void>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const Ctx = createContext<AuthContextValue | null>(null)
const ACCESS_KEY = 'auth.access'
const REFRESH_KEY = 'auth.refresh'

export function AuthProvider({ children }: { children: ReactNode }) {
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
          } catch {
            await logout()
          }
        }
        return Promise.reject(error)
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
          } catch {
            clearTokens()
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
    const refresh = load<string>(REFRESH_KEY, '')
    try { if (refresh) await api.post('/auth/logout', { refresh }) } catch { /* ignore */ }
    clearTokens()
    setUser(null)
  }, [])

  const refresh = useCallback(async () => { await refreshTokens() }, [])

  return (
    <Ctx.Provider value={{
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
    }}>
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

async function refreshTokens(): Promise<void> {
  if (refreshInFlight) return refreshInFlight
  refreshInFlight = (async () => {
    try {
      const refresh = load<string>(REFRESH_KEY, '')
      if (!refresh) throw new Error('no refresh token')
      const { data } = await api.post<TokenBundle>('/auth/refresh', { refresh })
      save(ACCESS_KEY, data.access)
      save(REFRESH_KEY, data.refresh)
    } finally {
      refreshInFlight = null
    }
  })()
  return refreshInFlight
}

function clearTokens() {
  remove(ACCESS_KEY)
  remove(REFRESH_KEY)
}
