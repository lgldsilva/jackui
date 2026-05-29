import axios from 'axios'
import { isIncognito } from '../lib/incognito'

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
  const cleaned = String(raw).replace(/^"|"$/g, '') // localStorage values are JSON-stringified
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

export interface Quality {
  resolution?: string
  codec?: string
  source?: string
  audio?: string[]
  group?: string
  year?: number
  season?: number
  episode?: number
  hdr?: boolean
  dv?: boolean
  tenBit?: boolean
  repack?: boolean
  proper?: boolean
  extended?: boolean
  remux?: boolean
  multi?: boolean
  dubbed?: boolean
  subbed?: boolean
}

export interface SearchResult {
  title: string
  tracker: string
  trackerId?: string
  categoryId: number
  category: string
  size: number
  seeders: number
  leechers: number
  age: string
  magnetUri: string
  link: string
  infoHash: string
  publishDate: string
  cached?: boolean
  quality?: Quality
  // Backend-computed enrichments (onda 2). Opcionais porque endpoints legados
  // ainda podem montar SearchResult sem eles (ex.: syntheticResult em deep
  // links). UI deve preferir esses campos quando presentes; heurística
  // client-side fica como fallback temporário até a onda 3.
  playable?: boolean
  mediaKind?: 'audio' | 'video' | 'other'
  isFavorited?: boolean
  isDownloaded?: boolean
  // Set client-side when multiple results share the same infoHash across trackers
  alsoIn?: string[]
  // Present when the result comes from a history endpoint — it's the
  // results.id primary key and unlocks per-row mutations like refresh.
  id?: number
}

export interface Indexer {
  id: string
  name: string
  description: string
  language: string
  type: string
  configured: boolean
}

export interface DownloadClient {
  id: string
  name: string
  type: string
  default: boolean
}

export interface DownloadClientFull extends DownloadClient {
  url: string
  username: string
  password: string
}

export interface JackettConfig {
  url: string
  // GET never returns the real key (secret); it comes back empty with
  // apiKeySet=true when one is stored. Sending empty on PUT keeps the current.
  apiKey: string
  apiKeySet?: boolean
}

export interface AppConfig {
  port: number
  jackett: JackettConfig
  downloadClients: DownloadClientFull[]
}

export const searchTorrents = async (
  q: string,
  indexers: string[],
  category: string
): Promise<SearchResult[]> => {
  const params = new URLSearchParams({ q })
  if (indexers.length > 0 && indexers[0] !== 'all') {
    params.set('indexers', indexers.join(','))
  }
  if (category && category !== 'all') {
    params.set('category', category)
  }
  const { data } = await api.get<SearchResult[]>(`/search?${params}`)
  return data
}

export const getIndexers = async (): Promise<Indexer[]> => {
  const { data } = await api.get<Indexer[]>('/indexers')
  return data
}

export const getClients = async (): Promise<DownloadClient[]> => {
  const { data } = await api.get<DownloadClient[]>('/clients')
  return data
}

export const downloadTorrent = async (
  clientId: string,
  magnetUri: string,
  torrentUrl: string,
  savePath?: string
): Promise<void> => {
  await api.post('/download', { clientId, magnetUri, torrentUrl, savePath })
}

export const getConfig = async (): Promise<AppConfig> => {
  const { data } = await api.get<AppConfig>('/config')
  return data
}

export const saveConfig = async (config: AppConfig): Promise<void> => {
  await api.put('/config', config)
}

export const testJackettConnection = async (): Promise<{ success: boolean; message?: string; error?: string }> => {
  const { data } = await api.post('/config/test')
  return data
}

export interface HistoryEntry {
  query: string
  resultCount: number
  lastSaved: string
}

export const getHistory = async (): Promise<HistoryEntry[]> => {
  const { data } = await api.get<HistoryEntry[]>('/history')
  return data
}

export const getHistoryResults = async (q: string): Promise<SearchResult[]> => {
  const { data } = await api.get<SearchResult[]>(`/history/results?q=${encodeURIComponent(q)}`)
  return data
}

export interface CachedSearchResult extends SearchResult {
  query?: string // origin query when returned by SearchCache
}

export const searchCache = async (q: string): Promise<CachedSearchResult[]> => {
  const { data } = await api.get<CachedSearchResult[]>(`/history/cache?q=${encodeURIComponent(q)}`)
  return data
}

// Response from POST /api/history/:id/refresh. `cached=true` means the swarm
// numbers came from the 5min TTL cache (no fresh Jackett call was made).
export interface HistoryRefreshResponse {
  id: number
  seeders: number
  leechers: number
  fetchedAt: string
  cached: boolean
}

export const historyRefresh = async (id: number): Promise<HistoryRefreshResponse> => {
  const { data } = await api.post<HistoryRefreshResponse>(`/history/${id}/refresh`)
  return data
}

// ─── Streaming ──────────────────────────────────────────────────────────────

export interface StreamFile {
  index: number
  path: string
  size: number
  isVideo: boolean
  downloaded: number
  progress: number
  priority: 'none' | 'low' | 'normal' | 'high'
}

export interface TorrentInfo {
  infoHash: string
  name: string
  totalSize: number
  files: StreamFile[]
  peers: number
  seeders: number
  downRate: number
  upRate: number
  progress: number
  primaryFile: number
  // Optional fields populated by ActiveList / Get when the user has interacted
  // with the Transmission-style controls. Older callers (initial streamAdd)
  // may not see these populated.
  status?: 'downloading' | 'paused' | 'seeding' | 'complete'
  priority?: 'low' | 'normal' | 'high' | ''
  trackers?: string[]
}

// ─── Local file source (pseudo-hash routing) ─────────────────────────────
//
// Arquivos locais usam um "pseudo info-hash" no formato `local-<base64url(json{mount,path})>`.
// PlayerModal e demais consumers continuam achando que estão lidando com um torrent
// normal — as funções abaixo (streamProbe, streamSidecars, subtitlesAuto, etc.)
// detectam o prefixo e roteiam pro `/api/local/*` em vez do `/api/stream/*`.
//
// Vantagem: PlayerModal não precisa mudar (zero risco no caminho torrent que já funciona).

const LOCAL_PREFIX = 'local-'

export function isLocalHash(hash: string): boolean {
  return typeof hash === 'string' && hash.startsWith(LOCAL_PREFIX)
}

export function buildLocalHash(mount: string, path: string): string {
  const json = JSON.stringify({ mount, path })
  // base64url, no padding (URL-safe)
  const b64 = btoa(unescape(encodeURIComponent(json)))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replaceAll('=', '')
  return LOCAL_PREFIX + b64
}

export function parseLocalHash(hash: string): { mount: string; path: string } | null {
  if (!isLocalHash(hash)) return null
  try {
    let b64 = hash.slice(LOCAL_PREFIX.length).replace(/-/g, '+').replace(/_/g, '/')
    while (b64.length % 4) b64 += '='
    const json = decodeURIComponent(escape(atob(b64)))
    const parsed = JSON.parse(json)
    if (typeof parsed.mount === 'string' && typeof parsed.path === 'string') return parsed
    return null
  } catch {
    return null
  }
}

function localQS(mount: string, path: string): string {
  return `mount=${encodeURIComponent(mount)}&path=${encodeURIComponent(path)}`
}

