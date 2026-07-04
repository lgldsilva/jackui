// Legendas: busca no OpenSubtitles (/api/subtitles/*), auto-search por OS-hash e
// os sidecars .srt/.vtt que vivem DENTRO do torrent (ou ao lado do arquivo local).
// Detecta o pseudo info-hash local e roteia pro /api/local/*. Extraído de
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

// In-memory cache popularizado por streamSidecars(local) — mapeia
// `${hash}:${index}` → filename, lido por streamSidecarURL pra construir o
// `?name=`. Sem isso o backend teria que re-listar o dir a cada chamada.
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
