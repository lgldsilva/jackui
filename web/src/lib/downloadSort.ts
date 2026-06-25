import type { DownloadEntry } from '../api/downloads'

// Sort keys handled CLIENT-SIDE. downRate/upRate/seeders are live (não persistidos
// no SQLite), então não podem ir no ORDER BY do backend — a ordenação por eles
// acontece aqui sobre a lista já enriquecida pelo handler. As demais chaves
// (created_at/name/size/...) continuam server-side.
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
// created_at order. dir 'desc' = maior primeiro (o padrão útil: mais rápido /
// mais seeds no topo).
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
      return a.i - b.i // estável: preserva a ordem original nos empates
    })
    .map(x => x.d)
}

// applyDownloadSort é o ponto de entrada usado pela página: ordena client-side
// quando sortCol é uma métrica ao vivo; caso contrário devolve a lista como veio
// (a ordem server-side por data/nome/... é preservada). Encapsular aqui mantém a
// DownloadsPage (god-file) sem ramificação extra.
export function applyDownloadSort(
  items: DownloadEntry[],
  sortCol: string,
  sortDir: string,
): DownloadEntry[] {
  if (!isLiveSortKey(sortCol)) return items
  return sortByLiveMetric(items, sortCol, sortDir === 'asc' ? 'asc' : 'desc')
}