// streamMetadata returns a cached TorrentInfo snapshot if the server has seen
// this hash before. Use in parallel with streamAdd to render the file list
// instantly while the torrent client is still resolving peers.
export const streamMetadata = async (hash: string): Promise<TorrentInfo | null> => {
  // Local files: synthesize TorrentInfo from /api/local/play (no real metadata cache).
  if (isLocalHash(hash)) return synthesizeLocalInfo(hash).catch(() => null)
  try {
    const { data, status } = await api.get<TorrentInfo>(`/stream/metadata/${hash}`, { validateStatus: () => true })
    return status === 200 ? data : null
  } catch {
    return null
  }
}

export const streamAdd = async (magnet: string): Promise<TorrentInfo> => {
  // Local files: magnet carries the pseudo-hash. Synthesize TorrentInfo from
  // /api/local/play + /api/local/probe without touching the torrent client.
  const localHash = extractHashFromMagnet(magnet)
  if (localHash && isLocalHash(localHash)) return synthesizeLocalInfo(localHash)
  const { data } = await api.post<TorrentInfo>('/stream/add', { magnet })
  return data
}

export const streamAddTorrentFile = async (file: File): Promise<TorrentInfo> => {
  const fd = new FormData()
  fd.append('file', file)
  const { data } = await api.post<TorrentInfo>('/stream/add-file', fd, {
    headers: { 'Content-Type': 'multipart/form-data' }
  })
  return data
}

function extractHashFromMagnet(magnet: string): string | null {
  const m = /[?&]xt=urn:btih:([^&]+)/i.exec(magnet)
  return m ? decodeURIComponent(m[1]) : null
}

// Cache da URL resolvida por localPlay (direct ou HLS) — populada por
// synthesizeLocalInfo, lida pelos URL builders (streamFileURL etc.) pra que
// PlayerModal não precise distinguir torrent de local.
const localPlayableURLCache = new Map<string, string>()

// synthesizeLocalInfo constrói um TorrentInfo "falso" pra arquivos locais.
// O PlayerModal não distingue — só lê os mesmos campos (infoHash, name, files,
// totalSize, primaryFile). file index é sempre 0 (o próprio arquivo local).
async function synthesizeLocalInfo(hash: string): Promise<TorrentInfo> {
  const loc = parseLocalHash(hash)
  if (!loc) throw new Error('invalid local hash')
  const play = await localPlay(loc.mount, loc.path)
  // The URL from localPlay starts with /api/... (no token); withToken adds it.
  localPlayableURLCache.set(hash, play.url)
  const name = loc.path.split('/').pop() || loc.path
  const file: StreamFile = {
    index: 0,
    path: loc.path,
    size: 0,
    isVideo: !/\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i.test(loc.path),
    downloaded: 0,
    progress: 1,
    priority: 'normal',
  }
  return {
    infoHash: hash,
    name,
    totalSize: 0,
    files: [file],
    peers: 0,
    seeders: 0,
    downRate: 0,
    upRate: 0,
    progress: 1,
    primaryFile: 0,
  }
}

// localResolvedURL returns the cached URL with auth token attached, or empty
// string if not yet resolved. Used by all the streamFileURL/streamHLSMasterURL/
// streamTranscodeURL builders when the hash is a local pseudo-hash — they all
// converge to the same URL (the server decided direct vs HLS in /api/local/play).
export function localResolvedURL(hash: string, tokenOverride?: string): string {
  const url = localPlayableURLCache.get(hash)
  return url ? withToken(url, tokenOverride) : ''
}

/** Picks the best available source for streaming — prefers magnet, falls back to .torrent URL. */
export function pickTorrentSource(r: SearchResult): string {
  return r.magnetUri || r.link || ''
}

export const streamInfo = async (hash: string): Promise<TorrentInfo> => {
  if (isLocalHash(hash)) return synthesizeLocalInfo(hash)
  const { data } = await api.get<TorrentInfo>(`/stream/info/${hash}`)
  return data
}

export const streamDrop = async (hash: string): Promise<void> => {
  await api.delete(`/stream/${hash}`)
}

// ─── Transmission-style controls (active torrents) ────────────────────────

export const streamActive = async (): Promise<TorrentInfo[]> => {
  const { data } = await api.get<TorrentInfo[]>('/stream/active')
  return data || []
}

export const streamPause = async (hash: string): Promise<void> => {
  await api.post(`/stream/${hash}/pause`)
}

export const streamResume = async (hash: string): Promise<void> => {
  await api.post(`/stream/${hash}/resume`)
}

export type StreamPriority = 'low' | 'normal' | 'high'

export const streamSetPriority = async (hash: string, priority: StreamPriority): Promise<void> => {
  await api.post(`/stream/${hash}/priority`, { priority })
}

export const streamSetFilePriority = async (hash: string, fileIdx: number, priority: 'none' | 'low' | 'normal' | 'high'): Promise<void> => {
  await api.post(`/stream/${hash}/files/${fileIdx}/priority`, { priority })
}

export const streamPauseAll = async (): Promise<{ paused: number }> => {
  const { data } = await api.post<{ paused: number }>('/stream/active/pause')
  return data
}

export const streamResumeAll = async (): Promise<{ resumed: number }> => {
  const { data } = await api.post<{ resumed: number }>('/stream/active/resume')
  return data
}

// Bandwidth caps in bytes/sec (0 = unlimited).
export interface StreamLimits {
  down: number
  up: number
}

export const streamGetLimits = async (): Promise<StreamLimits> => {
  const { data } = await api.get<StreamLimits>('/stream/limits')
  return data
}

export const streamSetLimits = async (limits: StreamLimits): Promise<StreamLimits> => {
  const { data } = await api.post<StreamLimits>('/stream/limits', limits)
  return data
}

// streamPrefetch hints the server to start downloading head bytes of `fileIdx`
// on an already-active torrent (e.g., next episode of a series). Fire-and-forget.
// ─── TMDB enrichment ──────────────────────────────────────────────────────

export interface TmdbMatch {
  tmdbId: number
  imdbId?: string
  title: string
  year: number
  posterUrl: string
  overview: string
  voteAverage: number
  imdbRating?: number
  kind: 'movie' | 'tv'
}

// In-memory dedupe for in-flight requests + soft session cache. Server already
// caches 30d but this prevents N visible cards from firing N parallel requests
// for the same title.
const tmdbInFlight = new Map<string, Promise<TmdbMatch | null>>()
const tmdbSessionCache = new Map<string, TmdbMatch | null>()

export const tmdbMatch = async (title: string): Promise<TmdbMatch | null> => {
  const key = title.trim().toLowerCase()
  if (tmdbSessionCache.has(key)) return tmdbSessionCache.get(key)!
  if (tmdbInFlight.has(key)) return tmdbInFlight.get(key)!
  const p = (async () => {
    try {
      const r = await api.get<TmdbMatch>(`/tmdb/match?title=${encodeURIComponent(title)}`, { validateStatus: () => true })
      if (r.status === 200) {
        tmdbSessionCache.set(key, r.data)
        return r.data
      }
      tmdbSessionCache.set(key, null)
      return null
    } catch {
      return null
    } finally {
      tmdbInFlight.delete(key)
    }
  })()
  tmdbInFlight.set(key, p)
  return p
}

// tmdbTrending returns this week's trending movies + shows for the Discover page.
// Empty array when TMDB is disabled (no key) or on error — the page degrades to
// an "enable TMDB" hint rather than failing.
export const tmdbTrending = async (): Promise<TmdbMatch[]> => {
  try {
    const { data } = await api.get<TmdbMatch[]>('/tmdb/trending', { validateStatus: () => true })
    return Array.isArray(data) ? data : []
  } catch {
    return []
  }
}

