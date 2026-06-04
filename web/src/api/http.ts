import axios from 'axios'
import { isIncognito } from '../lib/incognito'

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
  return config
})

// withToken appends an access token as ?token= query param. Used em URLs que
// vão pra <video src>/<track src> onde headers Authorization não podem ser
// setados — middleware aceita ?token= como fallback.
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
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}token=${encodeURIComponent(cleaned)}`
}

// fetchMediaToken pede ao backend um JWT scope="media" com TTL longo (6h por
// default). O PlayerModal chama isso ao montar e passa o token retornado pros
// URL builders via o param override do withToken — assim a URL do <video src>
// permanece estável durante toda a sessão de playback, sobrevivendo a
// refreshes do access token regular (que trocariam a query string e
// derrubariam o playback pra 0).
export async function fetchMediaToken(): Promise<string> {
  const r = await api.post('/auth/media-token')
  return r.data?.token || ''
}

export default api
