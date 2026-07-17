import axios, { AxiosError, type InternalAxiosRequestConfig } from 'axios'
import { isIncognito } from '../lib/incognito'
import { isRevealHidden } from '../lib/reveal'
import { isRetryableGet, retryDelayMs, RETRY_MAX } from './retry'

export const MAGNET_PREFIX = 'magnet:?xt=urn:btih:'

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

// stripToken remove um token= pré-existente (qualquer posição), sem deixar `?&`.
// Torna o withToken IDEMPOTENTE: re-aplicar não empilha `token=A&token=B`. Sem isto,
// o vídeo LOCAL ficava com `?...&token=ACCESS&token=MEDIA` (a URL cacheada do
// /api/local/play já trazia um token e o withToken adicionava outro) — URL instável.
function stripToken(url: string): string {
  const qIdx = url.indexOf('?')
  if (qIdx === -1) return url
  const base = url.slice(0, qIdx)
  const params = url.slice(qIdx + 1).split('&').filter(p => p && !p.startsWith('token='))
  return params.length ? `${base}?${params.join('&')}` : base
}

// withToken appends an access token as ?token= query param. Used em URLs que
// vão pra <video src>/<track src> onde headers Authorization não podem ser
// setados — middleware aceita ?token= como fallback. IDEMPOTENTE (stripToken).
//
// override: quando presente, usa esse token em vez do access token regular.
// Caso de uso: o PlayerModal pega um media token (scope="media", TTL longo)
// uma vez ao abrir e passa aqui — se usássemos o access token regular, o
// refresh em background trocaria a query string e o <video> resetaria o
// playback pra 0 (mesmo path, src "novo" do ponto de vista do browser).
export function withToken(url: string, override?: string): string {
  const raw = override ?? localStorage.getItem('jackui:auth.access')
  if (!raw) return url
  const cleaned = String(raw).replaceAll(/^"|"$/g, '') // localStorage values are JSON-stringified
  const base = stripToken(url)
  const sep = base.includes('?') ? '&' : '?'
  return `${base}${sep}token=${encodeURIComponent(cleaned)}`
}

// fetchMediaToken pede ao backend um JWT scope="media" com TTL longo (6h por
// default). O PlayerModal chama isso ao montar e passa o token retornado pros
// URL builders via o param override do withToken — assim a URL do <video src>
// permanece estável durante toda a sessão de playback, sobrevivendo a
// refreshes do access token regular (que trocariam a query string e
// derrubariam o playback pra 0).
//
// CACHEADO na sessão (module-level) + single-flight: o token de mídia vale pra
// TODA a sessão (não é por-faixa), então re-buscá-lo retornaria um JWT NOVO
// (iat/exp diferentes) → mudaria o `?token=` da URL → o browser recarregaria o
// <video> (loadstart) e ABORTARIA o play() pendente (AbortError) — era a causa
// do "play não toca no iPhone": uma re-init do player re-buscava o token e
// derrubava a reprodução. Com o cache, qualquer re-busca retorna o MESMO token
// → streamURL byte-idêntico → sem reload. Invalidado em clearMediaToken (logout).
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

// clearMediaToken invalida o cache acima — chamado no logout/limpeza de auth
// (clearTokens) pra que a próxima sessão pegue um token fresco.
export function clearMediaToken() {
  mediaTokenCache = ''
  mediaTokenInFlight = null
}

export default api
