// Tipos compartilhados dos módulos /api/stream*. Fica isolado aqui pra que
// stream.ts, local.ts e os módulos irmãos importem sem ciclos (R3 follow-up).

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
  bytesDownloaded?: number
  bytesUploaded?: number
  progress: number
  primaryFile: number
  status?: 'downloading' | 'paused' | 'seeding' | 'complete'
  priority?: 'low' | 'normal' | 'high' | ''
  trackers?: string[]
  stalled?: boolean
  // Authoritative decision returned by /api/local/play for pseudo-hashes.
  localPlaybackKind?: 'direct' | 'hls'
}

export type StreamHealth = {
  known: boolean
  active: boolean
  refreshing: boolean
  seeders?: number
  peers?: number
  available?: boolean
  checkedAt?: string
}

export type TrackerScrape = {
  tracker: string
  seeders: number
  leechers: number
  ok: boolean
}

export type CacheEntry = {
  path: string
  size: number
  modTime: string
  isActive: boolean
  isFavorite: boolean
  infoHash?: string
}

export type StreamCacheStats = {
  dataDir: string
  totalSize: number
  maxSize: number
  numActive: number
  entries: CacheEntry[]
  diskFree: number
  diskTotal: number
  evictedCount: number
  evictedBytes: number
  lastEvictionAt?: string
}

export type StorageBackend = 'file' | 'mmap'

export type StreamSettings = {
  maxDownloadRate: number
  maxUploadRate: number
  readaheadMB: number
  storageBackend: StorageBackend
  maxConnsPerTorrent: number
  halfOpenConns: number
  peersHighWater: number
  pieceHashers: number
  maxCacheGB: number
  seedTrackers: string[]
  hlsMediaRenditions: boolean
}

export type StreamSettingsDefaults = {
  readaheadMB: number
  maxConnsPerTorrent: number
  halfOpenConns: number
  peersHighWater: number
  pieceHashers: number
}

export type StreamSettingsResponse = StreamSettings & { defaults: StreamSettingsDefaults }

export type StreamRate = {
  downRate: number
  upRate: number
  activeTorrents: number
}

export type StreamFavorite = {
  name: string
  infoHash: string
  magnet: string
  userId: number
  favoritedAt: string
  reason: 'manual' | 'auto-5min'
  folderId: number | null
  totalSize?: number
  seeders?: number
}

export type FavoriteFolder = {
  id: number
  userId: number
  name: string
  parentId: number | null
  position: number
  hidden?: boolean
  createdAt: string
}

export type ImportResult = { infoHash: string; name: string; magnet: string }

export type MediaTrack = {
  index: number
  type: 'audio' | 'subtitle'
  codec: string
  language?: string
  title?: string
  default: boolean
  forced?: boolean
  channels?: number
  image?: boolean
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
  videoCodec?: string
  container?: string
  audioCodec?: string
  needsTranscode?: boolean
  transcodeReason?: string
}

export type StreamPriority = 'low' | 'normal' | 'high'

export type StreamLimits = {
  down: number
  up: number
}
