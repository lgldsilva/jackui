// Swarm health (peek batch + probe) e tracker scrape. Extraído de stream.ts (R3).
import { api } from './http'
import type { StreamHealth, TrackerScrape } from './stream-types'

const unknownHealth: StreamHealth = { known: false, active: false, refreshing: false }

const healthQueue: { hash: string; resolve: (h: StreamHealth) => void }[] = []
let healthFlushTimer: ReturnType<typeof setTimeout> | null = null

function flushHealthQueue() {
  if (healthFlushTimer) { clearTimeout(healthFlushTimer); healthFlushTimer = null }
  const batch = healthQueue.splice(0)
  if (batch.length === 0) return
  const hashes = [...new Set(batch.map(b => b.hash))]
  api.post<{ results?: Record<string, StreamHealth> }>('/stream/health/batch', { hashes })
    .then(r => { const results = r.data?.results ?? {}; for (const b of batch) b.resolve(results[b.hash] ?? unknownHealth) })
    .catch(() => { for (const b of batch) b.resolve(unknownHealth) })
}

export const streamHealth = async (hash: string, magnet?: string, probe = false): Promise<StreamHealth> => {
  if (probe) {
    try {
      const params = new URLSearchParams()
      if (magnet) params.set('magnet', magnet)
      params.set('probe', '1')
      const { data } = await api.get<StreamHealth>(`/stream/health/${hash}?${params.toString()}`)
      return data
    } catch {
      return unknownHealth
    }
  }
  return new Promise<StreamHealth>(resolve => {
    healthQueue.push({ hash, resolve })
    if (healthQueue.length >= 200) flushHealthQueue()
    else if (!healthFlushTimer) healthFlushTimer = setTimeout(flushHealthQueue, 40)
  })
}

export const streamTrackers = async (hash: string, magnet?: string): Promise<TrackerScrape[]> => {
  try {
    const params = new URLSearchParams()
    if (magnet) params.set('magnet', magnet)
    const { data } = await api.get<TrackerScrape[]>(`/stream/trackers/${hash}?${params.toString()}`)
    return data || []
  } catch {
    return []
  }
}
