// "Cache mark" de arquivos locais: pré-busca whole-file de mounts lentos/remotos
// (rclone/NFS/CIFS) pro disco local, e o toggle de "hidden". Extraído de
// local.ts (#417 follow-up).
import { api } from './http'
import { localQS } from './local-base'

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
