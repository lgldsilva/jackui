import { useMemo } from 'react'
import { SearchResult } from '../api/client'
import { groupByInfoHash } from './group'
import { isPlayable, isAudioResult } from './playable'

// Subset de SearchResult que o filtro/ordenação realmente lê. Restringe o
// genérico pra qualquer SearchResult-like (ex.: CachedSearchResult que estende
// com `query`) — assim o hook preserva o tipo original e não derruba campos
// extras nos consumers.

// SortKey aceita ambas as variantes históricas ('date' em History, 'age' em
// Search) — coladas internamente porque significam a mesma coisa (ordenar por
// publishDate).
export type SortKey = 'seeders' | 'leechers' | 'size' | 'title' | 'date' | 'age'

export type ResultFilters = {
  readonly minSeeders?: number
  readonly minLeechers?: number
  readonly maxBytes?: number
  readonly trackerFilter?: string
  readonly titleFilter?: string
  readonly onlyPlayable?: boolean
  readonly audioOnly?: boolean   // modo Música: mantém só resultados de áudio
  // Quality filters (onda 3). Empty/false = "qualquer".
  readonly resolution?: string // exact match against quality.resolution ('2160p'…)
  readonly hdrOnly?: boolean    // keep only HDR or Dolby Vision releases
  readonly codecGroup?: string  // normalized family: 'hevc' | 'h264' | 'av1'
}

// codecGroupOf normalizes the free-form quality.codec ('x265', 'HEVC', 'h.264',
// 'AV1'…) into a stable family so a single filter value matches every spelling.
export function codecGroupOf(codec?: string): string {
  if (!codec) return ''
  const c = codec.toLowerCase()
  if (c.includes('265') || c.includes('hevc')) return 'hevc'
  if (c.includes('264') || c.includes('avc')) return 'h264'
  if (c.includes('av1')) return 'av1'
  return 'other'
}

export type UseFilteredResultsOpts = ResultFilters & {
  readonly sortKey: SortKey
  readonly sortAsc: boolean
}

// Aplica groupByInfoHash + filtros + sort. Retorna também groupedCount pra
// distinguir "reduzido por filtro" de "reduzido por dedup".
export function useFilteredResults<T extends SearchResult>(
  input: T[],
  opts: UseFilteredResultsOpts,
): { filteredResults: T[]; groupedCount: number } {
  const {
    minSeeders = 0,
    minLeechers = 0,
    maxBytes = Infinity,
    trackerFilter = 'all',
    titleFilter = '',
    onlyPlayable = false,
    audioOnly = false,
    resolution = '',
    hdrOnly = false,
    codecGroup = '',
    sortKey,
    sortAsc,
  } = opts
  return useMemo(() => {
    // Tracker filter is applied BEFORE grouping. groupByInfoHash folds the same
    // release from multiple trackers into one card (with the rest in `alsoIn`),
    // so filtering AFTER grouping only matched the primary tracker yet still
    // surfaced the others — "filtered by one provider, got several". Pre-filtering
    // means only the chosen provider's listings survive, the merge has nothing
    // cross-provider to fold, and each card shows just that provider.
    const scoped = trackerFilter === 'all'
      ? input
      : input.filter(res => res.tracker === trackerFilter)
    const grouped = groupByInfoHash(scoped)
    const titleLower = titleFilter.toLowerCase()

    let r = grouped.filter(res => {
      if (res.seeders < minSeeders) return false
      if (res.leechers < minLeechers) return false
      if (res.size > maxBytes) return false
      if (titleLower && !res.title.toLowerCase().includes(titleLower)) return false
      if (onlyPlayable && !isPlayable(res)) return false
      if (audioOnly && !isAudioResult(res)) return false
      if (resolution && res.quality?.resolution !== resolution) return false
      if (hdrOnly && !(res.quality?.hdr || res.quality?.dv)) return false
      if (codecGroup && codecGroupOf(res.quality?.codec) !== codecGroup) return false
      return true
    })

    r = [...r].sort((a, b) => {
      let diff = 0
      switch (sortKey) {
        case 'seeders':  diff = b.seeders - a.seeders; break
        case 'leechers': diff = b.leechers - a.leechers; break
        case 'size':     diff = b.size - a.size; break
        case 'title':    diff = a.title.localeCompare(b.title); break
        case 'date':
        case 'age':      diff = b.publishDate.localeCompare(a.publishDate); break
      }
      return sortAsc ? -diff : diff
    })

    return { filteredResults: r, groupedCount: grouped.length }
  }, [input, minSeeders, minLeechers, maxBytes, trackerFilter, titleFilter, onlyPlayable, audioOnly, resolution, hdrOnly, codecGroup, sortKey, sortAsc])
}
