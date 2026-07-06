// Streaming de torrents (anacrolix → HTTP com Range): add/probe/info/health/art,
// favoritos, controles estilo Transmission e os URL builders (/api/stream/*).
// Arquivos locais são detectados pelo pseudo info-hash e roteados pro ./local —
// o PlayerModal não distingue torrent de local. Extraído de client.ts (#417).
import { api, withToken } from './http'
import { extractInfoHashFromMagnet } from '../lib/magnet'
import { downloadCreate, WHOLE_TORRENT_FILE_INDEX, type DownloadEntry } from './downloads'
import {
  isLocalHash,
  parseLocalHash,
  localQS,
  synthesizeLocalInfo,
  localStreamInfo,
  localResolvedURL,
  type AudioMeta,
} from './local'

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
  // Cumulative payload byte counters. bytesUploaded is per-SESSION (resets when
  // the torrent is re-added). Absent on older callers → treat as 0.
  bytesDownloaded?: number
  bytesUploaded?: number
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

// streamMetadata returns a cached TorrentInfo snapshot if the server has seen
// this hash before. Use in parallel with streamAdd to render the file list
// instantly while the torrent client is still resolving peers.
export const streamMetadata = async (hash: string): Promise<TorrentInfo | null> => {
  // Local files: synthesize TorrentInfo from /api/local/play (no real metadata cache).
  if (isLocalHash(hash)) return synthesizeLocalInfo(hash).catch(() => null)
  const cached = metadataPeekCache.get(hash)
  if (cached !== undefined) return cached
  try {
    const { data, status } = await api.get<TorrentInfo>(`/stream/metadata/${hash}`, { validateStatus: () => true })
    const result = status === 200 ? data : null
    metadataPeekCache.set(hash, result)
    return result
  } catch {
    metadataPeekCache.set(hash, null)
    return null
  }
}

// metadataPeekCache is seeded by streamMetadataBatch so a playlist warm-cache
// satisfies subsequent per-item streamMetadata calls without extra round-trips.
const metadataPeekCache = new Map<string, TorrentInfo | null>()

// streamMetadataBatch peeks cached TorrentInfo snapshots for many hashes at once.
// Misses are omitted from results — callers fall through to streamAdd.
export const streamMetadataBatch = async (hashes: readonly string[]): Promise<Record<string, TorrentInfo | null>> => {
  const torrent = [...new Set(hashes.filter(h => h && !isLocalHash(h)))]
  if (torrent.length === 0) return {}
  const { data } = await api.post<{ results?: Record<string, TorrentInfo> }>('/stream/metadata/batch', { hashes: torrent })
  const out: Record<string, TorrentInfo | null> = {}
  for (const h of torrent) {
    const hit = data.results?.[h]
    const info = hit?.files?.length ? hit : null
    out[h] = info
    metadataPeekCache.set(h, info)
  }
  return out
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
  const localHash = extractInfoHashFromMagnet(magnet) || null
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

// streamHealth peeks the last-known swarm health (cheap, no swarm activity).
// Pass probe=true ONLY on an explicit user action — that adds the torrent to the
// swarm to count peers (expensive). Auto-calling with probe=true bogs the app.
const unknownHealth: StreamHealth = { known: false, active: false, refreshing: false }

// PEEK coalescing: N SeedBadges peeking on mount become ONE POST
// /stream/health/batch (instead of one GET /stream/health/:hash per card — the
// list-page N+1). The PROBE (on-demand, expensive) stays a single request.
const healthQueue: { hash: string; resolve: (h: StreamHealth) => void }[] = []
let healthFlushTimer: ReturnType<typeof setTimeout> | null = null

function flushHealthQueue() {
  if (healthFlushTimer) { clearTimeout(healthFlushTimer); healthFlushTimer = null }
  const batch = healthQueue.splice(0)
  if (batch.length === 0) return
  const hashes = [...new Set(batch.map(b => b.hash))]
  api.post<{ results?: Record<string, StreamHealth> }>('/stream/health/batch', { hashes })
    .then(r => { const results = r.data?.results ?? {}; for (const b of batch) b.resolve(results[b.hash] ?? unknownHealth) })
    .catch(() => { for (const b of batch) b.resolve(unknownHealth) })
}

export const streamHealth = async (hash: string, magnet?: string, probe = false): Promise<StreamHealth> => {
  if (probe) {
    try {
      const params = new URLSearchParams()
      if (magnet) params.set('magnet', magnet)
      params.set('probe', '1')
      const { data } = await api.get<StreamHealth>(`/stream/health/${hash}?${params.toString()}`)
      return data
    } catch {
      return unknownHealth
    }
  }
  return new Promise<StreamHealth>(resolve => {
    healthQueue.push({ hash, resolve })
    if (healthQueue.length >= 200) flushHealthQueue()
    else if (!healthFlushTimer) healthFlushTimer = setTimeout(flushHealthQueue, 40)
  })
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
  // Substrings de announce URLs cujos torrents continuam seedando após o uso.
  seedTrackers: string[]
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

/**
 * HLS master playlist URL. Used as the Safari/iOS fallback path: Safari's
 * MSE pipeline refuses progressive fragmented MP4 over chunked transfer but
 * natively plays HLS (.m3u8 + .ts) via `<video src>`. Apple's documented
 * streaming format; the only thing Safari treats as a first-class video
 * source. Jellyfin, Plex, Emby all do the same routing.
 */
export const streamHLSMasterURL = (hash: string, fileIdx: number, tokenOverride?: string, audioTrack?: number): string => {
  // audioTrack: faixa de áudio escolhida (índice absoluto do probe). O backend
  // re-transcoda mapeando essa faixa e keya a sessão por ela. Vale pros DOIS
  // caminhos: torrent (/api/stream/hls) e local (/api/local/hls via localResolvedURL).
  const hasAudio = audioTrack != null && audioTrack >= 0
  if (isLocalHash(hash)) {
    const local = localResolvedURL(hash, tokenOverride)
    if (!local || !hasAudio) return local
    return local + (local.includes('?') ? '&' : '?') + `audio=${audioTrack}`
  }
  const audioQ = hasAudio ? `?audio=${audioTrack}` : ''
  return withToken(`/api/stream/hls/${hash}/${fileIdx}/index.m3u8${audioQ}`, tokenOverride)
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
  if (isIOS()) return true
  if (/Chrome|Chromium|Android|Edg/.test(ua)) return false
  return /Safari/i.test(ua)
}

// isIOS detecta APENAS iOS/iPadOS (touch), NÃO o macOS-Safari desktop. Usado pra
// gatear o comportamento "tap-to-play": no iPhone/iPad o play() de áudio EXIGE um
// gesto (regra da Apple, sem ativação persistente) e o autoplay não-gesto trava o
// elemento em readyState 1. No macOS-Safari/Chrome/Edge o autoplay funciona e fica
// intacto. Pega também o iPadOS em "desktop mode" (reporta "Macintosh" + multi-touch).
export function isIOS(): boolean {
  if (typeof navigator === 'undefined') return false
  const ua = navigator.userAgent
  return /iPhone|iPad|iPod/.test(ua) ||
    (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)
}

// streamAudioMeta fetches tags read from a file INSIDE a torrent (artist/album/
// year the filename usually omits). Best-effort: empty tags on the server side
// when the file can't be parsed, so the caller falls back to the filename.
export const streamAudioMeta = async (hash: string, fileIdx: number): Promise<AudioMeta> => {
  const { data } = await api.get<AudioMeta>(`/stream/audio/meta/${hash}/${fileIdx}`)
  return data
}
