// Controles estilo Transmission (pause/resume/priority/limits/viewer).
// Extraído de stream.ts (R3 follow-up).
import { api } from './http'
import type { StreamLimits, StreamPriority, TorrentInfo } from './stream-types'

export const streamActive = async (): Promise<TorrentInfo[]> => {
  const { data } = await api.get<TorrentInfo[]>('/stream/active')
  return data || []
}

export const streamPause = async (hash: string): Promise<void> => {
  await api.post(`/stream/${hash}/pause`)
}

export const streamResume = async (hash: string): Promise<void> => {
  await api.post(`/stream/${hash}/resume`)
}

export const streamSetPriority = async (hash: string, priority: StreamPriority): Promise<void> => {
  await api.post(`/stream/${hash}/priority`, { priority })
}

export const streamSetFilePriority = async (hash: string, fileIdx: number, priority: 'none' | 'low' | 'normal' | 'high'): Promise<void> => {
  await api.post(`/stream/${hash}/files/${fileIdx}/priority`, { priority })
}

export const streamPauseAll = async (): Promise<{ paused: number }> => {
  const { data } = await api.post<{ paused: number }>('/stream/active/pause')
  return data
}

export const streamResumeAll = async (): Promise<{ resumed: number }> => {
  const { data } = await api.post<{ resumed: number }>('/stream/active/resume')
  return data
}

export const streamGetLimits = async (): Promise<StreamLimits> => {
  const { data } = await api.get<StreamLimits>('/stream/limits')
  return data
}

export const streamSetLimits = async (limits: StreamLimits): Promise<StreamLimits> => {
  const { data } = await api.post<StreamLimits>('/stream/limits', limits)
  return data
}

export const streamPrefetch = async (hash: string, fileIdx: number): Promise<void> => {
  try {
    await api.post(`/stream/prefetch/${hash}/${fileIdx}`)
  } catch {
    // Silent: prefetch is best-effort.
  }
}

export const streamViewerOpen = async (hash: string): Promise<void> => {
  await api.post(`/stream/${hash}/viewer`)
}

export const streamViewerClose = async (hash: string): Promise<void> => {
  await api.delete(`/stream/${hash}/viewer`)
}
