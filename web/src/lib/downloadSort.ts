import type { DownloadEntry } from '../api/downloads'

// Sort keys handled CLIENT-SIDE. downRate/upRate/seeders are live (not persisted
// to SQLite), so they can't go into the backend's ORDER BY — sorting by them
// happens here over the list already enriched by the handler. The other keys
// (created_at/name/size/...) stay server-side.
export const LIVE_SORT_KEYS = ['downRate', 'upRate', 'seeders'] as const
export type LiveSortKey = (typeof LIVE_SORT_KEYS)[number]

export function isLiveSortKey(key: string): key is LiveSortKey {
  return (LIVE_SORT_KEYS as readonly string[]).includes(key)
}

function liveValue(d: DownloadEntry, key: LiveSortKey): number {
  switch (key) {
    case 'downRate': return d.downRate || 0
    case 'upRate': return d.upRate || 0
    case 'seeders': return d.seeders || 0
  }
}

// sortByLiveMetric returns a NEW array ordered by a live metric. Ties keep the
// input order (stable) so rows with equal/zero metric stay in the backend's
// created_at order. dir 'desc' = largest first (the useful default: fastest /
// most seeds on top).
export function sortByLiveMetric(
  items: readonly DownloadEntry[],
  key: LiveSortKey,
  dir: 'asc' | 'desc',
): DownloadEntry[] {
  const sign = dir === 'asc' ? 1 : -1
  return items
    .map((d, i) => ({ d, i }))
    .sort((a, b) => {
      const diff = liveValue(a.d, key) - liveValue(b.d, key)
      if (diff !== 0) return sign * diff
      return a.i - b.i // stable: preserves the original order on ties
    })
    .map(x => x.d)
}

// applyDownloadSort is the entry point used by the page: sorts client-side
// when sortCol is a live metric; otherwise returns the list as it came
// (the server-side order by date/name/... is preserved). Encapsulating it here keeps
// the DownloadsPage (god-file) free of extra branching.
export function applyDownloadSort(
  items: DownloadEntry[],
  sortCol: string,
  sortDir: string,
): DownloadEntry[] {
  if (!isLiveSortKey(sortCol)) return items
  return sortByLiveMetric(items, sortCol, sortDir === 'asc' ? 'asc' : 'desc')
}
