import { api } from './http'

// ─── Watchlists ────────────────────────────────────────────────────────────

export type SchedKind = 'interval' | 'daily' | 'weekly'

export type Watchlist = {
  id: number
  userId: number
  query: string
  category: string
  minSeeders: number
  ntfyTopic: string
  schedKind: SchedKind
  schedMinutes: number // interval: every N minutes (<= 0 → server default)
  schedWeekday: number // weekly: 0=Sunday … 6=Saturday
  schedHour: number
  schedMinute: number
  nextCheckAt: string
  lastChecked: string
  createdAt: string
  hitCount?: number
}

export type WatchlistInput = {
  query: string
  category?: string
  minSeeders?: number
  ntfyTopic?: string
  schedKind?: SchedKind
  schedMinutes?: number
  schedWeekday?: number
  schedHour?: number
  schedMinute?: number
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

export const watchlistsCreate = async (input: WatchlistInput): Promise<Watchlist> => {
  const { data } = await api.post<Watchlist>('/watchlists', input)
  return data
}

export const watchlistsUpdate = async (id: number, input: WatchlistInput): Promise<void> => {
  await api.put(`/watchlists/${id}`, input)
}

export const watchlistsDelete = async (id: number): Promise<void> => {
  await api.delete(`/watchlists/${id}`)
}

export const watchlistsHits = async (id: number): Promise<WatchlistHit[]> => {
  const { data } = await api.get<WatchlistHit[]>(`/watchlists/${id}/hits`)
  return data || []
}