// ─── Watchlists ────────────────────────────────────────────────────────────

export interface Watchlist {
  id: number
  userId: number
  query: string
  category: string
  minSeeders: number
  ntfyTopic: string
  lastChecked: string
  createdAt: string
  hitCount?: number
}

export interface WatchlistHit {
  infoHash: string
  title: string
  magnet: string
  seeders: number
  size: number
  seenAt: string
}

export const watchlistsList = async (): Promise<Watchlist[]> => {
  const { data } = await api.get<Watchlist[]>('/watchlists')
  return data || []
}

export const watchlistsCreate = async (
  query: string, category = '', minSeeders = 1, ntfyTopic = '',
): Promise<Watchlist> => {
  const { data } = await api.post<Watchlist>('/watchlists', { query, category, minSeeders, ntfyTopic })
  return data
}

export const watchlistsUpdate = async (
  id: number, query: string, category = '', minSeeders = 1, ntfyTopic = '',
): Promise<void> => {
  await api.put(`/watchlists/${id}`, { query, category, minSeeders, ntfyTopic })
}

export const watchlistsDelete = async (id: number): Promise<void> => {
  await api.delete(`/watchlists/${id}`)
}

export const watchlistsHits = async (id: number): Promise<WatchlistHit[]> => {
  const { data } = await api.get<WatchlistHit[]>(`/watchlists/${id}/hits`)
  return data || []
}

export const streamPrefetch = async (hash: string, fileIdx: number): Promise<void> => {
  try {
    await api.post(`/stream/prefetch/${hash}/${fileIdx}`)
  } catch {
    // Silent: prefetch is best-effort, never block playback if it fails.
  }
}

// ── AI title-identification benchmark (admin) ────────────────────────────────
export interface AISlotScore {
  slotId: string
  provider: string
  model: string
  accuracy: number      // 0..1
  avgLatencyMs: number
  composite: number
  samples: number
  failureReason?: string
}
export interface AIBenchmarkCase { raw: string; expect: string }
export interface AIStatus {
  enabled: boolean
  chain: { id: string; provider: string; model: string }[]
  results: AISlotScore[]
  cases: AIBenchmarkCase[]
}

export const aiBenchmarkStatus = async (): Promise<AIStatus> => {
  const { data } = await api.get<AIStatus>('/ai/benchmark')
  return data
}
export const runAIBenchmark = async (): Promise<AISlotScore[]> => {
  const { data } = await api.post<{ results: AISlotScore[] }>('/ai/benchmark')
  return data.results || []
}
export const saveAICases = async (cases: AIBenchmarkCase[]): Promise<AIBenchmarkCase[]> => {
  const { data } = await api.put<{ cases: AIBenchmarkCase[] }>('/ai/benchmark/cases', { cases })
  return data.cases || []
}

// ── Auth: account + admin user management ────────────────────────────────────
export interface AdminUser {
  id: number
  username: string
  email: string
  role: 'admin' | 'user' | 'guest'
  status: 'active' | 'pending' | 'disabled'
  emailVerified: boolean
  ntfyTopic: string
  createdAt: string
}

export const changePassword = async (current: string, next: string): Promise<void> => {
  await api.post('/auth/password', { current, new: next })
}
export const mfaEnroll = async (): Promise<{ secret: string; uri: string }> => {
  const { data } = await api.post<{ secret: string; uri: string }>('/auth/mfa/enroll')
  return data
}
export const mfaVerify = async (code: string): Promise<string[]> => {
  const { data } = await api.post<{ backupCodes: string[] }>('/auth/mfa/verify', { code })
  return data.backupCodes || []
}
export const mfaDisable = async (password: string): Promise<void> => {
  await api.post('/auth/mfa/disable', { password })
}
export const mfaBackupCodesRemaining = async (): Promise<number> => {
  const { data } = await api.get<{ remaining: number }>('/auth/mfa/backup-codes')
  return data.remaining ?? 0
}
export const mfaRegenerateBackupCodes = async (password: string): Promise<string[]> => {
  const { data } = await api.post<{ backupCodes: string[] }>('/auth/mfa/backup-codes/regenerate', { password })
  return data.backupCodes || []
}
export const adminListUsers = async (): Promise<AdminUser[]> => {
  const { data } = await api.get<AdminUser[]>('/auth/users')
  return data || []
}
export const adminCreateUser = async (username: string, password: string, role: 'admin' | 'user' | 'guest'): Promise<void> => {
  await api.post('/auth/users', { username, password, role })
}
export const adminDeleteUser = async (id: number): Promise<void> => {
  await api.delete(`/auth/users/${id}`)
}
export const adminSetUserStatus = async (id: number, status: 'active' | 'pending' | 'disabled'): Promise<void> => {
  await api.patch(`/auth/users/${id}/status`, { status })
}
export const adminInvite = async (email?: string): Promise<string> => {
  const { data } = await api.post<{ link: string }>('/auth/users/invite', { email: email || '' })
  return data.link
}

// ── Notification settings ──────────────────────────────────────────────────────
export const setNtfyTopic = async (topic: string): Promise<void> => {
  await api.post('/user/ntfy-topic', { topic })
}
export const notifyTest = async (): Promise<void> => {
  await api.post('/user/notify-test')
}

// ── Active sessions ──────────────────────────────────────────────────────────
export interface SessionInfo {
  id: string
  createdAt: string
  expiresAt: string
  remember: boolean
  current: boolean
}
export const listSessions = async (currentRefresh: string): Promise<SessionInfo[]> => {
  const { data } = await api.post<{ sessions: SessionInfo[] }>('/auth/sessions', { refresh: currentRefresh })
  return data.sessions || []
}
export const revokeSession = async (id: string): Promise<void> => {
  await api.delete(`/auth/sessions/${encodeURIComponent(id)}`)
}
export const revokeOtherSessions = async (currentRefresh: string): Promise<number> => {
  const { data } = await api.post<{ revoked: number }>('/auth/sessions/revoke-others', { refresh: currentRefresh })
  return data.revoked ?? 0
}

// Public auth flows (no token needed). These bypass the axios auth interceptor
// concerns since they're called from unauthenticated pages.
export const registerAccount = async (username: string, email: string, password: string, invite?: string) => {
  const { data } = await api.post<{ status: string; invited: boolean; message: string }>('/auth/register', { username, email, password, invite: invite || '' })
  return data
}
export const verifyEmail = async (token: string): Promise<void> => {
  await api.post('/auth/verify-email', { token })
}
export const forgotPassword = async (email: string): Promise<string> => {
  const { data } = await api.post<{ message: string }>('/auth/forgot', { email })
  return data.message
}
export const resetPassword = async (token: string, password: string): Promise<void> => {
  await api.post('/auth/reset', { token, password })
}

// ── Passkey (WebAuthn) ───────────────────────────────────────────────────────
// WebAuthn moves binary blobs (challenge, credential ids, signatures) over JSON
// as base64url. The browser API works with ArrayBuffers, so every "begin" reply
// is decoded into buffers before navigator.credentials.{create,get}, and the
// authenticator's response is re-encoded to base64url before posting "finish".

const b64urlToBuf = (s: string): ArrayBuffer => {
  const pad = s.length % 4 === 0 ? '' : '='.repeat(4 - (s.length % 4))
  const bin = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'))
  const buf = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i)
  return buf.buffer
}
const bufToB64url = (buf: ArrayBuffer): string => {
  const bytes = new Uint8Array(buf)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i])
  return btoa(bin).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '')
}

