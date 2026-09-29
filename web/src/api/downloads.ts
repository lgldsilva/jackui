import { api } from './http'
import { BATCH_CAPS, runChunked } from '../lib/batchChunk'
import type { TorrentInfo, PromotePreviewEntry, StreamFile } from './client'

// ─── Background downloads ──────────────────────────────────────────────────
// Full-file (not streaming) download queue. Backed by anacrolix file.Download
// which prioritises all pieces; protected from cache eviction until removed.

export type DownloadEntry = {
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
  status: 'queued' | 'downloading' | 'moving' | 'completed' | 'failed' | 'paused'
  bytesDownloaded: number
  progress: number
  downRate?: number
  upRate?: number          // upload bytes/sec (seeding), filled in by the backend
  bytesUploaded?: number   // total sent in this session (reset on re-add), filled in by the backend
  seeders?: number         // live swarm seeders, filled in by the backend
  eta?: number
  startedAt?: string | null
  completedAt?: string | null
  error?: string
  createdAt: string
  promoted?: boolean   // true when file was moved outside the download dir
  // Queue scheduling
  priority?: 'high' | 'normal' | 'low'
  stalls?: number          // times demoted for no-seed
  queuePosition?: number   // 1-based rank among queued rows (0 = not queued)
}

export type DownloadPriority = 'high' | 'normal' | 'low'

export type DownloadsQueueSettings = {
  maxActive: number
  perUserMaxActive: number
  /** Disk-bound piece rechecks in parallel — independent of maxActive. */
  maxConcurrentVerify: number
  stallThresholdMin: number
  maxStalls: number
  agingStepMin: number
  agingCap: number
  rotationEnabled: boolean
  autoPromoteArr: boolean
  // Concurrency mode for promote/move copies: 'auto' (detects HDD/SSD),
  // 'serial' (one at a time) or 'parallel' (always parallel).
  transferConcurrencyMode: 'auto' | 'serial' | 'parallel'
}

// One known source (magnet) for a download — the original + alternatives found
// via Jackett re-search (Phase 2 source rotation).
export type DownloadSource = {
  id: number
  downloadId: number
  infoHash: string
  title: string
  tracker: string
  seeders: number
  size: number
  status: 'active' | 'candidate' | 'cooldown' | 'failed'
  tries: number
  lastTried?: string | null
  createdAt: string
}

// AUTO_FILE_INDEX mirrors downloads.FileIndexAuto on the backend: the worker
// resolves the best file via pickBestFile (largest video/media). Use it ALWAYS
// while the file list is still unresolved — NEVER fileIndex 0 (in adult/scene
// packs index 0 is usually a tens-of-bytes .nfo; the download "completes"
// in seconds and the user ends up without the video).
export const AUTO_FILE_INDEX = -1

// WHOLE_TORRENT_FILE_INDEX mirrors downloads.FileIndexWholeTorrent on the backend:
// ONE queue row that downloads the ENTIRE torrent (aggregate progress; completion
// moves all files preserving the structure). -1 already means "auto-pick"
// (AUTO_FILE_INDEX), hence -2.
export const WHOLE_TORRENT_FILE_INDEX = -2

export type DownloadCreateParams = {
  infoHash: string
  fileIndex: number
  magnet: string
  name: string
  filePath: string
  fileSize: number
  tracker?: string
  category?: string
  destBase?: string   // chosen destination (#16); empty = default download dir
  destSubdir?: string // optional subfolder under destBase
}

/** Identity fields needed when the torrent file list is still unknown. */
export type UnresolvedCreateIdentity = {
  infoHash: string
  magnet: string
  name: string
  tracker?: string
  category?: string
  destBase?: string
  destSubdir?: string
}

/**
 * Build a downloadCreate payload when the file list is null/empty (metadata not
 * resolved yet). Always uses AUTO_FILE_INDEX so the worker runs pickBestFile.
 *
 * Regression: hardcoding fileIndex: 0 here completed only a 34-byte .nfo in
 * multi-file scene packs while the UI reported success.
 */
export function createParamsWhenFilesUnknown(base: UnresolvedCreateIdentity): DownloadCreateParams {
  return {
    infoHash: base.infoHash,
    magnet: base.magnet,
    name: base.name,
    fileIndex: AUTO_FILE_INDEX,
    filePath: '',
    fileSize: 0,
    tracker: base.tracker,
    category: base.category,
    destBase: base.destBase,
    destSubdir: base.destSubdir,
  }
}

// DownloadDestination is a writable target the user may pick for a download:
// a mount they're allowed to see, or a promote destination.
export type DownloadDestination = {
  name: string
  path: string
  userSubpath?: boolean
}

export const downloadDestinations = async (): Promise<DownloadDestination[]> => {
  const { data } = await api.get<DownloadDestination[]>('/downloads/destinations')
  return data || []
}

