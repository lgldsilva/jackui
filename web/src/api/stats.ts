import { api } from './http'

// ─── Personal usage statistics ──────────────────────────────────────────────

export type MonthCount = { month: string; count: number }

export type LibraryAgg = {
  titles: number
  completed: number
  inProgress: number
  watchSeconds: number
  byKind: Record<string, number>
  playsByWeekday: number[] // 0 = domingo
  playsByHour: number[]
  addedByMonth: MonthCount[]
}

export type UserStats = {
  library: LibraryAgg
  downloads: { total: number; completed: number; bytesDownloaded: number }
  searchQueries: number
  watchlists: { count: number; hits: number }
}

export const statsGet = async (): Promise<UserStats> => {
  const { data } = await api.get<UserStats>('/stats')
  return data
}
