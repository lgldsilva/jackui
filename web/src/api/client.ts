// HTTP core (axios instance, auth/incognito interceptors, token helpers) vive em
// ./http. client.ts re-exporta tudo num barrel pra que os ~40 call-sites
// (`import { api, withToken, ... } from '../api/client'`) sigam funcionando
// enquanto o módulo é dividido por domínio. Ver [[feedback-no-god-files]].
import { api, withToken, fetchMediaToken, MAGNET_PREFIX } from './http'
import { downloadCreate, WHOLE_TORRENT_FILE_INDEX, type DownloadEntry } from './downloads'
import { audioCapsParam } from '../lib/audioCaps'
export { api, withToken, fetchMediaToken, MAGNET_PREFIX }

// Domínios extraídos (re-exportados pra manter os call-sites em '../api/client').
export * from './auth'
export * from './downloads'
export * from './tmdb'
export * from './watchlists'
export * from './library'
export * from './playlists'
export * from './push'
export * from './stats'

export type Quality = {
  readonly resolution?: string
  readonly codec?: string
  readonly source?: string
  readonly audio?: readonly string[]
  readonly group?: string
  readonly year?: number
  readonly season?: number
  readonly episode?: number
  readonly hdr?: boolean
  readonly dv?: boolean
  readonly tenBit?: boolean
  readonly repack?: boolean
  readonly proper?: boolean
  readonly extended?: boolean
  readonly remux?: boolean
  readonly multi?: boolean
  readonly dubbed?: boolean
  readonly subbed?: boolean
}