export function isPasskeySupported(): boolean {
  return typeof window !== 'undefined' && !!window.PublicKeyCredential && !!navigator.credentials?.create
}

export interface PasskeyInfo { id: string }
export const passkeyList = async (): Promise<PasskeyInfo[]> => {
  const { data } = await api.get<{ passkeys: PasskeyInfo[] }>('/auth/passkey')
  return data.passkeys || []
}
export const passkeyDelete = async (id: string): Promise<void> => {
  await api.delete(`/auth/passkey/${encodeURIComponent(id)}`)
}

// passkeyRegister runs the full enrollment ceremony (authenticated). Throws on
// user cancellation or authenticator error — caller surfaces the message.
export const passkeyRegister = async (): Promise<void> => {
  const { data } = await api.post<{ options: any; session: string }>('/auth/passkey/register/begin')
  const pk = data.options.publicKey
  pk.challenge = b64urlToBuf(pk.challenge)
  pk.user.id = b64urlToBuf(pk.user.id)
  if (Array.isArray(pk.excludeCredentials)) {
    pk.excludeCredentials = pk.excludeCredentials.map((c: any) => ({ ...c, id: b64urlToBuf(c.id) }))
  }
  const cred = (await navigator.credentials.create({ publicKey: pk })) as PublicKeyCredential | null
  if (!cred) throw new Error('passkey cancelada')
  const att = cred.response as AuthenticatorAttestationResponse
  const body = {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    response: {
      attestationObject: bufToB64url(att.attestationObject),
      clientDataJSON: bufToB64url(att.clientDataJSON),
    },
  }
  await api.post('/auth/passkey/register/finish', body, { params: { session: data.session } })
}

export interface PasskeyTokenBundle {
  access: string
  refresh: string
  expiresAt: string
  user: any
}

// passkeyAuthenticate runs the login assertion ceremony (public) and returns the
// token bundle. The caller (AuthContext) persists the tokens + sets the user.
export const passkeyAuthenticate = async (username: string, remember: boolean): Promise<PasskeyTokenBundle> => {
  const { data } = await api.post<{ options: any; session: string }>('/auth/passkey/login/begin', { username })
  const pk = data.options.publicKey
  pk.challenge = b64urlToBuf(pk.challenge)
  if (Array.isArray(pk.allowCredentials)) {
    pk.allowCredentials = pk.allowCredentials.map((c: any) => ({ ...c, id: b64urlToBuf(c.id) }))
  }
  const assertion = (await navigator.credentials.get({ publicKey: pk })) as PublicKeyCredential | null
  if (!assertion) throw new Error('passkey cancelada')
  const r = assertion.response as AuthenticatorAssertionResponse
  const body = {
    id: assertion.id,
    rawId: bufToB64url(assertion.rawId),
    type: assertion.type,
    response: {
      authenticatorData: bufToB64url(r.authenticatorData),
      clientDataJSON: bufToB64url(r.clientDataJSON),
      signature: bufToB64url(r.signature),
      userHandle: r.userHandle ? bufToB64url(r.userHandle) : '',
    },
  }
  const { data: bundle } = await api.post<PasskeyTokenBundle>('/auth/passkey/login/finish', body, {
    params: { username, session: data.session, remember: remember ? '1' : '' },
  })
  return bundle
}

// ── Swarm health (seeds / availability for cards) ────────────────────────────
export interface StreamHealth {
  known: boolean
  active: boolean
  refreshing: boolean
  seeders?: number
  peers?: number
  available?: boolean
  checkedAt?: string
}

// streamHealth returns the last-known swarm health for a torrent (and kicks a
// background re-probe server-side when stale). `magnet` lets the server probe an
// inactive torrent. Best-effort: returns an "unknown" shape on error.
// streamHealth peeks the last-known swarm health (cheap, no swarm activity).
// Pass probe=true ONLY on an explicit user action — that adds the torrent to the
// swarm to count peers (expensive). Auto-calling with probe=true bogs the app.
export const streamHealth = async (hash: string, magnet?: string, probe = false): Promise<StreamHealth> => {
  try {
    const params = new URLSearchParams()
    if (magnet) params.set('magnet', magnet)
    if (probe) params.set('probe', '1')
    const { data } = await api.get<StreamHealth>(`/stream/health/${hash}?${params.toString()}`)
    return data
  } catch {
    return { known: false, active: false, refreshing: false }
  }
}

// streamArtURL returns the persisted per-torrent thumbnail (poster/cover/frame).
// Serves bytes, 302s to a TMDB poster, or 204s when nothing is resolved yet —
// so an <img> using it should fall back to the title-based poster on error.
export const streamArtURL = (hash: string): string =>
  withToken(`/api/stream/art/${hash}`)

// resolveArt kicks off the server-side art resolution chain (embedded torrent
// image → TMDB → web search → captured frame) and persists the result by
// info_hash. Idempotent + best-effort. `name` lets a card trigger resolution
// when the torrent isn't active (so the server has a title to search even
// without cached metadata); pass fileIdx=-1 there (no frame without playback).
// Returns the resolved source ("torrent"|"tmdb"|"web"|"frame") or null.
export const resolveArt = async (hash: string, fileIdx = -1, name?: string): Promise<string | null> => {
  // Local files don't have torrent-side art resolution (no infoHash on disk to
  // key by). LocalPage gera o seu próprio thumbnail via /local/thumb.
  if (isLocalHash(hash)) return null
  try {
    const params = new URLSearchParams({ file: String(fileIdx) })
    if (name) params.set('name', name)
    const { data, status } = await api.post(`/stream/art/${hash}/resolve?${params.toString()}`)
    return status === 200 ? (data?.source ?? null) : null
  } catch {
    // Silent: thumbnail resolution must never affect playback or browsing.
    return null
  }
}

export const streamFileURL = (hash: string, fileIdx: number, tokenOverride?: string): string => {
  if (isLocalHash(hash)) return localResolvedURL(hash, tokenOverride)
  return withToken(`/api/stream/${hash}/${fileIdx}`, tokenOverride)
}

// streamThumbnailURL returns the URL of a single JPEG frame captured at `atSeconds`
// in the file. Used by the player progress-bar hover preview.
export const streamThumbnailURL = (hash: string, fileIdx: number, atSeconds: number): string =>
  withToken(`/api/stream/thumb/${hash}/${fileIdx}?at=${Math.max(0, Math.floor(atSeconds))}`)

// streamArtworkURL returns the URL of embedded cover-art extracted by the server.
// Returns 204 server-side if the file has no embedded picture.
export const streamArtworkURL = (hash: string, fileIdx: number, tokenOverride?: string): string =>
  withToken(`/api/stream/artwork/${hash}/${fileIdx}`, tokenOverride)

// streamPlaylistM3UURL returns an absolute URL to a downloadable .m3u that points
// back to the stream. Used by the "Open in VLC" button — universal across desktop
// VLC, iOS VLC, and Android VLC, unlike the proprietary vlc:// scheme.
export const streamPlaylistM3UURL = (hash: string, fileIdx: number, transcode?: 'h264' | 'hevc'): string => {
  const base = `/api/stream/playlist/${hash}/${fileIdx}`
  const url = withToken(base)
  if (transcode) {
    const separator = url.includes('?') ? '&' : '?'
    return `${url}${separator}transcode=${transcode}`
  }
  return url
}

