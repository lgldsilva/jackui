import { useMemo } from 'react'
import { SearchResult } from '../api/client'
import { groupByInfoHash } from './group'
import { isPlayable } from './playable'

// Subset de SearchResult que o filtro/ordenação realmente lê. Restringe o
// genérico pra qualquer SearchResult-like (ex.: CachedSearchResult que estende
// com `query`) — assim o hook preserva o tipo original e não derruba campos
// extras nos consumers.

// SortKey aceita ambas as variantes históricas ('date' em History, 'age' em
// Search) — coladas internamente porque significam a mesma coisa (ordenar por
// publishDate).
export type SortKey = 'seeders' | 'leechers' | 'size' | 'title' | 'date' | 'age'

export type ResultFilters = {
  minSeeders?: number
  minLeechers?: number
  maxBytes?: number
  trackerFilter?: string
  titleFilter?: string
  onlyPlayable?: boolean
}

export type UseFilteredResultsOpts = ResultFilters & {
  readonly sortKey: SortKey
  readonly sortAsc: boolean
}

// Aplica groupByInfoHash + filtros + sort. Retorna também groupedCount pra
// distinguir "reduzido por filtro" de "reduzido por dedup". TODO(onda 6):
// quando o backend agrupar no SSE, remover o groupByInfoHash daqui.
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
    sortKey,
    sortAsc,
  } = opts
  return useMemo(() => {
    const grouped = groupByInfoHash(input)
    const titleLower = titleFilter.toLowerCase()

    let r = grouped.filter(res => {
      if (res.seeders < minSeeders) return false
      if (res.leechers < minLeechers) return false
      if (res.size > maxBytes) return false
      if (trackerFilter !== 'all' && res.tracker !== trackerFilter) return false
      if (titleLower && !res.title.toLowerCase().includes(titleLower)) return false
      if (onlyPlayable && !isPlayable(res)) return false
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
  }, [input, minSeeders, minLeechers, maxBytes, trackerFilter, titleFilter, onlyPlayable, sortKey, sortAsc])
}
