import type { DownloadEntry } from '../api/downloads'
import type { LibraryEntry } from '../api/library'

export const HOME_CONTINUE_LIMIT = 16
export const HOME_COMPLETED_LIMIT = 12
export const HOME_TRENDING_LIMIT = 16
export const HOME_RECS_LIMIT = 16

export function isUnfinished(entry: LibraryEntry): boolean {
  if (entry.durationSeconds <= 0) return entry.resumeSeconds > 0
  const ratio = entry.resumeSeconds / entry.durationSeconds
  return ratio > 0 && ratio < 0.95
}

// pickContinueWatching keeps unfinished titles, most-recent first (library
// list is already recency-sorted). Finished rows belong on /library, not Home.
export function pickContinueWatching(entries: readonly LibraryEntry[], limit = HOME_CONTINUE_LIMIT): LibraryEntry[] {
  const out: LibraryEntry[] = []
  for (const e of entries) {
    if (!isUnfinished(e)) continue
    out.push(e)
    if (out.length >= limit) break
  }
  return out
}

// pickRecentlyCompleted keeps one card per torrent (multi-file packs collapse)
// in completion order. Rows without an infoHash cannot be played from Home.
export function pickRecentlyCompleted(rows: readonly DownloadEntry[], limit = HOME_COMPLETED_LIMIT): DownloadEntry[] {
  const seen = new Set<string>()
  const out: DownloadEntry[] = []
  for (const row of rows) {
    if (row.status !== 'completed' || !row.infoHash) continue
    const key = row.infoHash.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(row)
    if (out.length >= limit) break
  }
  return out
}

export function downloadToPlayTarget(row: DownloadEntry): { infoHash: string; magnet: string; name: string; fileIndex?: number } {
  const fileIndex = row.fileIndex >= 0 ? row.fileIndex : undefined
  return { infoHash: row.infoHash, magnet: row.magnet, name: row.name, fileIndex }
}

// homePlayFileIndex mirrors LibraryPage: last watched file wins, including
// index 0. Only fall back to a strictly-positive primary; 0 there is the
// column default and must not override the server's pickPrimaryFile.
export function homePlayFileIndex(e: { lastFileIndex: number; primaryFileIndex: number }): number | undefined {
  if (e.lastFileIndex >= 0) return e.lastFileIndex
  if (e.primaryFileIndex > 0) return e.primaryFileIndex
  return undefined
}

export function homeIsEmpty(opts: {
  readonly continueCount: number
  readonly recentCount: number
  readonly recCount: number
  readonly trendCount: number
  readonly albumCount: number
}): boolean {
  return opts.continueCount + opts.recentCount + opts.recCount + opts.trendCount + opts.albumCount === 0
}
