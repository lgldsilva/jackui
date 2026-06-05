import { api } from './http'

// ─── Watchlists ────────────────────────────────────────────────────────────

export type Watchlist = {
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

export type WatchlistHit = {
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
