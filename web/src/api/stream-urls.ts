// URL builders e art resolve para /api/stream/*. Extraído de stream.ts (R3).
import { api, withToken } from './http'
import { isLocalHash, localQS, parseLocalHash } from './local-base'
import { localResolvedURL } from './local'

export const streamArtURL = (hash: string): string =>
  withToken(`/api/stream/art/${hash}`)

export type ArtResolveResult = { source?: string; reused?: boolean; resolved?: boolean; status?: string }

export const resolveArt = async (hash: string, fileIdx = -1, name?: string): Promise<string | null> => {
  if (isLocalHash(hash)) return null
  try {
    const params = new URLSearchParams({ file: String(fileIdx) })
    if (name) params.set('name', name)
    const { data, status } = await api.post(`/stream/art/${hash}/resolve?${params.toString()}`)
    return status === 200 ? (data?.source ?? null) : null
  } catch {
    return null
  }
}

/** Batch art resolve — 1 round-trip for library lists (Perf #6). */
export const resolveArtBatch = async (
  items: { hash: string; name?: string; file?: number }[],
): Promise<Record<string, ArtResolveResult>> => {
  if (items.length === 0) return {}
  try {
    const { data } = await api.post<{ results?: Record<string, ArtResolveResult> }>(
      '/stream/art/resolve/batch',
      { items },
    )
    return data?.results ?? {}
  } catch {
    return {}
  }
}

export const streamFileURL = (hash: string, fileIdx: number, tokenOverride?: string): string => {
  if (isLocalHash(hash)) return localResolvedURL(hash, tokenOverride)
  return withToken(`/api/stream/${hash}/${fileIdx}`, tokenOverride)
}

export const streamThumbnailURL = (hash: string, fileIdx: number, atSeconds: number): string =>
  withToken(`/api/stream/thumb/${hash}/${fileIdx}?at=${Math.max(0, Math.floor(atSeconds))}`)

export const streamArtworkURL = (hash: string, fileIdx: number, tokenOverride?: string): string =>
  withToken(`/api/stream/artwork/${hash}/${fileIdx}`, tokenOverride)

export const streamPlaylistM3UURL = (hash: string, fileIdx: number, transcode?: 'h264' | 'hevc'): string => {
  const base = `/api/stream/playlist/${hash}/${fileIdx}`
  const url = withToken(base)
  if (transcode) {
    const separator = url.includes('?') ? '&' : '?'
    return `${url}${separator}transcode=${transcode}`
  }
  return url
}

export const streamSubtrackURL = (hash: string, fileIdx: number, trackIdx: number, tokenOverride?: string): string => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    return withToken(`/api/local/subtrack?${localQS(loc.mount, loc.path)}&track=${trackIdx}`, tokenOverride)
  }
  return withToken(`/api/stream/subtrack/${hash}/${fileIdx}/${trackIdx}`, tokenOverride)
}

export const streamHLSMasterURL = (hash: string, fileIdx: number, tokenOverride?: string, audioTrack?: number): string => {
  const hasAudio = audioTrack != null && audioTrack >= 0
  if (isLocalHash(hash)) {
    const local = localResolvedURL(hash, tokenOverride)
    if (!local || !hasAudio) return local
    return local + (local.includes('?') ? '&' : '?') + `audio=${audioTrack}`
  }
  const audioQ = hasAudio ? `?audio=${audioTrack}` : ''
  return withToken(`/api/stream/hls/${hash}/${fileIdx}/index.m3u8${audioQ}`, tokenOverride)
}