export type SearchResult = {
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

export type Indexer = {
  readonly id: string
  readonly name: string
  readonly description: string
  readonly language: string
  readonly type: string
  readonly configured: boolean
}

export type DownloadClient = {
  readonly id: string
  readonly name: string
  readonly type: string
  readonly default: boolean
}

export type DownloadClientFull = DownloadClient & {
  readonly url: string
  readonly username: string
  readonly password: string
}

export type JackettConfig = {
  url: string
  // GET never returns the real key (secret); it comes back empty with
  // apiKeySet=true when one is stored. Sending empty on PUT keeps the current.
  apiKey: string
  apiKeySet?: boolean
}

export type AppConfig = {
  port: number
  jackett: JackettConfig
  downloadClients: DownloadClientFull[]
  envOverrides?: Record<string, string>
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

export type SystemStatus = {
  status: string
  version: string
  commit: string
  buildTime: string
  goVersion: string
  db: string
  time: string
}

// Build metadata served by GET /status (a public ROOT endpoint, not under /api).
// Plain fetch: it needs no auth header and lives outside the api baseURL.
export const getStatus = async (): Promise<SystemStatus> => {
  const res = await fetch('/status')
  if (!res.ok) throw new Error(`status ${res.status}`)
  return res.json()
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

export const testJackettConnection = async (
  creds?: { url: string; apiKey: string },
): Promise<{ success: boolean; message?: string; error?: string }> => {
  // Pass creds to validate them BEFORE saving (an empty apiKey reuses the stored
  // one server-side); omit them to re-test the currently-saved config.
  const { data } = await api.post('/config/test', creds)
  return data
}

export type HistoryEntry = {
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

export type CachedSearchResult = SearchResult & {
  query?: string // origin query when returned by SearchCache
}

export const searchCache = async (q: string): Promise<CachedSearchResult[]> => {
  const { data } = await api.get<CachedSearchResult[]>(`/history/cache?q=${encodeURIComponent(q)}`)
  return data
}

// Response from POST /api/history/:id/refresh. `cached=true` means the swarm
// numbers came from the 5min TTL cache (no fresh Jackett call was made).
export type HistoryRefreshResponse = {
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

export type StreamFile = {
  index: number
  path: string
  size: number
  isVideo: boolean
  downloaded: number
  progress: number
  priority: 'none' | 'low' | 'normal' | 'high'
}

export type TorrentInfo = {
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
  // stalled is set for local files served from a slow/rclone mount: the stream
  // is open but no bytes have flowed recently (waiting on the network).
  stalled?: boolean
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
  const bytes = new TextEncoder().encode(json)
  let bin = ''
  for (const byte of bytes) bin += String.fromCodePoint(byte)
  const b64 = btoa(bin)
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replaceAll('=', '')
  return LOCAL_PREFIX + b64
}

export function parseLocalHash(hash: string): { mount: string; path: string } | null {
  if (!isLocalHash(hash)) return null
  try {
    let b64 = hash.slice(LOCAL_PREFIX.length).replaceAll('-', '+').replaceAll('_', '/')
    while (b64.length % 4) b64 += '='
    const raw = atob(b64)
    const rawBytes = new Uint8Array(raw.length)
    for (let i = 0; i < raw.length; i++) rawBytes[i] = raw.codePointAt(i) ?? 0
    const json = new TextDecoder().decode(rawBytes)
    const parsed = JSON.parse(json)
    if (typeof parsed.mount === 'string' && typeof parsed.path === 'string') return parsed
    return null
  } catch {
    return null
  }
}

// localViewAsUser holds the admin "view as user" selection. When set (admin
// only — the backend re-validates the role before honoring it), every
// /api/local/* call carries ?user=<username> so the server scopes to that
// user's subdir instead of the admin's own. Empty = operate on own space.
let localViewAsUser = ''
export function setLocalViewAsUser(username: string): void {
  localViewAsUser = username || ''
}
export function getLocalViewAsUser(): string {
  return localViewAsUser
}

// appendViewAs adds the ?user= override to a URLSearchParams when an admin has
// selected another user to view.
function appendViewAs(p: URLSearchParams): URLSearchParams {
  if (localViewAsUser) p.set('user', localViewAsUser)
  return p
}

// withViewAs appends ?user= to an already-built URL (media URLs returned by the
// backend like localPlay's url, and the POST endpoints that take no params).
function withViewAs(url: string): string {
  if (!localViewAsUser) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}user=${encodeURIComponent(localViewAsUser)}`
}

function localQS(mount: string, path: string): string {
  const base = `mount=${encodeURIComponent(mount)}&path=${encodeURIComponent(path)}`
  return localViewAsUser ? `${base}&user=${encodeURIComponent(localViewAsUser)}` : base
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

// resolveTorrentInfo busca o metadata mais barato primeiro (snapshot em cache
// por infoHash) e só cai pro streamAdd (ativa o torrent) quando frio.
export const resolveTorrentInfo = async (magnet: string, infoHash?: string): Promise<TorrentInfo> => {
  if (infoHash) {
    const cached = await streamMetadata(infoHash)
    if (cached?.files?.length) return cached
  }
  return streamAdd(magnet)
}

// queueAllTorrentFiles enfileira o torrent COMPLETO como UM item da fila
// (fileIndex = WHOLE_TORRENT_FILE_INDEX). A versão anterior criava 1 download
// POR arquivo via Promise.allSettled — num torrent de 5699 arquivos o navegador
// estoura o limite de requests em voo (net::ERR_INSUFFICIENT_RESOURCES) e a
// fila vira milhares de itens individuais. Agora é 1 request, 1 row, 1 slot no
// scheduler; o worker faz t.DownloadAll() e agrega o progresso.
// downloadCreate continua idempotente por (infoHash, fileIndex).
export const queueAllTorrentFiles = async (
  info: TorrentInfo, magnet: string, name: string, tracker?: string, category?: string,
): Promise<DownloadEntry> =>
  downloadCreate({
    infoHash: info.infoHash, fileIndex: WHOLE_TORRENT_FILE_INDEX, magnet, name,
    filePath: '', fileSize: info.totalSize, tracker, category,
  })

export const streamAdd = async (magnet: string, kind?: 'audio' | 'video'): Promise<TorrentInfo> => {
  // Local files: magnet carries the pseudo-hash. Synthesize TorrentInfo from
  // /api/local/play + /api/local/probe without touching the torrent client.
  const localHash = extractHashFromMagnet(magnet)
  if (localHash && isLocalHash(localHash)) return synthesizeLocalInfo(localHash)
  // kind (from the player's detectKind) lets the server classify the library row
  // as audio/video for Continue Watching + stats. Omitted → server leaves it.
  const { data } = await api.post<TorrentInfo>('/stream/add', kind ? { magnet, kind } : { magnet })
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

// localStreamInfo is the poll-time refresh for a local file. Unlike
// synthesizeLocalInfo it does NOT re-run localPlay (ffprobe) every tick — it
// reuses the URL resolved on the first add and only hits the cheap
// transfer-status endpoint, mapping throughput onto the TorrentInfo the player
// already understands (ratePerSec→downRate, bytesRead/size→progress). Falls
// back to a full synthesize when the URL hasn't been resolved yet.
async function localStreamInfo(hash: string): Promise<TorrentInfo> {
  const loc = parseLocalHash(hash)
  if (!loc) throw new Error('invalid local hash')
  if (!localPlayableURLCache.has(hash)) return synthesizeLocalInfo(hash)

  const st = await localTransferStatus(loc.mount, loc.path)
  const size = st?.size ?? 0
  const bytesRead = st?.bytesRead ?? 0
  const progress = size > 0 ? Math.min(1, bytesRead / size) : 1
  const name = loc.path.split('/').pop() || loc.path
  const file: StreamFile = {
    index: 0,
    path: loc.path,
    size,
    isVideo: !/\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i.test(loc.path),
    downloaded: bytesRead,
    progress,
    priority: 'normal',
  }
  return {
    infoHash: hash,
    name,
    totalSize: size,
    files: [file],
    peers: 0,
    seeders: 0,
    downRate: st?.ratePerSec ?? 0,
    upRate: 0,
    progress,
    primaryFile: 0,
    stalled: st?.stalled ?? false,
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
  if (isLocalHash(hash)) return localStreamInfo(hash)
  const { data } = await api.get<TorrentInfo>(`/stream/info/${hash}`)
  return data
}

export const streamDrop = async (hash: string): Promise<void> => {
  await api.delete(`/stream/${hash}`)
}

// Viewer "lease": the player opens one while watching and closes it on
// unmount/file-change. While ≥1 lease is open the torrent keeps streaming; the
// last close drops it after a short grace period (so multiple viewers of the
// same stream survive one of them closing, and a stream-only torrent stops
// seeding promptly instead of lingering until the idle reaper).
export const streamViewerOpen = async (hash: string): Promise<void> => {
  await api.post(`/stream/${hash}/viewer`)
}

export const streamViewerClose = async (hash: string): Promise<void> => {
  await api.delete(`/stream/${hash}/viewer`)
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
export type StreamLimits = {
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
export const streamPrefetch = async (hash: string, fileIdx: number): Promise<void> => {
  try {
    await api.post(`/stream/prefetch/${hash}/${fileIdx}`)
  } catch {
    // Silent: prefetch is best-effort, never block playback if it fails.
  }
}

// ── AI title-identification benchmark (admin) ────────────────────────────────
// Per-task accuracy breakdown (keyed by task id: "rename" | "identify" | "schedule").
export type AITaskScore = {
  accuracy: number // 0..1 over this task's cases
  samples: number
  scored: number
}
export type AISlotScore = {
  slotId: string
  provider: string
  model: string
  accuracy: number      // 0..1 — mean of the per-task accuracies
  avgLatencyMs: number
  composite: number
  samples: number
  costPer1M?: number     // blended USD per 1M tokens (0 = free); factored into the score
  failureReason?: string
  incomplete?: boolean   // some cases skipped (rate limit) → re-run via "Rodar faltantes"
  tasks?: Record<string, AITaskScore> // optional per-task breakdown (multi-task benchmark)
  // Durable run history (backend benchmark_history): whether the LAST run succeeded
  // or errored, whether the error persisted, and when it last succeeded. All
  // optional — empty/absent on legacy rows persisted before history existed.
  lastOutcome?: string         // 'ok' | 'incomplete' | 'error'
  lastError?: string           // failure reason of the last failing run (durable); '' once it succeeds again
  lastSuccessAt?: string       // RFC3339 of the last 'ok' run; absent = never succeeded
  lastRunAt?: string           // RFC3339 of the last run (any outcome)
  firstFailureAt?: string      // RFC3339 the current error streak began
  consecutiveFailures?: number // # of consecutive 'error' runs (0 = not in a streak)
}
// task: which AI task the case measures — "rename" (default, omitted), "identify"
// or "schedule". Optional for retrocompat: a case without it is the rename task.
export type AIBenchmarkCase = { raw: string; expect: string; task?: string }
export type AICostConfig = {
  maxCostPer1M: number // teto p/ incluir pagos no benchmark ($/1M); 0 = só grátis
  kwhPrice: number     // tarifa de energia (USD/kWh); 0 = local fica grátis
  localWatts: number   // potência da GPU sob carga (W)
}
export type AIStatus = {
  enabled: boolean
  chain: { id: string; provider: string; model: string }[]
  results: AISlotScore[]
  cases: AIBenchmarkCase[]
  cost: AICostConfig
  providers?: string[]
}

export const aiBenchmarkStatus = async (): Promise<AIStatus> => {
  const { data } = await api.get<AIStatus>('/ai/benchmark')
  return data
}
export const runAIBenchmark = async (provider?: string, model?: string): Promise<AISlotScore[]> => {
  let url = '/ai/benchmark'
  const params = new URLSearchParams()
  if (provider) params.append('provider', provider)
  if (model) params.append('model', model)
  const query = params.toString()
  if (query) url += `?${query}`

  const { data } = await api.post<{ results: AISlotScore[] }>(url)
  return data.results || []
}
// Updates the cost knobs (ceiling, energy tariff, GPU watts) live + persisted.
export const saveAICostConfig = async (cost: AICostConfig): Promise<AICostConfig> => {
  const { data } = await api.put<{ cost: AICostConfig }>('/ai/settings', cost)
  return data.cost
}
// Re-runs ONLY the models left incomplete (cases skipped by a rate limit) and
// merges them in — trigger it later so the retry lands outside the limit window.
export const runAIBenchmarkIncomplete = async (): Promise<AISlotScore[]> => {
  const { data } = await api.post<{ results: AISlotScore[] }>('/ai/benchmark/rerun-incomplete')
  return data.results || []
}
export const saveAICases = async (cases: AIBenchmarkCase[]): Promise<AIBenchmarkCase[]> => {
  const { data } = await api.put<{ cases: AIBenchmarkCase[] }>('/ai/benchmark/cases', { cases })
  return data.cases || []
}

// ── Swarm health (seeds / availability for cards) ────────────────────────────
export type StreamHealth = {
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

// TrackerScrape is one tracker's reported swarm size (BEP 48). `tracker` is the
// host only — the server never exposes a private tracker's passkey.
export type TrackerScrape = {
  tracker: string
  seeders: number
  leechers: number
  ok: boolean
}

// streamTrackers scrapes the torrent's trackers and returns per-tracker swarm
// sizes for the info panel. Bounded server-side (~8s). Empty on error.
export const streamTrackers = async (hash: string, magnet?: string): Promise<TrackerScrape[]> => {
  try {
    const params = new URLSearchParams()
    if (magnet) params.set('magnet', magnet)
    const { data } = await api.get<TrackerScrape[]>(`/stream/trackers/${hash}?${params.toString()}`)
    return data || []
  } catch {
    return []
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

export type CacheEntry = {
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

export type StreamCacheStats = {
  dataDir: string
  totalSize: number
  maxSize: number
  numActive: number
  entries: CacheEntry[]
  /** Filesystem usage of the disk hosting dataDir (0 when statfs unavailable). */
  diskFree: number
  diskTotal: number
  /** Lifetime LRU eviction counters since the server started. */
  evictedCount: number
  evictedBytes: number
  lastEvictionAt?: string
}

export const streamCacheStats = async (): Promise<StreamCacheStats> => {
  const { data } = await api.get<StreamCacheStats>('/stream/cache')
  return data
}

export const streamCacheClear = async (entry?: string): Promise<void> => {
  const url = entry ? `/stream/cache?entry=${encodeURIComponent(entry)}` : '/stream/cache'
  await api.delete(url)
}

// ─── Streamer & Performance settings ──────────────────────────────────────
// Rate limits e readahead são aplicados AO VIVO; storage/conns/peers/hashers/
// cache exigem reiniciar o processo (restartRequired na resposta do PUT).
export type StorageBackend = 'file' | 'mmap'

export type StreamSettings = {
  maxDownloadRate: number // bytes/seg, 0=ilimitado
  maxUploadRate: number
  readaheadMB: number
  storageBackend: StorageBackend
  maxConnsPerTorrent: number
  halfOpenConns: number
  peersHighWater: number
  pieceHashers: number
  maxCacheGB: number
}

export type StreamSettingsDefaults = {
  readaheadMB: number
  maxConnsPerTorrent: number
  halfOpenConns: number
  peersHighWater: number
  pieceHashers: number
}

export type StreamSettingsResponse = StreamSettings & { defaults: StreamSettingsDefaults }

export const getStreamSettings = async (): Promise<StreamSettingsResponse> => {
  const { data } = await api.get<StreamSettingsResponse>('/stream/settings')
  return data
}

export const updateStreamSettings = async (
  s: StreamSettings,
): Promise<{ restartRequired: boolean }> => {
  const { data } = await api.put<{ restartRequired: boolean }>('/stream/settings', s)
  return data
}

export type StreamRate = {
  downRate: number
  upRate: number
  activeTorrents: number
}

export const streamRate = async (): Promise<StreamRate> => {
  const { data } = await api.get<StreamRate>('/stream/rate')
  return data
}

// ─── Favorites ────────────────────────────────────────────────────────────

export type StreamFavorite = {
  name: string
  infoHash: string
  magnet: string
  userId: number
  favoritedAt: string
  reason: 'manual' | 'auto-5min'
  folderId: number | null  // nil = root level; otherwise nested in a FavoriteFolder
  totalSize?: number       // bytes, from the metadata cache; absent/0 = unknown
  seeders?: number         // last probed swarm seeders; absent = never probed
}

/** Favorite folder for organizing the user's favorites tree. */
export type FavoriteFolder = {
  id: number
  userId: number
  name: string
  parentId: number | null  // null = root-level folder
  position: number
  hidden?: boolean         // hidden folders only show after the UI's easter egg
  createdAt: string
}

// ─── Sidecar subtitles inside torrent ──────────────────────────────────────

export type SidecarSubtitle = {
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

export const favoritesList = async (includeHidden = false): Promise<StreamFavorite[]> => {
  const { data } = await api.get<StreamFavorite[]>(`/stream/favorites${includeHidden ? '?includeHidden=1' : ''}`)
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
export type ImportResult = { infoHash: string; name: string; magnet: string }
export const streamImport = async (
  payload: { magnet?: string; torrentB64?: string; name?: string; folderId?: number | null },
): Promise<ImportResult> => {
  const { data } = await api.post<ImportResult>('/stream/import', payload)
  return data
}

// ─── Favorite folders (tree organization) ──────────────────────────────
// Folders live alongside favorites; each favorite can have an optional
// folder_id. Server prevents cycles when moving subfolders.

export const folderList = async (includeHidden = false): Promise<FavoriteFolder[]> => {
  const { data } = await api.get<FavoriteFolder[]>(`/stream/favorites/folders${includeHidden ? '?includeHidden=1' : ''}`)
  return data || []
}

export const folderCreate = async (name: string, parentId: number | null = null, hidden = false): Promise<FavoriteFolder> => {
  const { data } = await api.post<FavoriteFolder>('/stream/favorites/folders', { name, parentId, hidden })
  return data
}

// folderSetHidden flips a folder's hidden curtain (PATCH hidden).
export const folderSetHidden = async (id: number, hidden: boolean): Promise<void> => {
  await api.patch(`/stream/favorites/folders/${id}`, { hidden })
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

export type Subtitle = {
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

export type AutoSubtitlesResponse = {
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

export type MediaTrack = {
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

export type MediaChapter = {
  index: number
  startSec: number
  endSec?: number
  title?: string
}

export type StreamProbe = {
  audio: MediaTrack[]
  subtitles: MediaTrack[]
  chapters?: MediaChapter[]
  // Decisão de transcode vinda do backend (ffprobe), navegador-agnóstica:
  // MKV/HEVC/AV1/AC3/DTS não tocam direto em browser nenhum → HLS. O player
  // decide por isto, não pelo nome do arquivo.
  videoCodec?: string
  container?: string
  audioCodec?: string
  needsTranscode?: boolean
  transcodeReason?: string
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

// localSubtrackBlobURL fetches a LOCAL embedded subtitle track as a WebVTT blob
// URL, retrying while the server reports 503 {code:"extracting"}. Extracting an
// embedded sub demuxes the whole container, so on a large rclone file the server
// does it in the background and 503s until ready — we poll instead of letting a
// <track src> fail. Returns '' when cancelled or on a hard (non-503) failure.
export async function localSubtrackBlobURL(
  hash: string, fileIdx: number, trackIdx: number, token: string,
  isCancelled: () => boolean,
): Promise<string> {
  const url = streamSubtrackURL(hash, fileIdx, trackIdx, token)
  for (let attempt = 0; attempt < 40 && !isCancelled(); attempt++) {
    let res: Response
    try {
      res = await fetch(url)
    } catch {
      return '' // network error — give up quietly
    }
    if (res.status === 200) {
      const text = await res.text()
      if (isCancelled()) return ''
      return URL.createObjectURL(new Blob([text], { type: 'text/vtt' }))
    }
    if (res.status !== 503) return '' // hard failure (e.g. image-based sub)
    await new Promise(r => setTimeout(r, 10000)) // still extracting → wait & retry
  }
  return ''
}

// ─── Transcoding capabilities ──────────────────────────────────────────────

export type TranscodeEncoder = {
  id: string
  codec: string
  backend: string
  available: boolean
  functional: boolean
  benchFps?: number
  description: string
  error?: string
}

export type TranscodeCapabilities = {
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

export type TranscodeOpts = {
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
// CRITICAL: iOS/iPadOS força qualquer browser a usar WebKit, então Chrome/Edge/Firefox
// no iOS (CriOS/EdgiOS/FxiOS) rejeitam MP4 progressive chunked igual ao Safari e
// PRECISAM de HLS — mesmo com "CriOS"/"Edg" na UA. Por isso detecta iOS PRIMEIRO,
// antes da exclusão de Chrome/Edg. Também pega o iPadOS em "desktop mode", que se
// reporta como "Macintosh" mas é um dispositivo multi-touch.
export function isSafariBrowser(): boolean {
  if (typeof navigator === 'undefined') return false
  const ua = navigator.userAgent
  const iOS = /iPhone|iPad|iPod/.test(ua) ||
    (/Macintosh/.test(ua) && typeof navigator !== 'undefined' && navigator.maxTouchPoints > 1)
  if (iOS) return true
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

export type LocalMount = { name: string; path: string; userSubpath?: boolean; restricted?: boolean; freeBytes?: number; totalBytes?: number; cacheable?: boolean }

// ExternalMount is the full admin-side mount config (includes allowedUsers).
export type ExternalMount = {
  name: string
  path: string
  userSubpath?: boolean
  allowedUsers?: string[]
}

export const getMounts = async (): Promise<ExternalMount[]> => {
  const { data } = await api.get<ExternalMount[]>('/mounts')
  return data || []
}

export const updateMounts = async (mounts: ExternalMount[]): Promise<void> => {
  await api.put('/mounts', mounts)
}
export type LocalEntry = {
  name: string
  path: string       // relative to mount root
  isDir: boolean
  size: number
  modTime: string
  isPlayable: boolean
  childCount?: number // # of entries inside a directory (0/absent for files)
  locked?: boolean    // dir pinned (.keep) — "clean empty" never removes it
}

export const localMounts = async (): Promise<LocalMount[]> => {
  const { data } = await api.get<LocalMount[]>('/local/mounts')
  return data || []
}

export const localList = async (mount: string, path: string): Promise<LocalEntry[]> => {
  const params = appendViewAs(new URLSearchParams({ mount, path }))
  const { data } = await api.get<LocalEntry[]>(`/local/list?${params}`)
  return data || []
}

export const localDelete = async (mount: string, path: string): Promise<void> => {
  const params = appendViewAs(new URLSearchParams({ mount, path }))
  await api.delete(`/local/file?${params}`)
}

// localCleanEmptyDirs removes empty subdirectories under `path` (mount root when
// empty). Returns how many were deleted. Writable mount / admin only.
export const localCleanEmptyDirs = async (mount: string, path = ''): Promise<{ cleaned: number }> => {
  const params = appendViewAs(new URLSearchParams({ mount, path }))
  const { data } = await api.post<{ cleaned: number }>(`/local/clean-empty?${params}`)
  return data
}

// Duplicate detection: content-identical files (different names) under a folder.
export type DuplicateFile = { path: string; name: string; size: number; modTime: string }
export type DuplicateGroup = { hash: string; size: number; files: DuplicateFile[] }

// localDuplicates scans `path` (recursive) for byte-identical files. Read-only;
// can be slow on rclone (hashes file content) so the UI shows a spinner.
export const localDuplicates = async (mount: string, path = ''): Promise<DuplicateGroup[]> => {
  const { data } = await api.get<{ groups: DuplicateGroup[] }>(`/local/duplicates?${localQS(mount, path)}`)
  return data.groups || []
}

// localDeleteDuplicates removes the selected duplicate files (mount-root-relative
// paths from localDuplicates). Writable mount / admin only.
export const localDeleteDuplicates = async (mount: string, paths: string[]): Promise<{ deleted: number; errors: string[] }> => {
  const { data } = await api.post<{ deleted: number; errors: string[] }>(withViewAs('/local/duplicates/delete'), { mount, paths })
  return data
}

export type LocalUploadResult = { uploaded: string; path: string }

// localUpload streams a file to the destination folder via multipart/form-data.
// axios sets the multipart boundary automatically when handed a FormData; the
// auth interceptor injects the Bearer token. onProgress reports bytes for the
// progress bar; signal lets the caller cancel an in-flight transfer.
export const localUpload = async (
  mount: string,
  path: string,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
  signal?: AbortSignal,
): Promise<LocalUploadResult> => {
  const form = new FormData()
  form.append('file', file)
  const { data } = await api.post<LocalUploadResult>(`/local/upload?${localQS(mount, path)}`, form, {
    onUploadProgress: (e) => onProgress?.(e.loaded, e.total ?? file.size),
    signal,
  })
  return data
}

// PromoteItemResult is the per-item outcome of a batch promote/reclassify, keyed
// by the ORIGINAL (un-scoped) source path the UI sent — so the reclassify table
// can mark each row succeeded/failed.
export type PromoteItemResult = { path: string; ok: boolean; error?: string }

export type PromoteResult = {
  moved: number
  failed: number
  errors: { path: string; error: string }[]
  results?: PromoteItemResult[]
}

export const localPromote = async (
  mount: string,
  path: string,
  targetSubdir: string,
  targetBase?: string,
  renameIA?: boolean,
  paths?: string[],
  // overrides maps a source path → user-edited target RELATIVE to the base. The
  // backend re-sanitizes each (path traversal, unsafe chars, category reuse)
  // before honouring it; an invalid override silently falls back to the IA path.
  overrides?: Record<string, string>,
): Promise<PromoteResult> => {
  const { data } = await api.post<PromoteResult>(withViewAs('/local/promote'), {
    mount, path, paths, targetSubdir, targetBase, renameIA, overrides,
  })
  return data
}

export type PromotePreviewEntry = {
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
  // reusedFolder is set when the IA landed the item in an EXISTING destination
  // category folder (e.g. "Movies") instead of creating a near-duplicate.
  reusedFolder?: string
  error?: string
}

export const localPromotePreview = async (
  mount: string,
  path: string,
  targetSubdir: string,
  targetBase?: string,
  paths?: string[],
): Promise<{ previews: PromotePreviewEntry[] }> => {
  const { data } = await api.post<{ previews: PromotePreviewEntry[] }>(withViewAs('/local/promote/preview'), {
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
  const params = appendViewAs(new URLSearchParams({ mount, path, media_only: mediaOnly ? '1' : '0' }))
  const { data } = await api.get<{ entries: LocalEntry[]; total: number }>(`/local/walk?${params}`)
  return data
}

// localMove moves a file or directory from one mount to another (admin only).
// dstPath is the target directory; the source name is preserved. The move runs
// asynchronously server-side (202): it returns a jobId tracked by the global
// Transfers dock; the file lands once the job finishes.
export const localMove = async (
  srcMount: string,
  srcPath: string,
  dstMount: string,
  dstPath: string,
): Promise<{ moved?: string; jobId?: string; async?: boolean }> => {
  const { data } = await api.post(withViewAs('/local/move'), { srcMount, srcPath, dstMount, dstPath })
  return data ?? {}
}

// localRename renames a file/folder in place (new bare name, no path separators).
export const localRename = async (
  mount: string,
  path: string,
  newName: string,
): Promise<{ renamed: string; relinked: number }> => {
  const { data } = await api.post<{ renamed: string; relinked: number }>(
    withViewAs('/local/rename'),
    { mount, path, newName },
  )
  return data
}

// localSetFolderLock pins/unpins a folder so "clean empty" keeps it (.keep marker).
export const localSetFolderLock = async (mount: string, path: string, locked: boolean): Promise<void> => {
  await api.post(withViewAs('/local/lock'), { mount, path, locked })
}

// Direct file URL with auth token in query string (http.ServeFile handles Range
// natively; <video src> can hit this directly).
export const localFileURL = (mount: string, path: string): string => {
  const params = appendViewAs(new URLSearchParams({ mount, path }))
  return withToken(`/api/local/file?${params}`)
}

// Formats browsers can play natively without transcoding.
const NATIVE_VIDEO_EXTS = new Set(['.mp4', '.m4v', '.webm', '.mov'])

// localTranscodeURL returns a server-side transcode URL for formats browsers
// can't decode natively (MKV, AVI, WMV, etc.).
export const localTranscodeURL = (mount: string, path: string): string => {
  const params = appendViewAs(new URLSearchParams({ mount, path }))
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
  const params = appendViewAs(new URLSearchParams({ mount, path }))
  return withToken(`/api/local/thumb?${params}`)
}

// LocalPlaySource describes how the frontend should load a local file. The
// backend probes the file (ffprobe) and either tells us to direct-play it
// (browser-compatible container + codecs) or to load an HLS playlist that the
// transcode pipeline produces on demand. Mirrors the torrent-side decision so
// the player can stay codec-agnostic — it just sets <video src> to `url`.
export type LocalPlaySource = {
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
  const sp = new URLSearchParams({ mount, path })
  // Tell the server which non-universal audio codecs this browser can play
  // inline, so it transcodes (audio-only HLS) the ones it can't — Safari can't
  // do FLAC/OGG/Opus. Harmless on video files (the server ignores it there).
  const caps = audioCapsParam()
  if (caps) sp.set('acaps', caps)
  const params = appendViewAs(sp)
  const { data } = await api.get<LocalPlaySource>(`/local/play?${params}`)
  // The backend builds data.url (direct file or HLS playlist) without the
  // ?user= override; re-append it so the <video> request re-scopes correctly.
  return { ...data, url: withViewAs(data.url) }
}

// AudioMeta is the tag metadata for a local audio file (server reads ID3/Vorbis/
// MP4 tags via dhowden/tag and caches them). Empty fields → fall back to filename.
export type AudioMeta = {
  title: string
  artist: string
  album: string
  albumArtist: string
  genre: string
  year: number
  trackNumber: number
  discNumber: number
  hasCover: boolean
}

// localAudioMeta fetches cached tags for a local audio file. Best-effort: a
// parse failure returns empty fields (200), never throws server-side.
export const localAudioMeta = async (mount: string, path: string): Promise<AudioMeta> => {
  const params = appendViewAs(new URLSearchParams({ mount, path }))
  const { data } = await api.get<AudioMeta>(`/local/audio/meta?${params}`)
  return data
}

// streamAudioMeta fetches tags read from a file INSIDE a torrent (artist/album/
// year the filename usually omits). Best-effort: empty tags on the server side
// when the file can't be parsed, so the caller falls back to the filename.
export const streamAudioMeta = async (hash: string, fileIdx: number): Promise<AudioMeta> => {
  const { data } = await api.get<AudioMeta>(`/stream/audio/meta/${hash}/${fileIdx}`)
  return data
}

// localAudioCoverURL builds the <img> URL for a local audio file's embedded
// album art (204 when none). Carries ?token= because <img> can't set headers.
export const localAudioCoverURL = (mount: string, path: string, tokenOverride?: string): string => {
  const params = new URLSearchParams({ mount, path })
  return withViewAs(withToken(`/api/local/audio/cover?${params}`, tokenOverride))
}

// Lyrics mirrors the backend LrcLib proxy result. source="" means none found.
export type Lyrics = { synced: string; plain: string; source: string }

// lyricsGet resolves lyrics for a track via the backend LrcLib proxy. Best-effort.
export const lyricsGet = async (
  title: string, artist: string, album: string, durationSec: number,
): Promise<Lyrics> => {
  const sp = new URLSearchParams({ title })
  if (artist) sp.set('artist', artist)
  if (album) sp.set('album', album)
  if (durationSec > 0) sp.set('duration', String(Math.round(durationSec)))
  const { data } = await api.get<Lyrics>(`/lyrics?${sp}`)
  return data
}

// LocalCacheStatus is the "cache mark" for a local file: whether it's been
// pre-fetched to fast local disk (instant, seekable, EIO-proof playback).
export type LocalCacheStatus = {
  status: 'none' | 'queued' | 'copying' | 'ready' | 'error'
  size: number
  copied: number
  percent: number
  error?: string
  // True only when the file lives on a slow/remote mount (rclone/NFS/CIFS).
  // Files already on local disk are cacheable=false → the player hides the
  // cache button (there's nothing to pre-fetch — they're already fast).
  cacheable?: boolean
}

// localCacheStart enqueues a full-file copy of a local/rclone file to the local
// cache. localCacheStatus polls the progress; localCacheDelete drops the copy.
export const localCacheStart = async (mount: string, path: string): Promise<LocalCacheStatus> => {
  const { data } = await api.post<LocalCacheStatus>(`/local/cache?${localQS(mount, path)}`)
  return data
}
export const localCacheStatus = async (mount: string, path: string): Promise<LocalCacheStatus> => {
  const { data } = await api.get<LocalCacheStatus>(`/local/cache/status?${localQS(mount, path)}`)
  return data
}
// localCacheFolder enqueues a full-file copy of EVERY playable file under a
// folder (recursive) — pre-fetch a whole rclone/Drive series in one click.
export const localCacheFolder = async (mount: string, path: string): Promise<{ queued: number; cacheable: boolean }> => {
  const { data } = await api.post<{ queued: number; cacheable: boolean }>(`/local/cache/folder?${localQS(mount, path)}`)
  return data
}
export const localCacheDelete = async (mount: string, path: string): Promise<void> => {
  await api.delete(`/local/cache?${localQS(mount, path)}`)
}

// HiddenLocalPath mirrors the backend: a (mount, path) the user marked hidden.
export type HiddenLocalPath = { mount: string; path: string }

// localSetHidden marks (or unmarks) a local folder/file as hidden — it then
// drops out of the listing unless the global reveal curtain is open.
export const localSetHidden = async (mount: string, path: string, hidden: boolean): Promise<void> => {
  await api.post('/local/hidden', { mount, path, hidden })
}

// localListHidden returns the user's hidden local paths (to flag them when the
// curtain is open).
export const localListHidden = async (): Promise<HiddenLocalPath[]> => {
  const { data } = await api.get<HiddenLocalPath[]>('/local/hidden')
  return data
}

// LocalTransfer is the throughput snapshot for a playing local file, used to
// show "downloading X MB/s" / "waiting for data" — the rclone/Drive case where
// a play silently fetches over the network.
export type LocalTransfer = {
  key?: string
  bytesRead: number
  ratePerSec: number
  size: number
  active: boolean
  stalled: boolean
}

// localTransferStatus polls the read throughput for a playing local file. It is
// cheap (no ffprobe) so the player can call it every couple of seconds.
export const localTransferStatus = async (mount: string, path: string): Promise<LocalTransfer | null> => {
  try {
    const { data, status } = await api.get<LocalTransfer>(
      `/local/transfer-status?${localQS(mount, path)}`,
      { validateStatus: () => true },
    )
    return status === 200 ? data : null
  } catch {
    return null
  }
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

// Baixa um arquivo pela camada autenticada (axios injeta Authorization e faz
// refresh) e dispara o save via blob. Navegação direta (location.href) não
// envia o header, e as rotas de .torrent não estão na whitelist de ?token= —
// com auth ligada o usuário recebia um JSON 401 no lugar do arquivo.
export const downloadFileAuthenticated = async (path: string, filename: string): Promise<void> => {
  const { data } = await api.get<Blob>(path, { responseType: 'blob' })
  const blobUrl = URL.createObjectURL(data)
  const a = document.createElement('a')
  a.href = blobUrl
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(blobUrl), 10_000)
}

// Baixa o .torrent de um resultado: via link do indexer (proxy) ou convertendo
// o magnet. O nome vem do título do resultado.
export const downloadTorrentForResult = async (r: { title: string; link?: string; magnetUri?: string }): Promise<void> => {
  const filename = `${r.title || 'download'}.torrent`
  if (r.link) {
    await downloadFileAuthenticated(`/proxy/torrent?url=${encodeURIComponent(r.link)}`, filename)
    return
  }
  if (r.magnetUri) {
    await downloadFileAuthenticated(`/convert/magnet-to-torrent?magnet=${encodeURIComponent(r.magnetUri)}`, filename)
  }
}

export type HLSSessionSnapshot = {
  key: string
  codec: string
  segmentsReady: number
  startedAt: string
  lastActivity: string
  pid: number
}

export type GPUInfo = {
  type: 'nvidia' | 'vaapi' | 'none'
  gpu: number
  vramUsed?: number
  vramTotal?: number
}

export type ActiveTranscodesResponse = {
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

// ─── Electron local download ─────────────────────────────────────────────
// Uses the Electron IPC bridge to download a file from the Go server to the
// user's local machine (Save dialog → filesystem). Falls back to browser
// download (anchor element) when not in Electron.
// apiPath: relative path starting with /api/... (withToken() already applied).

// Fallback de navegador: dispara o download via <a download>. Compartilhado
// pelas duas funções de download local (sem duplicar — usa globalThis + remove()).
function browserAnchorDownload(apiPath: string, suggestedName: string): { success: true } {
  const a = document.createElement('a')
  a.href = apiPath.startsWith('http') ? apiPath : `${globalThis.location.origin}${apiPath}`
  a.download = suggestedName
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  a.remove()
  return { success: true }
}

export async function downloadLocalFile(
  apiPath: string,
  suggestedName: string,
  category?: string,
  mediaKind?: string,
): Promise<{ success?: boolean; cancelled?: boolean; error?: string; filePath?: string }> {
  if (globalThis.electronAPI) {
    return globalThis.electronAPI.downloadFile(apiPath, suggestedName, category, mediaKind)
  }
  return browserAnchorDownload(apiPath, suggestedName)
}

/** Asks the backend to classify a title into a category.
 *  Uses regex heuristics + optional AI. */
export type CategoryResult = {
  category: string   // "movies" | "tv" | "music" | "games" | "software" | "adult" | "other"
  label: string      // human-readable
  source: string     // "regex" | "ai" | "jackett" | "fallback"
  confidence: number // 0..1
}

export async function classifyCategory(
  title: string,
  jackettCategory?: string,
): Promise<CategoryResult> {
  const p = new URLSearchParams({ title })
  if (jackettCategory) p.set('jackett_category', jackettCategory)
  const { data } = await api.get<CategoryResult>(`/classify?${p}`)
  return data
}

/** Downloads directly to the configured Electron folder with automatic
 *  categorization (Movies/TV/Music/…). Falls back to showSaveDialog when
 *  no folder is configured, or to browser anchor when not in Electron. */
export async function downloadLocalFileDirect(
  apiPath: string,
  suggestedName: string,
  category?: string,
  mediaKind?: string,
): Promise<{ success?: boolean; error?: string; filePath?: string }> {
  if (globalThis.electronAPI) {
    return globalThis.electronAPI.downloadFileDirect(apiPath, suggestedName, category, mediaKind)
  }
  return browserAnchorDownload(apiPath, suggestedName)
}

export default api

