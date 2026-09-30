import axios, { AxiosError, type AxiosRequestConfig, type InternalAxiosRequestConfig } from 'axios'
import { isIncognito } from '../lib/incognito'
import { isRevealHidden } from '../lib/reveal'
import { isRetryableGet, retryDelayMs, RETRY_MAX } from './retry'

export const MAGNET_PREFIX = 'magnet:?xt=urn:btih:'

// sessionLifecycle marks best-effort session-lifecycle calls (logout,
// incognito cleanup/heartbeat) with skipAuthRefresh. The AuthContext's
// 401→refresh interceptor ignores 401s from those calls: without the marker,
// logout() re-enters the interceptor via its own DELETE /user/incognito
// (which 401s on an already-dead session) → refresh → 401 → logout() → … infinite
// mutual recursion (request storm in the proxy logs; the UI never reaches the
// login screen). These calls must fail straight into the caller's try/catch
// (they are fire-and-forget by design).
export function sessionLifecycle(config: AxiosRequestConfig = {}): AxiosRequestConfig {
  return { ...config, skipAuthRefresh: true } as AxiosRequestConfig
}

// Exported so diagnostic shippers (lib/diag.ts) can post without re-wiring
// auth interceptors. Don't reach into this directly from feature code — keep
// using the helper functions below; this is for cross-cutting infra only.
export const api = axios.create({
  baseURL: '/api',
  headers: {
    'Content-Type': 'application/json',
  },
})

// Tag every request with X-JackUI-Incognito when the user has the toggle on.
// Backend middleware reads this and instructs history/library handlers to skip
// the write while still returning 200 — UX stays fluid, just nothing persists.
api.interceptors.request.use((config) => {
  if (isIncognito()) {
    config.headers['X-JackUI-Incognito'] = '1'
  }
  // When the hidden curtain is open (easter egg), let the backend include
  // hidden favourites / Continue Watching / downloads / local entries.
  if (isRevealHidden()) {
    config.headers['X-JackUI-Reveal-Hidden'] = '1'
  }
  return config
})

// Retry idempotent GETs on transient failures (network blip, 429, 5xx) with
// backoff so a momentary hiccup doesn't surface as a hard error on cards,
// search, health probes, metadata, etc. POSTs are never retried (may mutate).
// 401s are left to the auth refresh interceptor (AuthContext) — not retried here.
api.interceptors.response.use(undefined, async (error: AxiosError) => {
  const config = error.config as (InternalAxiosRequestConfig & { _retryCount?: number }) | undefined
  if (!config || !isRetryableGet(config.method, error.response?.status)) {
    throw error
  }
  const attempt = config._retryCount ?? 0
  if (attempt >= RETRY_MAX) throw error
  config._retryCount = attempt + 1
  const ra = Number(error.response?.headers?.['retry-after'])
  await new Promise((res) => setTimeout(res, retryDelayMs(attempt, Number.isFinite(ra) ? ra : undefined)))
  return api(config)
})

// Remove stateful credentials that are rebuilt from the current browser
// session. This keeps media URLs idempotent when auth or the hidden curtain
// changes while a cached local-play URL is being reused.
function stripMediaCredentials(url: string): string {
  const qIdx = url.indexOf('?')
  if (qIdx === -1) return url
  const base = url.slice(0, qIdx)
  const params = url.slice(qIdx + 1).split('&').filter((p) => {
    if (!p) return false
    const key = p.split('=', 1)[0]
    return key !== 'token' && key !== 'revealHidden'
  })
  return params.length ? `${base}?${params.join('&')}` : base
}

// withToken appends an access token as ?token= query param. Used on URLs that
// go to <video src>/<track src> where Authorization headers cannot be
// set — the middleware accepts ?token= as a fallback. IDEMPOTENT for token and
// for the curtain state (stripMediaCredentials).
//
// override: when present, uses that token instead of the regular access token.
// Use case: PlayerModal fetches a media token (scope="media", long TTL)
// once on open and passes it here — if we used the regular access token, a
// background refresh would swap the query string and the <video> would reset
// playback to 0 (same path, "new" src from the browser's point of view).
export function withToken(url: string, override?: string): string {
  const raw = override ?? localStorage.getItem('jackui:auth.access')
  let result = stripMediaCredentials(url)
  const append = (key: string, value: string) => {
    result += `${result.includes('?') ? '&' : '?'}${key}=${encodeURIComponent(value)}`
  }
  // Native media elements, EventSource and iframe resources cannot carry the
  // axios header, so mirror the curtain state in the query string.
  if (isRevealHidden()) append('revealHidden', '1')
  if (raw) {
    const cleaned = String(raw).replaceAll(/^"|"$/g, '') // localStorage values are JSON-stringified
    append('token', cleaned)
  }
  return result
}

// fetchMediaToken asks the backend for a scope="media" JWT with a long TTL (6h by
// default). PlayerModal calls it on mount and passes the returned token to the
// URL builders via withToken's override param — so the <video src> URL
// stays stable for the whole playback session, surviving
// refreshes of the regular access token (which would swap the query string and
// knock playback back to 0).
//
// SESSION-CACHED (module-level) + single-flight: the media token is valid for
// the WHOLE session (not per-track), so re-fetching it would return a NEW JWT
// (different iat/exp) → the URL's `?token=` would change → the browser would reload the
// <video> (loadstart) and ABORT the pending play() (AbortError) — that was the cause
// of "play doesn't play on iPhone": a player re-init re-fetched the token and
// killed playback. With the cache, any re-fetch returns the SAME token
// → byte-identical streamURL → no reload. Invalidated in clearMediaToken (logout).
let mediaTokenCache = ''
let mediaTokenInFlight: Promise<string> | null = null
export async function fetchMediaToken(): Promise<string> {
  if (mediaTokenCache) return mediaTokenCache
  if (mediaTokenInFlight) return mediaTokenInFlight
  mediaTokenInFlight = api.post('/auth/media-token')
    .then(r => { mediaTokenCache = r.data?.token || ''; return mediaTokenCache })
    .finally(() => { mediaTokenInFlight = null })
  return mediaTokenInFlight
}

// clearMediaToken invalidates the cache above — called on logout/auth cleanup
// (clearTokens) so the next session gets a fresh token.
export function clearMediaToken() {
  mediaTokenCache = ''
  mediaTokenInFlight = null
}

export default api