export interface CacheEntry {
  path: string
  size: number
  modTime: string
  isActive: boolean
  isFavorite: boolean
  /** Hex-encoded SHA1 info hash. Empty when the backend can't resolve it
   *  (e.g., no persisted .torrent in metainfoDir). The UI uses this to feed
   *  a bare magnet to /api/stream/add for the Play button. */
  infoHash?: string
}

export interface StreamCacheStats {
  dataDir: string
  totalSize: number
  maxSize: number
  numActive: number
  entries: CacheEntry[]
}

export const streamCacheStats = async (): Promise<StreamCacheStats> => {
  const { data } = await api.get<StreamCacheStats>('/stream/cache')
  return data
}

export const streamCacheClear = async (entry?: string): Promise<void> => {
  const url = entry ? `/stream/cache?entry=${encodeURIComponent(entry)}` : '/stream/cache'
  await api.delete(url)
}

export interface StreamRate {
  downRate: number
  upRate: number
  activeTorrents: number
}

export const streamRate = async (): Promise<StreamRate> => {
  const { data } = await api.get<StreamRate>('/stream/rate')
  return data
}

// ─── Favorites ────────────────────────────────────────────────────────────

export interface StreamFavorite {
  name: string
  infoHash: string
  magnet: string
  userId: number
  favoritedAt: string
  reason: 'manual' | 'auto-5min'
  folderId: number | null  // nil = root level; otherwise nested in a FavoriteFolder
}

/** Favorite folder for organizing the user's favorites tree. */
export interface FavoriteFolder {
  id: number
  userId: number
  name: string
  parentId: number | null  // null = root-level folder
  position: number
  createdAt: string
}

// ─── Library (per-user history of streamed torrents) ───────────────────────

export interface LibraryEntry {
  id: number
  userId: number
  infoHash: string
  magnet: string
  name: string
  primaryFileIndex: number
  // The file the user actually last watched (multi-file torrents). -1 = unknown.
  lastFileIndex: number
  totalSize: number
  resumeSeconds: number
  durationSeconds: number
  kind: string
  lastPlayedAt: string
  addedAt: string
}

export const libraryList = async (opts: { limit?: number; all?: boolean } = {}): Promise<LibraryEntry[]> => {
  const p = new URLSearchParams()
  if (opts.limit) p.set('limit', String(opts.limit))
  if (opts.all) p.set('all', '1')
  const { data } = await api.get<LibraryEntry[]>(`/library?${p}`)
  return data
}

export const libraryGet = async (id: number): Promise<LibraryEntry> => {
  const { data } = await api.get<LibraryEntry>(`/library/${id}`)
  return data
}

export const libraryUpdateResume = async (id: number, resumeSeconds: number, durationSeconds = 0, fileIndex?: number): Promise<void> => {
  await api.patch(`/library/${id}`, { resumeSeconds, durationSeconds, fileIndex })
}

export const libraryDelete = async (id: number): Promise<void> => {
  await api.delete(`/library/${id}`)
}

export const libraryDeleteAll = async (): Promise<{ deleted: number }> => {
  const { data } = await api.delete<{ deleted: number }>('/library')
  return data
}

// ─── Playlists ─────────────────────────────────────────────────────────────

export interface Playlist {
  id: number
  userId: number
  name: string
  description: string
  createdAt: string
  updatedAt: string
  itemCount?: number
}

export interface PlaylistItem {
  id: number
  playlistId: number
  position: number
  libraryId?: number
  title: string
  magnet: string
  infoHash: string
  fileIndex: number
  addedAt: string
}

export const playlistsList = async (): Promise<Playlist[]> => {
  const { data } = await api.get<Playlist[]>('/playlists')
  return data
}
export const playlistsGet = async (id: number): Promise<{ playlist: Playlist; items: PlaylistItem[] }> => {
  const { data } = await api.get(`/playlists/${id}`)
  return data
}
export const playlistsCreate = async (name: string, description = ''): Promise<Playlist> => {
  const { data } = await api.post<Playlist>('/playlists', { name, description })
  return data
}
export const playlistsUpdate = async (id: number, name: string, description: string): Promise<void> => {
  await api.patch(`/playlists/${id}`, { name, description })
}
export const playlistsDelete = async (id: number): Promise<void> => {
  await api.delete(`/playlists/${id}`)
}
export const playlistsAddItem = async (
  playlistId: number,
  item: { title: string; magnet: string; infoHash?: string; fileIndex?: number; libraryId?: number },
): Promise<PlaylistItem> => {
  const { data } = await api.post<PlaylistItem>(`/playlists/${playlistId}/items`, item)
  return data
}
export const playlistsRemoveItem = async (playlistId: number, itemId: number): Promise<void> => {
  await api.delete(`/playlists/${playlistId}/items/${itemId}`)
}
export const playlistsReorderItem = async (playlistId: number, itemId: number, position: number): Promise<void> => {
  await api.patch(`/playlists/${playlistId}/items/${itemId}`, { position })
}

// ─── Sidecar subtitles inside torrent ──────────────────────────────────────

export interface SidecarSubtitle {
  index: number
  path: string
  size: number
  language: string
  format: 'srt' | 'vtt' | 'ass' | 'ssa' | 'sub'
}

// In-memory cache popularizado por streamSidecars(local) — mapeia
// `${hash}:${index}` → filename, lido por streamSidecarURL pra construir o
// `?name=`. Sem isso o backend teria que re-listar o dir a cada chamada.
const localSidecarNameCache = new Map<string, string>()

export const streamSidecars = async (hash: string, fileIdx: number): Promise<SidecarSubtitle[]> => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    type LocalSub = { name: string; size: number; language: string; format: SidecarSubtitle['format']; match: number }
    const { data } = await api.get<LocalSub[]>(`/local/sidecars?${localQS(loc.mount, loc.path)}`)
    return data.map((s, i) => {
      localSidecarNameCache.set(`${hash}:${i}`, s.name)
      return {
        index: i,
        path: s.name,
        size: s.size,
        language: s.language,
        format: s.format,
      }
    })
  }
  const { data } = await api.get<SidecarSubtitle[]>(`/stream/sidecars/${hash}/${fileIdx}`)
  return data
}
export const streamSidecarURL = (hash: string, fileIdx: number, tokenOverride?: string): string => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    const name = localSidecarNameCache.get(`${hash}:${fileIdx}`) ?? ''
    if (name) {
      return withToken(`/api/local/sidecar?${localQS(loc.mount, loc.path)}&name=${encodeURIComponent(name)}`, tokenOverride)
    }
    return withToken(`/api/local/sidecar?${localQS(loc.mount, loc.path)}&index=${fileIdx}`, tokenOverride)
  }
  return withToken(`/api/stream/sidecar/${hash}/${fileIdx}`, tokenOverride)
}

export const favoritesList = async (): Promise<StreamFavorite[]> => {
  const { data } = await api.get<StreamFavorite[]>('/stream/favorites')
  return data
}

export const favoriteAdd = async (name: string, infoHash: string, magnet = '', reason: 'manual' | 'auto-5min' = 'manual'): Promise<void> => {
  await api.post('/stream/favorite', { name, infoHash, magnet, reason })
}

export const favoriteRemove = async (name: string): Promise<void> => {
  await api.delete(`/stream/favorite/${encodeURIComponent(name)}`)
}

// Import a torrent straight into favorites — magnet URI or a base64-encoded
// .torrent file. Server resolves hash + name locally (no DHT) and caches the
// metainfo so playback is instant. Returns the resolved entry.
export interface ImportResult { infoHash: string; name: string; magnet: string }
export const streamImport = async (
  payload: { magnet?: string; torrentB64?: string; name?: string; folderId?: number | null },
): Promise<ImportResult> => {
  const { data } = await api.post<ImportResult>('/stream/import', payload)
  return data
}

