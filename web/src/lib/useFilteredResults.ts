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

type MatchFilters = {
  minSeeders: number
  minLeechers: number
  maxBytes: number
  titleLower: string
  onlyPlayable: boolean
  audioOnly: boolean
  resolution: string
  hdrOnly: boolean
  codecGroup: string
}

// Pure predicate so Sonar doesn't count the multi-clause filter as cognitive
// complexity inside the useMemo callback (S3776).
function matchesResultFilters(res: SearchResult, f: MatchFilters): boolean {
  // -1 = contagem DESCONHECIDA (vários trackers/Jackett não expõem o número).
  // Tratar como "não filtrar por contagem": só rejeita valores CONHECIDOS
  // (>= 0) abaixo do mínimo. Sem o guard `>= 0`, `-1 < 0` derrubava esses
  // resultados mesmo com o mínimo em 0 — e "limpar filtros" nunca os revelava.
  if (res.seeders >= 0 && res.seeders < f.minSeeders) return false
  if (res.leechers >= 0 && res.leechers < f.minLeechers) return false
  if (res.size > f.maxBytes) return false
  if (f.titleLower && !res.title.toLowerCase().includes(f.titleLower)) return false
  if (f.onlyPlayable && !isPlayable(res)) return false
  if (f.audioOnly && !isAudioResult(res)) return false
  if (f.resolution && res.quality?.resolution !== f.resolution) return false
  if (f.hdrOnly && !(res.quality?.hdr || res.quality?.dv)) return false
  if (f.codecGroup && codecGroupOf(res.quality?.codec) !== f.codecGroup) return false
  return true
}

function compareResults(a: SearchResult, b: SearchResult, sortKey: SortKey, sortAsc: boolean): number {
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
    const filters: MatchFilters = {
      minSeeders, minLeechers, maxBytes,
      titleLower: titleFilter.toLowerCase(),
      onlyPlayable, audioOnly, resolution, hdrOnly, codecGroup,
    }
    const r = grouped
      .filter(res => matchesResultFilters(res, filters))
      .slice()
      .sort((a, b) => compareResults(a, b, sortKey, sortAsc))
    return { filteredResults: r, groupedCount: grouped.length }
  }, [input, minSeeders, minLeechers, maxBytes, trackerFilter, titleFilter, onlyPlayable, audioOnly, resolution, hdrOnly, codecGroup, sortKey, sortAsc])
}
