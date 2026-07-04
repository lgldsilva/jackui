// HTTP core (axios instance, auth/incognito interceptors, token helpers) vive em
// ./http. client.ts re-exporta tudo num barrel pra que os ~40 call-sites
// (`import { api, withToken, ... } from '../api/client'`) sigam funcionando
// enquanto o módulo é dividido por domínio. Ver [[feedback-no-god-files]].
import { api } from './http'
export { api }
// Re-export puro (símbolos não usados internamente aqui) — `export…from` evita o
// smell S7763 do Sonar (importar só pra re-exportar).
export { withToken, fetchMediaToken, clearMediaToken, MAGNET_PREFIX } from './http'

// Domínios extraídos (re-exportados pra manter os call-sites em '../api/client').
export * from './auth'
export * from './downloads'
export * from './tmdb'
export * from './watchlists'
export * from './library'
export * from './playlists'
export * from './push'
export * from './stats'
export * from './stream'
export * from './local'
export * from './subtitles'

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

export const clearHistory = async (): Promise<void> => {
  await api.delete('/history')
}

export const deleteHistoryEntry = async (q: string): Promise<void> => {
  await api.delete(`/history?q=${encodeURIComponent(q)}`)
}

/** Picks the best available source for streaming — prefers magnet, falls back to .torrent URL. */
export function pickTorrentSource(r: SearchResult): string {
  return r.magnetUri || r.link || ''
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
  running?: boolean   // a benchmark run is in flight right now (may have been started
                       // from another tab, or from a request that already timed out
                       // client-side — the run keeps going server-side regardless)
  startedAt?: string   // RFC3339; present only when running
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
// Stops whichever benchmark run is currently in flight (see AIStatus.running).
// Whatever was already measured before the cancel is kept — the backend saves
// each slot's result as it finishes, not just at the end of the whole run.
export const cancelAIBenchmark = async (): Promise<void> => {
  await api.post('/ai/benchmark/cancel')
}
export const saveAICases = async (cases: AIBenchmarkCase[]): Promise<AIBenchmarkCase[]> => {
  const { data } = await api.put<{ cases: AIBenchmarkCase[] }>('/ai/benchmark/cases', { cases })
  return data.cases || []
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

export default api
