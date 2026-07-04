// Arquivos locais: browser de mounts (config `external.mounts`), cache whole-file,
// upload, promote, e o "pseudo info-hash" (`local-<b64>`) que faz um arquivo de
// disco passar por torrent no PlayerModal. Extraído de client.ts (god-file, #417).
// As funções /api/local/* espelham as de torrent (streamProbe, subtitlesAuto…):
// detectam o prefixo e roteiam pra cá em vez de /api/stream/*.
import { api, withToken } from './http'
import { audioCapsParam } from '../lib/audioCaps'
import { isIOS, streamSubtrackURL, type TorrentInfo, type StreamFile } from './stream'

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

// localQS monta a query mount/path (+?user= quando "view as user"). Exportada
// porque stream.ts/subtitles.ts reusam pra rotear o branch local.
export function localQS(mount: string, path: string): string {
  const base = `mount=${encodeURIComponent(mount)}&path=${encodeURIComponent(path)}`
  return localViewAsUser ? `${base}&user=${encodeURIComponent(localViewAsUser)}` : base
}

// Cache da URL resolvida por localPlay (direct ou HLS) — populada por
// synthesizeLocalInfo, lida pelos URL builders (streamFileURL etc.) pra que
// PlayerModal não precise distinguir torrent de local.
const localPlayableURLCache = new Map<string, string>()

// synthesizeLocalInfo constrói um TorrentInfo "falso" pra arquivos locais.
// O PlayerModal não distingue — só lê os mesmos campos (infoHash, name, files,
// totalSize, primaryFile). file index é sempre 0 (o próprio arquivo local).
export async function synthesizeLocalInfo(hash: string): Promise<TorrentInfo> {
  const loc = parseLocalHash(hash)
  if (!loc) throw new Error('invalid local hash')
  const isVideo = !/\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i.test(loc.path)
  // Vídeo local no iOS → força HLS (o WebKit trava em MP4 progressive). Áudio e
  // desktop seguem no direct.
  const play = await localPlay(loc.mount, loc.path, isVideo && isIOS())
  // The URL from localPlay starts with /api/... (no token); withToken adds it.
  localPlayableURLCache.set(hash, play.url)
  const name = loc.path.split('/').pop() || loc.path
  const file: StreamFile = {
    index: 0,
    path: loc.path,
    size: 0,
    isVideo,
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
export async function localStreamInfo(hash: string): Promise<TorrentInfo> {
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
// string if not yet resolved. Used by the streamFileURL/streamHLSMasterURL
// builders when the hash is a local pseudo-hash — they all converge to the
// same URL (the server decided direct vs HLS in /api/local/play).
export function localResolvedURL(hash: string, tokenOverride?: string): string {
  const url = localPlayableURLCache.get(hash)
  return url ? withToken(url, tokenOverride) : ''
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
  incomplete?: boolean // download in progress (a .part file, or a dir holding one)
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
  // Go nil slice → JSON null; callers do r.entries.filter(...) → guard both fields.
  return { entries: data?.entries ?? [], total: data?.total ?? 0 }
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
export const localPlay = async (mount: string, path: string, forceHLS = false): Promise<LocalPlaySource> => {
  const sp = new URLSearchParams({ mount, path })
  // Tell the server which non-universal audio codecs this browser can play
  // inline, so it transcodes (audio-only HLS) the ones it can't — Safari can't
  // do FLAC/OGG/Opus. Harmless on video files (the server ignores it there).
  const caps = audioCapsParam()
  if (caps) sp.set('acaps', caps)
  // forceHLS: vídeo local no iOS/Safari. O WebKit trava em MP4 progressive por HTTP,
  // então o vídeo local vai por HLS (remux, sem re-encode pra H264) — o MESMO caminho
  // confiável do torrent. Sem isto o iOS carregava o <video src=mp4> e travava em rs2.
  if (forceHLS) sp.set('transcode', 'hls')
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
