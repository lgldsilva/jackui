import api, { withToken } from './http'
import { parseLocalHash, getLocalViewAsUser } from './client'

// Universal-viewer API (GET /api/preview/*). Every endpoint takes the SAME
// source identification the rest of the app already uses: a torrent infoHash +
// file index, or the `local-...` pseudo-hash that encodes mount+path. This
// module translates the pseudo-hash so callers (viewers) stay source-agnostic.

export type ArchiveEntry = { name: string; size: number }
export type ArchiveListing = { format: string; entries: ArchiveEntry[]; truncated: boolean }
export type EpubManifest = { title: string; chapters: string[] }

// previewParams builds the source query (?hash=&idx= or ?mount=&path=[&user=]).
export function previewParams(infoHash: string, fileIdx: number): URLSearchParams {
  const loc = parseLocalHash(infoHash)
  if (loc) {
    const p = new URLSearchParams({ mount: loc.mount, path: loc.path })
    const viewAs = getLocalViewAsUser()
    if (viewAs) p.set('user', viewAs)
    return p
  }
  return new URLSearchParams({ hash: infoHash, idx: String(fileIdx) })
}

export async function previewArchiveList(infoHash: string, fileIdx: number): Promise<ArchiveListing> {
  const { data } = await api.get(`/preview/archive?${previewParams(infoHash, fileIdx)}`)
  return { format: data?.format ?? '', entries: data?.entries ?? [], truncated: !!data?.truncated }
}

export function previewArchiveEntryURL(infoHash: string, fileIdx: number, name: string, token?: string): string {
  const p = previewParams(infoHash, fileIdx)
  p.set('name', name)
  return withToken(`/api/preview/archive/entry?${p}`, token)
}

export async function previewComicManifest(infoHash: string, fileIdx: number): Promise<string[]> {
  const { data } = await api.get(`/preview/comic?${previewParams(infoHash, fileIdx)}`)
  return data?.pages ?? []
}

export function previewComicPageURL(infoHash: string, fileIdx: number, name: string, token?: string): string {
  const p = previewParams(infoHash, fileIdx)
  p.set('name', name)
  return withToken(`/api/preview/comic/page?${p}`, token)
}

export async function previewEpubManifest(infoHash: string, fileIdx: number): Promise<EpubManifest> {
  const { data } = await api.get(`/preview/epub?${previewParams(infoHash, fileIdx)}`)
  return { title: data?.title ?? '', chapters: data?.chapters ?? [] }
}

export function previewEpubChapterURL(infoHash: string, fileIdx: number, name: string, token?: string): string {
  const p = previewParams(infoHash, fileIdx)
  p.set('name', name)
  return withToken(`/api/preview/epub/chapter?${p}`, token)
}

// previewRawURL — direct bytes of the container/file itself (download links,
// <img>, PDF iframe). Local pseudo-hashes go to /api/local/file; torrents to
// /api/stream/:hash/:idx.
export function previewRawURL(infoHash: string, fileIdx: number, token?: string): string {
  const loc = parseLocalHash(infoHash)
  if (loc) {
    const p = new URLSearchParams({ mount: loc.mount, path: loc.path })
    const viewAs = getLocalViewAsUser()
    if (viewAs) p.set('user', viewAs)
    return withToken(`/api/local/file?${p}`, token)
  }
  return withToken(`/api/stream/${infoHash}/${fileIdx}`, token)
}
