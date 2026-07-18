// Arquivos locais: browser de mounts (config `external.mounts`), cache whole-file,
// upload, promote, e o "pseudo info-hash" (`local-<b64>`) que faz um arquivo de
// disco passar por torrent no PlayerModal. Extraído de client.ts (god-file, #417).
// As funções /api/local/* espelham as de torrent (streamProbe, subtitlesAuto…):
// detectam o prefixo e roteiam pra cá em vez de /api/stream/*.
//
// Este arquivo continua sendo o ponto de entrada único (`client.ts` faz
// `export * from './local'`): re-exporta os módulos irmãos abaixo pra que NENHUM
// import externo quebre. A base compartilhada (pseudo-hash + "view as user") vive
// em ./local-base pra evitar ciclos com os irmãos.
import { api, withToken } from './http'
import { audioCapsParam } from '../lib/audioCaps'
import { isIOS } from './stream-browser'
import { streamSubtrackURL } from './stream-urls'
import type { TorrentInfo, StreamFile } from './stream-types'
import { buildLocalHash, parseLocalHash, appendViewAs, withViewAs, localQS } from './local-base'
import { localTransferStatus } from './local-transfer'

export * from './local-base'
export * from './local-promote'
export * from './local-audio'
export * from './local-cache'
export * from './local-transfer'
export * from './local-download'

// Cache da URL resolvida por localPlay (direct ou HLS) — populada por
// synthesizeLocalInfo, lida pelos URL builders (streamFileURL etc.) pra que
// PlayerModal não precise distinguir torrent de local.
type LocalPlayable = Pick<LocalPlaySource, 'url' | 'kind'>
const localPlayableURLCache = new Map<string, LocalPlayable>()

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
  localPlayableURLCache.set(hash, { url: play.url, kind: play.kind })
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
    localPlaybackKind: play.kind,
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
  const playable = localPlayableURLCache.get(hash)!
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
    localPlaybackKind: playable.kind,
  }
}

// localResolvedURL returns the backend-selected cached URL with media
// credentials attached, or empty when the file has not been resolved yet.
// Direct playback uses it; an explicit fallback builds a fresh local HLS URL.
export function localResolvedURL(hash: string, tokenOverride?: string): string {
  const source = localPlayableURLCache.get(hash)
  return source ? withToken(source.url, tokenOverride) : ''
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

// localPlayBatch resolves direct-vs-HLS + the URL for MANY local files in ONE
// call (POST /api/local/play/batch) and PRE-WARMS localPlayableURLCache — exactly
// as synthesizeLocalInfo does per file, but for the whole folder at once. So a
// playlist is warmed without the N+1 of one ffprobe per track, and each track's
// play/auto-advance is instant (cache hit via localStreamInfo, which skips
// synthesizeLocalInfo when the URL is already cached). forceHLS mirrors
// synthesizeLocalInfo's iOS choice so the pre-warmed video URL matches the real
// play (audio ignores it server-side). Best-effort: per-file errors are ignored
// (that track just falls back to the normal resolve path on play).
export async function localPlayBatch(mount: string, paths: string[]): Promise<void> {
  if (paths.length === 0) return
  const { data } = await api.post<{ items: { path: string; kind?: 'direct' | 'hls'; url?: string; error?: string }[] }>(
    withViewAs('/local/play/batch'),
    { mount, paths, forceHLS: isIOS() },
  )
  for (const it of data.items ?? []) {
    if (it.url && it.kind && !it.error) {
      localPlayableURLCache.set(buildLocalHash(mount, it.path), { url: withViewAs(it.url), kind: it.kind })
    }
  }
}
