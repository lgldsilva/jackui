import { StreamFavorite } from '../api/client'

// Sort options for the favorites list. seeds/size come from the metadata cache
// (enriched server-side); date/name are always present on the favorite.
export type SortKey = 'date' | 'name' | 'seeds' | 'size'
export type SortDir = 'asc' | 'desc'

export const SORT_LABELS: Record<SortKey, string> = {
  date: 'Data de adição',
  name: 'Nome',
  seeds: 'Seeds',
  size: 'Tamanho',
}

// Raw comparator (ascending). Unknown seeds (never probed) and size (never
// resolved) collapse to the lowest value so they land last on desc — the
// default direction the user picks for "most seeds / biggest first".
function compareFavs(a: StreamFavorite, b: StreamFavorite, key: SortKey): number {
  switch (key) {
    case 'name': return a.name.localeCompare(b.name)
    case 'seeds': return (a.seeders ?? -1) - (b.seeders ?? -1)
    case 'size': return (a.totalSize ?? 0) - (b.totalSize ?? 0)
    default: return new Date(a.favoritedAt).getTime() - new Date(b.favoritedAt).getTime()
  }
}

export function sortFavorites(list: StreamFavorite[], key: SortKey, dir: SortDir): StreamFavorite[] {
  const mul = dir === 'asc' ? 1 : -1
  return [...list].sort((a, b) => compareFavs(a, b, key) * mul)
}
