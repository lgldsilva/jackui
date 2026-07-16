// Favoritos, pastas e import de torrent. Extraído de stream.ts (R3 follow-up).
import { api } from './http'
import { BATCH_CAPS, runChunked } from '../lib/batchChunk'
import type { FavoriteFolder, ImportResult, StreamFavorite } from './stream-types'

const emptyFavBatch: FavoriteBatchResult = { affected: 0, total: 0, failed: [] }
const mergeFavBatch = (a: FavoriteBatchResult, b: FavoriteBatchResult): FavoriteBatchResult => ({
  affected: a.affected + b.affected,
  total: a.total + b.total,
  failed: [...a.failed, ...b.failed],
})

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

/** Result of favorites batch remove/folder (Perf #9). */
export type FavoriteBatchResult = {
  affected: number
  total: number
  failed: string[]
}

/** Remove many favorites in ONE call (Perf #9 — no N DELETE /stream/favorite/:name). */
export const favoriteRemoveBatch = async (names: string[]): Promise<FavoriteBatchResult> =>
  runChunked(names, BATCH_CAPS.favorites, async chunk => {
    const { data } = await api.post<FavoriteBatchResult>('/stream/favorites/batch/remove', { names: chunk })
    return data
  }, mergeFavBatch, emptyFavBatch)

export const streamImport = async (
  payload: { magnet?: string; torrentB64?: string; name?: string; folderId?: number | null },
): Promise<ImportResult> => {
  const { data } = await api.post<ImportResult>('/stream/import', payload)
  return data
}

export const folderList = async (includeHidden = false): Promise<FavoriteFolder[]> => {
  const { data } = await api.get<FavoriteFolder[]>(`/stream/favorites/folders${includeHidden ? '?includeHidden=1' : ''}`)
  return data || []
}

export const folderCreate = async (name: string, parentId: number | null = null, hidden = false): Promise<FavoriteFolder> => {
  const { data } = await api.post<FavoriteFolder>('/stream/favorites/folders', { name, parentId, hidden })
  return data
}

export const folderSetHidden = async (id: number, hidden: boolean): Promise<void> => {
  await api.patch(`/stream/favorites/folders/${id}`, { hidden })
}

export const folderRename = async (id: number, name: string): Promise<void> => {
  await api.patch(`/stream/favorites/folders/${id}`, { name })
}

export const folderMove = async (id: number, newParentID: number | null): Promise<void> => {
  await api.patch(`/stream/favorites/folders/${id}`, newParentID === null
    ? { parentToRoot: true }
    : { parentId: newParentID })
}

export const folderDelete = async (id: number): Promise<void> => {
  await api.delete(`/stream/favorites/folders/${id}`)
}

export const favoriteSetFolder = async (name: string, folderID: number | null): Promise<void> => {
  await api.patch(`/stream/favorite/${encodeURIComponent(name)}/folder`, folderID === null
    ? { toRoot: true }
    : { folderId: folderID })
}

/** Move many favorites into a folder (or root) in ONE call (Perf #9). */
export const favoriteSetFolderBatch = async (
  names: string[],
  folderID: number | null,
): Promise<FavoriteBatchResult> =>
  runChunked(names, BATCH_CAPS.favorites, async chunk => {
    const body = folderID === null
      ? { names: chunk, toRoot: true as const }
      : { names: chunk, folderId: folderID }
    const { data } = await api.post<FavoriteBatchResult>('/stream/favorites/batch/folder', body)
    return data
  }, mergeFavBatch, emptyFavBatch)
