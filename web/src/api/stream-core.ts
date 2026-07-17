// Núcleo do streaming: add, metadata, info, drop, queue. Extraído de stream.ts (R3).
import { api } from './http'
import { BATCH_CAPS, runChunked } from '../lib/batchChunk'
import { extractBtihFromMagnet } from '../lib/magnet'
import { downloadCreate, WHOLE_TORRENT_FILE_INDEX, type DownloadEntry } from './downloads'
import {
  isLocalHash,
  localStreamInfo,
  synthesizeLocalInfo,
} from './local'
import type { TorrentInfo } from './stream-types'
import type { AudioMeta } from './local-audio'

const metadataPeekCache = new Map<string, TorrentInfo | null>()

export const streamMetadata = async (hash: string): Promise<TorrentInfo | null> => {
  if (isLocalHash(hash)) return synthesizeLocalInfo(hash).catch(() => null)
  const cached = metadataPeekCache.get(hash)
  if (cached !== undefined) return cached
  try {
    const { data, status } = await api.get<TorrentInfo>(`/stream/metadata/${hash}`, { validateStatus: () => true })
    const result = status === 200 ? data : null
    metadataPeekCache.set(hash, result)
    return result
  } catch {
    metadataPeekCache.set(hash, null)
    return null
  }
}

export const streamMetadataBatch = async (hashes: readonly string[]): Promise<Record<string, TorrentInfo | null>> => {
  const torrent = [...new Set(hashes.filter(h => h && !isLocalHash(h)))]
  if (torrent.length === 0) return {}
  const { data } = await api.post<{ results?: Record<string, TorrentInfo> }>('/stream/metadata/batch', { hashes: torrent })
  const out: Record<string, TorrentInfo | null> = {}
  for (const h of torrent) {
    const hit = data.results?.[h]
    const info = hit?.files?.length ? hit : null
    out[h] = info
    metadataPeekCache.set(h, info)
  }
  return out
}

export const resolveTorrentInfo = async (magnet: string, infoHash?: string): Promise<TorrentInfo> => {
  if (infoHash && isLocalHash(infoHash)) return synthesizeLocalInfo(infoHash)
  if (infoHash) {
    const cached = await streamMetadata(infoHash)
    if (cached?.files?.length) return cached
  }
  return streamAdd(magnet)
}

export const queueAllTorrentFiles = async (
  info: TorrentInfo, magnet: string, name: string, tracker?: string, category?: string,
): Promise<DownloadEntry> =>
  downloadCreate({
    infoHash: info.infoHash, fileIndex: WHOLE_TORRENT_FILE_INDEX, magnet, name,
    filePath: '', fileSize: info.totalSize, tracker, category,
  })

export const streamAdd = async (magnet: string, kind?: 'audio' | 'video'): Promise<TorrentInfo> => {
  // extractBtihFromMagnet keeps the raw btih segment (incl. a `local-…`
  // pseudo-hash); extractInfoHashFromMagnet normalises to hex40 only, so the old
  // second check could never see a local hash — it was dead code.
  const rawBtih = extractBtihFromMagnet(magnet)
  if (rawBtih && isLocalHash(rawBtih)) return synthesizeLocalInfo(rawBtih)
  const { data } = await api.post<TorrentInfo>('/stream/add', kind ? { magnet, kind } : { magnet })
  return data
}

export const streamAddTorrentFile = async (file: File): Promise<TorrentInfo> => {
  const fd = new FormData()
  fd.append('file', file)
  const { data } = await api.post<TorrentInfo>('/stream/add-file', fd, {
    headers: { 'Content-Type': 'multipart/form-data' }
  })
  return data
}

export const streamInfo = async (hash: string): Promise<TorrentInfo> => {
  if (isLocalHash(hash)) return localStreamInfo(hash)
  const { data } = await api.get<TorrentInfo>(`/stream/info/${hash}`)
  return data
}

export const streamDrop = async (hash: string): Promise<void> => {
  await api.delete(`/stream/${hash}`)
}

/** Perf #7: drop many torrents (+ HLS) in one POST instead of N DELETE /stream/:hash. */
export type StreamDropBatchResult = {
  dropped: number
  total: number
  failed?: string[]
}
export const streamDropBatch = async (hashes: string[]): Promise<StreamDropBatchResult> =>
  runChunked(hashes, BATCH_CAPS.streamDrop, async chunk => {
    const { data } = await api.post<StreamDropBatchResult>('/stream/drop/batch', { hashes: chunk })
    return data
  }, (a, b) => ({
    dropped: a.dropped + b.dropped,
    total: a.total + b.total,
    failed: [...(a.failed ?? []), ...(b.failed ?? [])],
  }), { dropped: 0, total: 0, failed: [] })

export const streamAudioMeta = async (hash: string, fileIdx: number): Promise<AudioMeta> => {
  const { data } = await api.get<AudioMeta>(`/stream/audio/meta/${hash}/${fileIdx}`)
  return data
}
