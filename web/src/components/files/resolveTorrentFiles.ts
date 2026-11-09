import type { SearchResult, StreamFile, TorrentInfo } from '../../api/client'
import { canonicalInfoHash } from '../../lib/magnet'

export interface ResolveDeps {
  metadata: (hash: string) => Promise<TorrentInfo | null>
  add: (source: string) => Promise<TorrentInfo>
}

export type ResolvedFiles =
  | { files: StreamFile[]; error?: undefined }
  | { files?: undefined; error: string }

/**
 * Resolves a torrent's file list for the download picker: metadata cache first
 * (instant when the torrent was played/downloaded before), then streamAdd.
 * A streamAdd failure keeps ITS message (fallbackFailed only when it has
 * none); an add that succeeds without files yields notYet. Never rejects.
 */
export async function resolveTorrentFiles(
  result: Partial<Pick<SearchResult, 'magnetUri' | 'infoHash' | 'link'>>,
  deps: ResolveDeps,
  msgs: { notYet: string; fallbackFailed: string },
): Promise<ResolvedFiles> {
  const magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
  const infoHash = result.infoHash || canonicalInfoHash(result.infoHash, magnet)
  const source = magnet || result.link || ''
  let info: TorrentInfo | null = null
  if (infoHash) info = await deps.metadata(infoHash).catch(() => null)
  if (!info && source) {
    try {
      info = await deps.add(source)
    } catch (e) {
      return { error: (e as Error)?.message || msgs.fallbackFailed }
    }
  }
  const files = info?.files ?? []
  return files.length > 0 ? { files } : { error: msgs.notYet }
}
