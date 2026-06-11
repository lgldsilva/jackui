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
  autoDownload: boolean
  minResolution: string
  maxSizeBytes: number
  codec: string
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
  autoDownload?: boolean
  minResolution?: string
  maxSizeBytes?: number
  codec?: string
}

export type WatchlistHit = {
  infoHash: string
  title: string
  magnet: string
  seeders: number
  size: number
  seenAt: string
  autoDownloaded?: boolean
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

// ParsedSchedule is what POST /watchlists/schedule/parse returns: the same
// sched* shape as Watchlist, already normalized (clamped) by the backend.
export type ParsedSchedule = {
  schedKind: SchedKind
  schedMinutes: number
  schedWeekday: number
  schedHour: number
  schedMinute: number
}

// watchlistsParseSchedule converts a free-text phrase ("toda segunda às 9h")
// into a schedule via the server's AI chain. Errors: 400 empty text, 422 the
// AI couldn't read it as a schedule, 503 AI disabled/unavailable.
export const watchlistsParseSchedule = async (text: string): Promise<ParsedSchedule> => {
  const { data } = await api.post<ParsedSchedule>('/watchlists/schedule/parse', { text })
  return data
}