// ─── Favorite folders (tree organization) ──────────────────────────────
// Folders live alongside favorites; each favorite can have an optional
// folder_id. Server prevents cycles when moving subfolders.

export const folderList = async (): Promise<FavoriteFolder[]> => {
  const { data } = await api.get<FavoriteFolder[]>('/stream/favorites/folders')
  return data || []
}

export const folderCreate = async (name: string, parentId: number | null = null): Promise<FavoriteFolder> => {
  const { data } = await api.post<FavoriteFolder>('/stream/favorites/folders', { name, parentId })
  return data
}

export const folderRename = async (id: number, name: string): Promise<void> => {
  await api.patch(`/stream/favorites/folders/${id}`, { name })
}

export const folderMove = async (id: number, newParentID: number | null): Promise<void> => {
  // `parentToRoot` distinguishes "move to root" from "leave parent alone".
  // Without this flag the server can't tell `null` from "unset".
  await api.patch(`/stream/favorites/folders/${id}`, newParentID === null
    ? { parentToRoot: true }
    : { parentId: newParentID })
}

export const folderDelete = async (id: number): Promise<void> => {
  await api.delete(`/stream/favorites/folders/${id}`)
}

/** Reassigns a favorite to a folder. Pass null to move back to root level. */
export const favoriteSetFolder = async (name: string, folderID: number | null): Promise<void> => {
  await api.patch(`/stream/favorite/${encodeURIComponent(name)}/folder`, folderID === null
    ? { toRoot: true }
    : { folderId: folderID })
}

// ─── Subtitles ──────────────────────────────────────────────────────────────

export interface Subtitle {
  id: string
  language: string
  release: string
  url: string
  uploaderName: string
  downloads: number
  hearingImpaired: boolean
  trusted: boolean
}

export const subtitlesEnabled = async (): Promise<boolean> => {
  const { data } = await api.get<{ enabled: boolean }>('/subtitles/enabled')
  return data.enabled
}

export const subtitlesSearch = async (
  q: string,
  opts: { season?: number; episode?: number; langs?: string } = {},
): Promise<Subtitle[]> => {
  const params = new URLSearchParams({ q })
  if (opts.langs) params.set('langs', opts.langs)
  if (opts.season) params.set('season', String(opts.season))
  if (opts.episode) params.set('episode', String(opts.episode))
  const { data } = await api.get<Subtitle[]>(`/subtitles/search?${params}`)
  return data
}

export const subtitleDownloadURL = (fileId: string, tokenOverride?: string): string =>
  withToken(`/api/subtitles/download/${fileId}`, tokenOverride)

export interface AutoSubtitlesResponse {
  osHash: string
  osSize: number
  hashErr?: string
  file: string
  results: Subtitle[]
}

// Stremio-style auto subtitle search: uses OS file hash from the active stream
export const subtitlesAuto = async (
  hash: string,
  fileIdx: number,
  langs = 'pt-BR,pt',
): Promise<AutoSubtitlesResponse> => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    const { data } = await api.get<AutoSubtitlesResponse>(
      `/local/subtitles/auto?${localQS(loc.mount, loc.path)}&langs=${encodeURIComponent(langs)}`,
    )
    return data
  }
  const { data } = await api.get<AutoSubtitlesResponse>(
    `/subtitles/auto/${hash}/${fileIdx}?langs=${encodeURIComponent(langs)}`,
  )
  return data
}

// ─── Embedded tracks (audio + subtitles inside the container) ───────────────

export interface MediaTrack {
  index: number
  type: 'audio' | 'subtitle'
  codec: string
  language?: string
  title?: string
  default: boolean
  forced?: boolean
  channels?: number
  image?: boolean // subtitle is image-based (PGS, DVD) — requires burn-in
}

export interface StreamProbe {
  audio: MediaTrack[]
  subtitles: MediaTrack[]
}

export const streamProbe = async (hash: string, fileIdx: number): Promise<StreamProbe> => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    const { data } = await api.get<StreamProbe>(`/local/probe?${localQS(loc.mount, loc.path)}`)
    return data
  }
  const { data } = await api.get<StreamProbe>(`/stream/probe/${hash}/${fileIdx}`)
  return data
}

export const streamSubtrackURL = (hash: string, fileIdx: number, trackIdx: number, tokenOverride?: string): string => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    return withToken(`/api/local/subtrack?${localQS(loc.mount, loc.path)}&track=${trackIdx}`, tokenOverride)
  }
  return withToken(`/api/stream/subtrack/${hash}/${fileIdx}/${trackIdx}`, tokenOverride)
}

// ─── Transcoding capabilities ──────────────────────────────────────────────

export interface TranscodeEncoder {
  id: string
  codec: string
  backend: string
  available: boolean
  functional: boolean
  benchFps?: number
  description: string
  error?: string
}

export interface TranscodeCapabilities {
  probedAt: string
  os: string
  ffmpegPath: string
  ffmpegVersion: string
  hasNvidia: boolean
  hasVaapi: boolean
  hasQsv: boolean
  encoders: TranscodeEncoder[]
  decoders: Array<{ id: string; codec: string; backend: string; functional: boolean }>
  preferred: string
  preferredHevc: string
}

export const transcodeCapabilities = async (refresh = false): Promise<TranscodeCapabilities> => {
  const { data } = await api.get<TranscodeCapabilities>(
    `/transcode/capabilities${refresh ? '?refresh=1' : ''}`,
  )
  return data
}

export interface TranscodeOpts {
  audio?: number      // absolute audio stream index
  video?: 'h264' | 'hevc' | '' // force re-encode to this codec
  acodec?: 'aac' | '' // force audio re-encode
  burn?: number       // burn-in subtitle track index (forces video re-encode)
}

export const streamTranscodeURL = (hash: string, fileIdx: number, opts: TranscodeOpts, tokenOverride?: string): string => {
  if (isLocalHash(hash)) return localResolvedURL(hash, tokenOverride)
  const p = new URLSearchParams()
  if (opts.audio !== undefined) p.set('audio', String(opts.audio))
  if (opts.video) p.set('video', opts.video)
  if (opts.acodec) p.set('acodec', opts.acodec)
  if (opts.burn !== undefined) p.set('burn', String(opts.burn))
  return withToken(`/api/stream/transcode/${hash}/${fileIdx}?${p}`, tokenOverride)
}

/**
 * HLS master playlist URL. Used as the Safari/iOS fallback path: Safari's
 * MSE pipeline refuses progressive fragmented MP4 over chunked transfer but
 * natively plays HLS (.m3u8 + .ts) via `<video src>`. Apple's documented
 * streaming format; the only thing Safari treats as a first-class video
 * source. Jellyfin, Plex, Emby all do the same routing.
 */
export const streamHLSMasterURL = (hash: string, fileIdx: number, tokenOverride?: string): string => {
  if (isLocalHash(hash)) return localResolvedURL(hash, tokenOverride)
  return withToken(`/api/stream/hls/${hash}/${fileIdx}/index.m3u8`, tokenOverride)
}

/**
 * Best-effort Safari detection. Safari includes "Safari" but Chrome on macOS
 * also includes "Safari/..." for compatibility. Negative-match `Chrome` and
 * `Android` (Chrome Mobile). Browsers exposing themselves as `Edg` or
 * `Edge` also fall under non-Safari (Chromium-based, plays MP4 fine).
 */
