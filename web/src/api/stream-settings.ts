// Settings, cache LRU e taxa global do streamer. Extraído de stream.ts (R3).
import { api } from './http'
import type {
  StreamCacheStats,
  StreamRate,
  StreamSettings,
  StreamSettingsResponse,
} from './stream-types'

export const streamCacheStats = async (): Promise<StreamCacheStats> => {
  const { data } = await api.get<StreamCacheStats>('/stream/cache')
  return data
}

export const streamCacheClear = async (entry?: string): Promise<void> => {
  const url = entry ? `/stream/cache?entry=${encodeURIComponent(entry)}` : '/stream/cache'
  await api.delete(url)
}

export const getStreamSettings = async (): Promise<StreamSettingsResponse> => {
  const { data } = await api.get<StreamSettingsResponse>('/stream/settings')
  return data
}

export const updateStreamSettings = async (
  s: StreamSettings,
): Promise<{ restartRequired: boolean }> => {
  const { data } = await api.put<{ restartRequired: boolean }>('/stream/settings', s)
  return data
}

export const streamRate = async (): Promise<StreamRate> => {
  const { data } = await api.get<StreamRate>('/stream/rate')
  return data
}
