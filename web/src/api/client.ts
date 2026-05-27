import axios from 'axios'

// Exported so diagnostic shippers (lib/diag.ts) can post without re-wiring
// auth interceptors. Don't reach into this directly from feature code — keep
// using the helper functions below; this is for cross-cutting infra only.
export const api = axios.create({
  baseURL: '/api',
  headers: {
    'Content-Type': 'application/json',
  },
})

// withToken appends the current access token as a query param.
// Used for URLs that go into <video src>/<track src> where headers can't be set.
// The middleware accepts `?token=` as a fallback to Authorization: Bearer.
export function withToken(url: string): string {
  const token = localStorage.getItem('jackui:auth.access')
  if (!token) return url
  const cleaned = token.replace(/^"|"$/g, '') // localStorage values are JSON-stringified
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}token=${encodeURIComponent(cleaned)}`
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
}

// streamMetadata returns a cached TorrentInfo snapshot if the server has seen
// this hash before. Use in parallel with streamAdd to render the file list
// instantly while the torrent client is still resolving peers.
export const streamMetadata = async (hash: string): Promise<TorrentInfo | null> => {
  try {
    const { data, status } = await api.get<TorrentInfo>(`/stream/metadata/${hash}`, { validateStatus: () => true })
    return status === 200 ? data : null
  } catch {
    return null
  }
}

export const streamAdd = async (magnet: string): Promise<TorrentInfo> => {
  const { data } = await api.post<TorrentInfo>('/stream/add', { magnet })
  return data
}

/** Picks the best available source for streaming — prefers magnet, falls back to .torrent URL. */
export function pickTorrentSource(r: SearchResult): string {
  return r.magnetUri || r.link || ''
}

export const streamInfo = async (hash: string): Promise<TorrentInfo> => {
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
  title: string
  year: number
  posterUrl: string
  overview: string
  voteAverage: number
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

export const streamFileURL = (hash: string, fileIdx: number): string =>
  withToken(`/api/stream/${hash}/${fileIdx}`)

// streamThumbnailURL returns the URL of a single JPEG frame captured at `atSeconds`
// in the file. Used by the player progress-bar hover preview.
export const streamThumbnailURL = (hash: string, fileIdx: number, atSeconds: number): string =>
  withToken(`/api/stream/thumb/${hash}/${fileIdx}?at=${Math.max(0, Math.floor(atSeconds))}`)

// streamArtworkURL returns the URL of embedded cover-art extracted by the server.
// Returns 204 server-side if the file has no embedded picture.
export const streamArtworkURL = (hash: string, fileIdx: number): string =>
  withToken(`/api/stream/artwork/${hash}/${fileIdx}`)

// streamPlaylistM3UURL returns an absolute URL to a downloadable .m3u that points
// back to the stream. Used by the "Open in VLC" button — universal across desktop
// VLC, iOS VLC, and Android VLC, unlike the proprietary vlc:// scheme.
export const streamPlaylistM3UURL = (hash: string, fileIdx: number, transcode?: 'h264' | 'hevc'): string => {
  const base = `/api/stream/playlist/${hash}/${fileIdx}`
  const url = withToken(base)
  return transcode ? `${url}${url.includes('?') ? '&' : '?'}transcode=${transcode}` : url
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

export const libraryUpdateResume = async (id: number, resumeSeconds: number, durationSeconds = 0): Promise<void> => {
  await api.patch(`/library/${id}`, { resumeSeconds, durationSeconds })
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

export const streamSidecars = async (hash: string, fileIdx: number): Promise<SidecarSubtitle[]> => {
  const { data } = await api.get<SidecarSubtitle[]>(`/stream/sidecars/${hash}/${fileIdx}`)
  return data
}
export const streamSidecarURL = (hash: string, fileIdx: number): string =>
  withToken(`/api/stream/sidecar/${hash}/${fileIdx}`)

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

export const subtitleDownloadURL = (fileId: string): string =>
  withToken(`/api/subtitles/download/${fileId}`)

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
  const { data } = await api.get<StreamProbe>(`/stream/probe/${hash}/${fileIdx}`)
  return data
}

export const streamSubtrackURL = (hash: string, fileIdx: number, trackIdx: number): string =>
  withToken(`/api/stream/subtrack/${hash}/${fileIdx}/${trackIdx}`)

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

export const streamTranscodeURL = (hash: string, fileIdx: number, opts: TranscodeOpts): string => {
  const p = new URLSearchParams()
  if (opts.audio !== undefined) p.set('audio', String(opts.audio))
  if (opts.video) p.set('video', opts.video)
  if (opts.acodec) p.set('acodec', opts.acodec)
  if (opts.burn !== undefined) p.set('burn', String(opts.burn))
  return withToken(`/api/stream/transcode/${hash}/${fileIdx}?${p}`)
}

/**
 * HLS master playlist URL. Used as the Safari/iOS fallback path: Safari's
 * MSE pipeline refuses progressive fragmented MP4 over chunked transfer but
 * natively plays HLS (.m3u8 + .ts) via `<video src>`. Apple's documented
 * streaming format; the only thing Safari treats as a first-class video
 * source. Jellyfin, Plex, Emby all do the same routing.
 */
export const streamHLSMasterURL = (hash: string, fileIdx: number): string =>
  withToken(`/api/stream/hls/${hash}/${fileIdx}/index.m3u8`)

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

// Direct file URL with auth token in query string (http.ServeFile handles Range
// natively; <video src> can hit this directly).
export const localFileURL = (mount: string, path: string): string => {
  const params = new URLSearchParams({ mount, path })
  return withToken(`/api/local/file?${params}`)
}

// ─── Background downloads ──────────────────────────────────────────────────
// Full-file (not streaming) download queue. Backed by anacrolix file.Download
// which prioritises all pieces; protected from cache eviction until removed.

export interface DownloadEntry {
  id: number
  userId: number
  infoHash: string
  fileIndex: number
  filePath: string
  fileSize: number
  name: string
  magnet: string
  status: 'queued' | 'downloading' | 'completed' | 'failed' | 'paused'
  bytesDownloaded: number
  progress: number
  startedAt?: string | null
  completedAt?: string | null
  error?: string
  createdAt: string
}

export interface DownloadCreateParams {
  infoHash: string
  fileIndex: number
  magnet: string
  name: string
  filePath: string
  fileSize: number
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

export default api