export function isSafariBrowser(): boolean {
  const ua = navigator.userAgent
  if (/Chrome|Chromium|Android|Edg/.test(ua)) return false
  return /Safari/i.test(ua)
}

export const clearHistory = async (): Promise<void> => {
  await api.delete('/history')
}

export const deleteHistoryEntry = async (q: string): Promise<void> => {
  await api.delete(`/history?q=${encodeURIComponent(q)}`)
}

// ---- Local mount browser ----
// Browses filesystem mounts declared in config.yaml's `external.mounts`.
// Lets the player serve content already on disk (HD externo, NAS, etc.)
// without going through anacrolix. http.ServeFile handles HTTP Range for
// progressive playback; HEVC files still need browser support locally.

export interface LocalMount { name: string; path: string }
export interface LocalEntry {
  name: string
  path: string       // relative to mount root
  isDir: boolean
  size: number
  modTime: string
  isPlayable: boolean
}

export const localMounts = async (): Promise<LocalMount[]> => {
  const { data } = await api.get<LocalMount[]>('/local/mounts')
  return data || []
}

export const localList = async (mount: string, path: string): Promise<LocalEntry[]> => {
  const params = new URLSearchParams({ mount, path })
  const { data } = await api.get<LocalEntry[]>(`/local/list?${params}`)
  return data || []
}

export const localDelete = async (mount: string, path: string): Promise<void> => {
  const params = new URLSearchParams({ mount, path })
  await api.delete(`/local/file?${params}`)
}

export const localPromote = async (
  mount: string,
  path: string,
  targetSubdir: string,
  targetBase?: string,
  renameIA?: boolean,
  paths?: string[],
): Promise<void> => {
  await api.post('/local/promote', { mount, path, paths, targetSubdir, targetBase, renameIA })
}

export interface PromotePreviewEntry {
  id?: number
  path?: string
  originalName: string
  cleanName: string
  targetPath: string
  kind: 'movie' | 'tv'
  year?: number
  season?: number
  episode?: number
  episodeName?: string
  error?: string
}

export const localPromotePreview = async (
  mount: string,
  path: string,
  targetSubdir: string,
  targetBase?: string,
  paths?: string[],
): Promise<{ previews: PromotePreviewEntry[] }> => {
  const { data } = await api.post<{ previews: PromotePreviewEntry[] }>('/local/promote/preview', {
    mount,
    path,
    paths,
    targetSubdir,
    targetBase,
  })
  return data
}

// localWalk recursively lists all files under a directory in a mount.
// Returns entries with paths relative to the mount root (same format as localList).
export const localWalk = async (
  mount: string,
  path: string,
  mediaOnly = false,
): Promise<{ entries: LocalEntry[]; total: number }> => {
  const params = new URLSearchParams({ mount, path, media_only: mediaOnly ? '1' : '0' })
  const { data } = await api.get<{ entries: LocalEntry[]; total: number }>(`/local/walk?${params}`)
  return data
}

// localMove moves a file or directory from one mount to another (admin only).
// dstPath is the target directory; the source name is preserved.
export const localMove = async (
  srcMount: string,
  srcPath: string,
  dstMount: string,
  dstPath: string,
): Promise<void> => {
  await api.post('/local/move', { srcMount, srcPath, dstMount, dstPath })
}

// Direct file URL with auth token in query string (http.ServeFile handles Range
// natively; <video src> can hit this directly).
export const localFileURL = (mount: string, path: string): string => {
  const params = new URLSearchParams({ mount, path })
  return withToken(`/api/local/file?${params}`)
}

// Formats browsers can play natively without transcoding.
const NATIVE_VIDEO_EXTS = new Set(['.mp4', '.m4v', '.webm', '.mov'])

// localTranscodeURL returns a server-side transcode URL for formats browsers
// can't decode natively (MKV, AVI, WMV, etc.).
export const localTranscodeURL = (mount: string, path: string): string => {
  const params = new URLSearchParams({ mount, path })
  return withToken(`/api/local/transcode?${params}`)
}

// localVideoURL returns the best URL to play a local file: direct for
// native formats, transcoded for everything else.
export const localVideoURL = (mount: string, path: string): string => {
  const ext = path.slice(path.lastIndexOf('.')).toLowerCase()
  return NATIVE_VIDEO_EXTS.has(ext) ? localFileURL(mount, path) : localTranscodeURL(mount, path)
}

// localThumbURL returns an early-frame JPEG preview for a local video file
// (204 server-side for non-videos / undecodable files; <img> onError falls back
// to the generic icon).
export const localThumbURL = (mount: string, path: string): string => {
  const params = new URLSearchParams({ mount, path })
  return withToken(`/api/local/thumb?${params}`)
}

// LocalPlaySource describes how the frontend should load a local file. The
// backend probes the file (ffprobe) and either tells us to direct-play it
// (browser-compatible container + codecs) or to load an HLS playlist that the
// transcode pipeline produces on demand. Mirrors the torrent-side decision so
// the player can stay codec-agnostic — it just sets <video src> to `url`.
export interface LocalPlaySource {
  kind: 'direct' | 'hls'
  url: string         // ready to drop into <video src>, token already appended
  reason?: string     // when kind=hls, why (e.g. "container=matroska", "vcodec=hevc")
  vcodec?: string
  acodec?: string
  container?: string
}

// localPlay asks the server how to play a local file. The URL it returns is
// ready to use — it already carries `?token=` so it works in <video src>
// without the JS axios interceptor (which can't set headers on the element).
export const localPlay = async (mount: string, path: string): Promise<LocalPlaySource> => {
  const params = new URLSearchParams({ mount, path })
  const { data } = await api.get<LocalPlaySource>(`/local/play?${params}`)
  return data
}

// ─── Background downloads ──────────────────────────────────────────────────
// Full-file (not streaming) download queue. Backed by anacrolix file.Download
// which prioritises all pieces; protected from cache eviction until removed.

export interface DownloadEntry {
  id: number
  userId: number
  username?: string
  infoHash: string
  fileIndex: number
  filePath: string
  fileSize: number
  name: string
  magnet: string
  tracker?: string
  category?: string
  status: 'queued' | 'downloading' | 'completed' | 'failed' | 'paused'
  bytesDownloaded: number
  progress: number
  downRate?: number
  eta?: number
  startedAt?: string | null
  completedAt?: string | null
  error?: string
  createdAt: string
  promoted?: boolean   // true when file was moved outside the download dir
}

export interface DownloadCreateParams {
  infoHash: string
  fileIndex: number
  magnet: string
  name: string
  filePath: string
  fileSize: number
  tracker?: string
  category?: string
}

export interface DownloadFilterParams {
  status?: string
  tracker?: string
  category?: string
  search?: string
  sort?: string
  order?: string
  userId?: string
}

export interface DownloadUserEntry {
  id: number
  username: string
}

export const downloadsListAll = async (params: DownloadFilterParams): Promise<DownloadEntry[]> => {
  const query = new URLSearchParams()
  if (params.status) query.set('status', params.status)
  if (params.tracker) query.set('tracker', params.tracker)
  if (params.category) query.set('category', params.category)
  if (params.search) query.set('search', params.search)
  if (params.sort) query.set('sort', params.sort)
  if (params.order) query.set('order', params.order)
  if (params.userId) query.set('userId', params.userId)
  const { data } = await api.get<DownloadEntry[]>(`/downloads/all?${query.toString()}`)
  return data || []
}