export const downloadDestBrowse = async (base: string, path: string): Promise<{ dirs: string[]; path: string }> => {
  const query = new URLSearchParams({ base, path })
  const { data } = await api.get<{ dirs: string[]; path: string }>(`/downloads/dest/browse?${query.toString()}`)
  return data
}

export type DownloadFilterParams = {
  status?: string
  tracker?: string
  category?: string
  search?: string
  sort?: string
  order?: string
  userId?: string
}

export type DownloadUserEntry = {
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

// One torrent file inside an enqueue batch. Only the per-file fields;
// infoHash/magnet/name/tracker/category/destination are shared in the body.
export type BatchFile = {
  fileIndex: number
  filePath: string
  fileSize: number
}

export type DownloadBatchCreateParams = {
  infoHash: string
  magnet: string
  name: string
  tracker?: string
  category?: string
  destBase?: string
  destSubdir?: string
  files: BatchFile[]
}

export type DownloadBatchCreateResult = {
  created: DownloadEntry[]
  requeued: number
}

// buildBatchFiles maps the picked files (StreamFile from the preview) to the
// per-file format of the batch body. PURE function — testable without network.
export function buildBatchFiles(picks: readonly StreamFile[]): BatchFile[] {
  return picks.map(f => ({ fileIndex: f.index, filePath: f.path, fileSize: f.size }))
}

// isWholeTorrentSelection: true when ALL of the torrent's files are
// selected. In that case a SINGLE "whole torrent" row is enqueued (fileIndex=-2,
// anacrolix file priorities) instead of N per-file rows — a 778-file
// pack becomes 1 row (end of the explosion that inflated the list and /api/downloads).
// A subset (the user unchecked something) stays a batch, preserving
// granularity. PURE function — testable without network.
export function isWholeTorrentSelection(
  files: readonly StreamFile[],
  selected: ReadonlySet<number>,
): boolean {
  return files.length > 0 && files.every(f => selected.has(f.index))
}

// downloadBatchCreate enqueues N files of ONE torrent in a SINGLE request
// (replaces the 1-POST-per-file Promise.allSettled). The backend resolves the
// destination once and inserts everything in one transaction (all-or-nothing, idempotent).
export const downloadBatchCreate = async (
  params: DownloadBatchCreateParams,
): Promise<DownloadBatchCreateResult> => {
  const { data } = await api.post<DownloadBatchCreateResult>('/downloads/batch', params)
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

export const downloadSetPriority = async (id: number, priority: DownloadPriority): Promise<void> => {
  await api.patch(`/downloads/${id}/priority`, { priority })
}

export const getDownloadsQueueSettings = async (): Promise<DownloadsQueueSettings> => {
  const { data } = await api.get<DownloadsQueueSettings>('/downloads/settings')
  return data
}

export const downloadSources = async (id: number): Promise<DownloadSource[]> => {
  const { data } = await api.get<DownloadSource[]>(`/downloads/${id}/sources`)
  return data || []
}

export const updateDownloadsQueueSettings = async (
  s: DownloadsQueueSettings,
): Promise<{ restartRequired: boolean }> => {
  const { data } = await api.put<{ restartRequired: boolean }>('/downloads/settings', s)
  return data
}

// downloadRecheck forces a "Force Recheck" (qBittorrent style) — re-hashes
// every piece of the file on disk and resets bytes_downloaded so the worker
// reconciles it afterwards. UI shows a spinner while the backend processes
// (the call returns as soon as the hash check starts; progress shows up
// on the worker's next tick).
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

export const downloadBatchDelete = async (
  ids: number[],
): Promise<{ deleted: number; total: number; failed?: number[] }> => {
  const { data } = await api.post<{ deleted: number; total: number; failed?: number[] }>(
    '/downloads/batch/delete',
    { ids },
  )
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

// DownloadDetails: the download row + the torrent's full file list
// + real sizes (sparse vs apparent). The backend only fills torrent while the
// info_hash is active in the streamer; null once dropped (post-completed
// without seed).
export type DownloadDetails = {
  download: DownloadEntry
  file: { apparent: number; onDisk: number; exists: boolean }
  torrent: TorrentInfo | null
}
export const downloadDetails = async (id: number): Promise<DownloadDetails> => {
  const { data } = await api.get<DownloadDetails>(`/downloads/${id}/details`)
  return data
}

// PeerInfo: a peer connected to the torrent. `availability` is the fraction (0..1) of
// the pieces the peer has. `sending`/`receiving` are INFERRED from the rates (the
// anacrolix lib does not expose choke/interest). `addr` may repeat across polls.
export type PeerInfo = {
  addr: string
  client?: string
  network?: string
  availability: number
  downRate: number
  upRate: number
  downloaded: number
  uploaded: number
  isSeeder: boolean
  receiving: boolean
  sending: boolean
  encrypted?: boolean
}

// DownloadPeers: live snapshot of the peers. `active=false` when the torrent is not
// loaded in the streamer (dropped / never opened) — peers comes back empty.
export type DownloadPeers = {
  peers: PeerInfo[]
  active: boolean
}
export const downloadPeers = async (id: number): Promise<DownloadPeers> => {
  const { data } = await api.get<DownloadPeers>(`/downloads/${id}/peers`)
  return data
}

export type PromoteDestination = {
  name: string
  path: string
}

// Moves a completed download to the shared directory (JACKUI_SHARED_DIR
// on the server) or another destination (targetBase), optionally into a subfolder. After
// moving, optionally keeps seeding (keepSeeding=true).
export const downloadPromote = async (
  id: number,
  opts: { keepSeeding: boolean; targetSubdir?: string; targetBase?: string },
): Promise<DownloadEntry> => {
  const { data } = await api.post<DownloadEntry>(`/downloads/${id}/promote`, opts)
  return data
}

// Promotes N downloads to the same destination subfolder. Individual failures
// don't abort the batch; returns { promoted, failed }.
export type PromoteBatchResult = {
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
  // Go nil slice serializes as JSON null; the UI does previews.length → guard.
  return { previews: data?.previews ?? [] }
}

// Lists subfolders under {base}/<path> to feed the PromoteModal browser.
// Empty base = sharedDir (default).
export const downloadPromoteBrowse = async (path: string, base?: string): Promise<{ dirs: string[]; path: string }> => {
  const params = new URLSearchParams({ path })
  if (base) params.set('base', base)
  const { data } = await api.get<{ dirs: string[]; path: string }>(
    `/downloads/promote/browse?${params}`,
  )
  // A leaf subfolder (no subdirs) comes back with dirs=null (nil slice in Go); the
  // browser does dirs.length → null crashes. Always normalize to [].
  return { dirs: data?.dirs ?? [], path: data?.path ?? path }
}

// Lists the available promotion destinations (name + path).
export const fetchPromoteDestinations = async (): Promise<PromoteDestination[]> => {
  const { data } = await api.get<PromoteDestination[]>('/promote/destinations')
  return data ?? []
}

// Stops seeding without moving the file.
export const downloadStopSeed = async (id: number): Promise<void> => {
  await api.post(`/downloads/${id}/stop-seed`)
}

/** Perf #10: stop-seed many queue rows in one POST (unique info_hashes DropSeed'd once). */
export type StopSeedBatchResult = {
  affected: number
  total: number
  failed?: number[]
  hashes?: number
}
export const downloadBatchStopSeed = async (ids: number[]): Promise<StopSeedBatchResult> =>
  runChunked(ids, BATCH_CAPS.stopSeed, async chunk => {
    const { data } = await api.post<StopSeedBatchResult>('/downloads/batch/stop-seed', { ids: chunk })
    return data
  }, (a, b) => ({
    affected: a.affected + b.affected,
    total: a.total + b.total,
    failed: [...(a.failed ?? []), ...(b.failed ?? [])],
  }), { affected: 0, total: 0, failed: [] })

// ─── Cross-torrent dedup (#23) ─────────────────────────────────────────────
// One of a torrent's files that the user ALREADY has on disk, so it can be
// linked instead of re-downloaded. source 'download' = already in the queue;
// 'library'/'cloud' = a browsable mount (carries mount+relPath, the link target).
// confidence 'certain' = piece-verified (local); 'probable' = head/tail
// fingerprint (the only check possible for cloud files).
export type DedupMatch = {
  fileIndex: number
  name: string
  size: number
  isVideo: boolean
  source: 'download' | 'library' | 'cloud'
  mount?: string
  relPath?: string
  confidence: 'certain' | 'probable'
}

export type DedupCheckResult = { matches: DedupMatch[]; totalFiles: number }

export type DedupLinkItem = { fileIndex: number; mount: string; relPath: string }
export type DedupLinkParams = { infoHash: string; magnet: string; name: string; items: DedupLinkItem[] }
export type DedupLinkResult = { linked: number; errors: string[] }

// dedupCheck probes (read-only) which of a torrent's files are already on disk
// before enqueuing, so the UI can offer to link them. The backend briefly
// activates the torrent to read its file list, so this is not instant.
export const dedupCheck = async (magnet: string): Promise<DedupCheckResult> => {
  const { data } = await api.post<DedupCheckResult>('/downloads/dedup-check', { magnet })
  return data
}

// dedupLink records the confirmed matches as completed links (no swarm fetch).
// Soft failures come back in result.errors (the call itself resolves on 200).
export const dedupLink = async (params: DedupLinkParams): Promise<DedupLinkResult> => {
  const { data } = await api.post<DedupLinkResult>('/downloads/link', params)
  return data
}
