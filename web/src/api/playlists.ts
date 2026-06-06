import { api } from './http'

// ─── Playlists ─────────────────────────────────────────────────────────────

export type Playlist = {
  id: number
  userId: number
  name: string
  description: string
  createdAt: string
  updatedAt: string
  itemCount?: number
}

export type PlaylistItem = {
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