export const downloadUsers = async (): Promise<DownloadUserEntry[]> => {
  const { data } = await api.get<DownloadUserEntry[]>('/downloads/users')
  return data || []
}

export const downloadsList = async (): Promise<DownloadEntry[]> => {
  const { data } = await api.get<DownloadEntry[]>('/downloads')
  return data || []
}

export const downloadCreate = async (params: DownloadCreateParams): Promise<DownloadEntry> => {
  const { data } = await api.post<DownloadEntry>('/downloads', params)
  return data
}

export const downloadDelete = async (id: number): Promise<void> => {
  await api.delete(`/downloads/${id}`)
}

export const downloadPause = async (id: number): Promise<void> => {
  await api.patch(`/downloads/${id}/pause`)
}

export const downloadResume = async (id: number): Promise<void> => {
  await api.patch(`/downloads/${id}/resume`)
}

// downloadRecheck força um "Force Recheck" (estilo qBittorrent) — re-hasha
// todos os pieces do arquivo no disco e reseta bytes_downloaded pro worker
// reconciliar depois. UI mostra spinner enquanto o backend processa
// (chamada retorna assim que o hash check inicia; o progresso aparece
// no próximo tick do worker).
export const downloadsListFiltered = async (params: DownloadFilterParams): Promise<DownloadEntry[]> => {
  const query = new URLSearchParams()
  if (params.status) query.set('status', params.status)
  if (params.tracker) query.set('tracker', params.tracker)
  if (params.category) query.set('category', params.category)
  if (params.search) query.set('search', params.search)
  if (params.sort) query.set('sort', params.sort)
  if (params.order) query.set('order', params.order)
  const { data } = await api.get<DownloadEntry[]>(`/downloads/filtered?${query.toString()}`)
  return data || []
}

export const downloadPauseAll = async (): Promise<{ affected: number }> => {
  const { data } = await api.patch<{ affected: number }>('/downloads/pause-all')
  return data
}

export const downloadResumeAll = async (): Promise<{ affected: number }> => {
  const { data } = await api.patch<{ affected: number }>('/downloads/resume-all')
  return data
}

export const downloadBatchPause = async (ids: number[]): Promise<{ affected: number }> => {
  const { data } = await api.patch<{ affected: number }>('/downloads/batch/pause', { ids })
  return data
}

export const downloadBatchResume = async (ids: number[]): Promise<{ affected: number }> => {
  const { data } = await api.patch<{ affected: number }>('/downloads/batch/resume', { ids })
  return data
}

export const downloadBatchDelete = async (ids: number[]): Promise<{ deleted: number; total: number }> => {
  const { data } = await api.post<{ deleted: number; total: number }>('/downloads/batch/delete', { ids })
  return data
}

export const downloadTrackers = async (): Promise<string[]> => {
  const { data } = await api.get<string[]>('/downloads/trackers')
  return data || []
}

export const downloadCategories = async (): Promise<string[]> => {
  const { data } = await api.get<string[]>('/downloads/categories')
  return data || []
}

export const downloadRecheck = async (id: number): Promise<DownloadEntry> => {
  const { data } = await api.post<DownloadEntry>(`/downloads/${id}/recheck`)
  return data
}

// DownloadDetails: row do download + lista completa de arquivos do torrent
// + sizes reais (sparse vs apparent). Backend só preenche torrent quando o
// info_hash está active no streamer; null quando dropado (post-completed
// sem seed).
export interface DownloadDetails {
  download: DownloadEntry
  file: { apparent: number; onDisk: number; exists: boolean }
  torrent: TorrentInfo | null
}
export const downloadDetails = async (id: number): Promise<DownloadDetails> => {
  const { data } = await api.get<DownloadDetails>(`/downloads/${id}/details`)
  return data
}

export interface PromoteDestination {
  name: string
  path: string
}

// Move um download concluído para o diretório compartilhado (JACKUI_SHARED_DIR
// no servidor) ou outro destino (targetBase), opcionalmente numa subpasta. Após
// mover, opcionalmente continua seedando (keepSeeding=true).
export const downloadPromote = async (
  id: number,
  opts: { keepSeeding: boolean; targetSubdir?: string; targetBase?: string },
): Promise<DownloadEntry> => {
  const { data } = await api.post<DownloadEntry>(`/downloads/${id}/promote`, opts)
  return data
}

// Promove N downloads pra mesma subpasta de destino. Falhas individuais não
// abortam o batch; retorna { promoted, failed }.
export interface PromoteBatchResult {
  promoted: DownloadEntry[]
  failed: { id: number; error: string }[]
}
export const downloadPromoteBatch = async (
  ids: number[],
  opts: { keepSeeding: boolean; targetSubdir?: string; targetBase?: string; renameIA?: boolean },
): Promise<PromoteBatchResult> => {
  const { data } = await api.post<PromoteBatchResult>('/downloads/promote', { ids, ...opts })
  return data
}

export const downloadPromotePreview = async (
  ids: number[],
  opts: { targetSubdir?: string; targetBase?: string },
): Promise<{ previews: PromotePreviewEntry[] }> => {
  const { data } = await api.post<{ previews: PromotePreviewEntry[] }>('/downloads/promote/preview', {
    ids,
    ...opts,
  })
  return data
}

// Lista subpastas no {base}/<path> pra alimentar o navegador da PromoteModal.
// base vazio = sharedDir (default). 
export const downloadPromoteBrowse = async (path: string, base?: string): Promise<{ dirs: string[]; path: string }> => {
  const params = new URLSearchParams({ path })
  if (base) params.set('base', base)
  const { data } = await api.get<{ dirs: string[]; path: string }>(
    `/downloads/promote/browse?${params}`,
  )
  return data
}

// Lista destinos de promoção disponíveis (nome + path).
export const fetchPromoteDestinations = async (): Promise<PromoteDestination[]> => {
  const { data } = await api.get<PromoteDestination[]>('/promote/destinations')
  return data
}

// Para de seedar sem mover o arquivo.
export const downloadStopSeed = async (id: number): Promise<void> => {
  await api.post(`/downloads/${id}/stop-seed`)
}

// Converte uma URL de arquivo .torrent para link magnet no backend.
export const convertTorrentToMagnet = async (
  url: string,
): Promise<{ magnet: string; infoHash: string; name: string }> => {
  const { data } = await api.get<{ magnet: string; infoHash: string; name: string }>(
    `/convert/torrent-to-magnet?url=${encodeURIComponent(url)}`,
  )
  return data
}

// Retorna a URL para converter magnet em arquivo .torrent para download.
export const convertMagnetToTorrentUrl = (magnet: string): string => {
  return `/api/convert/magnet-to-torrent?magnet=${encodeURIComponent(magnet)}`
}

export interface HLSSessionSnapshot {
  key: string
  codec: string
  segmentsReady: number
  startedAt: string
  lastActivity: string
  pid: number
}

export interface GPUInfo {
  type: 'nvidia' | 'vaapi' | 'none'
  gpu: number
  vramUsed?: number
  vramTotal?: number
}

export interface ActiveTranscodesResponse {
  sessions: HLSSessionSnapshot[]
  gpu: GPUInfo
}

export const fetchActiveTranscodes = async (): Promise<ActiveTranscodesResponse> => {
  const { data } = await api.get<ActiveTranscodesResponse>('/transcode/active')
  return data
}

export const killTranscodeSession = async (key: string): Promise<void> => {
  await api.delete(`/transcode/active/${encodeURIComponent(key)}`)
}

export default api

