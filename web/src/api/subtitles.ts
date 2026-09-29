// Subtitles: OpenSubtitles search (/api/subtitles/*), OS-hash auto-search and
// the .srt/.vtt sidecars that live INSIDE the torrent (or next to the local file).
// Detects the local pseudo info-hash and routes to /api/local/*. Extracted from
// client.ts (god-file, #417).
import { api, withToken } from './http'
import { isLocalHash, parseLocalHash, localQS } from './local'

// ─── Sidecar subtitles inside torrent ──────────────────────────────────────

export type SidecarSubtitle = {
  index: number
  path: string
  size: number
  language: string
  format: 'srt' | 'vtt' | 'ass' | 'ssa' | 'sub'
}

// In-memory cache popularized by streamSidecars(local) — maps
// `${hash}:${index}` → filename, read by streamSidecarURL to build the
// `?name=`. Without it the backend would have to re-list the dir on every call.
const localSidecarNameCache = new Map<string, string>()

export const streamSidecars = async (hash: string, fileIdx: number): Promise<SidecarSubtitle[]> => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    type LocalSub = { name: string; size: number; language: string; format: SidecarSubtitle['format']; match: number }
    const { data } = await api.get<LocalSub[]>(`/local/sidecars?${localQS(loc.mount, loc.path)}`)
    return data.map((s, i) => {
      localSidecarNameCache.set(`${hash}:${i}`, s.name)
      return {
        index: i,
        path: s.name,
        size: s.size,
        language: s.language,
        format: s.format,
      }
    })
  }
  const { data } = await api.get<SidecarSubtitle[]>(`/stream/sidecars/${hash}/${fileIdx}`)
  return data
}
export const streamSidecarURL = (hash: string, fileIdx: number, tokenOverride?: string): string => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    const name = localSidecarNameCache.get(`${hash}:${fileIdx}`) ?? ''
    if (name) {
      return withToken(`/api/local/sidecar?${localQS(loc.mount, loc.path)}&name=${encodeURIComponent(name)}`, tokenOverride)
    }
    return withToken(`/api/local/sidecar?${localQS(loc.mount, loc.path)}&index=${fileIdx}`, tokenOverride)
  }
  return withToken(`/api/stream/sidecar/${hash}/${fileIdx}`, tokenOverride)
}

// ─── Subtitles ──────────────────────────────────────────────────────────────

export type Subtitle = {
  id: string
  language: string
  release: string
  url: string
  uploaderName: string
  downloads: number
  hearingImpaired: boolean
  trusted: boolean
}

export const subtitlesEnabled = async (): Promise<boolean> => {
  const { data } = await api.get<{ enabled: boolean }>('/subtitles/enabled')
  return data.enabled
}

export const subtitlesSearch = async (
  q: string,
  opts: { season?: number; episode?: number; langs?: string } = {},
): Promise<Subtitle[]> => {
  const params = new URLSearchParams({ q })
  if (opts.langs) params.set('langs', opts.langs)
  if (opts.season) params.set('season', String(opts.season))
  if (opts.episode) params.set('episode', String(opts.episode))
  const { data } = await api.get<Subtitle[]>(`/subtitles/search?${params}`)
  return data
}

export const subtitleDownloadURL = (fileId: string, tokenOverride?: string): string =>
  withToken(`/api/subtitles/download/${fileId}`, tokenOverride)

export type AutoSubtitlesResponse = {
  osHash: string
  osSize: number
  hashErr?: string
  file: string
  results: Subtitle[]
}

// Stremio-style auto subtitle search: uses OS file hash from the active stream
export const subtitlesAuto = async (
  hash: string,
  fileIdx: number,
  langs = 'pt-BR,pt',
): Promise<AutoSubtitlesResponse> => {
  if (isLocalHash(hash)) {
    const loc = parseLocalHash(hash)!
    const { data } = await api.get<AutoSubtitlesResponse>(
      `/local/subtitles/auto?${localQS(loc.mount, loc.path)}&langs=${encodeURIComponent(langs)}`,
    )
    return data
  }
  const { data } = await api.get<AutoSubtitlesResponse>(
    `/subtitles/auto/${hash}/${fileIdx}?langs=${encodeURIComponent(langs)}`,
  )
  return data
}
