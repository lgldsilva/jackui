// Estado/persistência das abas de busca — extraído do SearchPage.tsx (móvel puro:
// types + funções puras, sem JSX). O contador de abas vira nextTabId().
import { load, save } from './storage'
import type { SearchResult } from '../api/client'
import { mergeCachedResults, getTabResults } from './searchResultsCache'
import type { SearchPhase } from './searchResultsCache'
import { appendUnique } from './searchStream'
import { isIncognito } from './incognito'

export const TABS_KEY = 'searchTabs'
export const ACTIVE_KEY = 'activeTabId'
// Last-used filter preferences, applied to every NEW tab/search so a setting
// like "min 10 seeders" sticks instead of resetting to 0 on each fresh search.
export const FILTER_DEFAULTS_KEY = 'searchFilterDefaults'
// One-shot flag: corrige filtros antigos persistidos no browser que escondiam
// resultados — `onlyPlayable` ligado matava qualquer torrent sem magnet (trackers
// privados como o jackui só expõem o .torrent), e `minSeeders=0` deixava
// passar torrents mortos. Migra uma vez para os novos defaults.
export const FILTER_MIGRATION_KEY = 'searchFiltersMigratedV1'

export type FilterDefaults = {
  trackerFilter: string
  minSeeders: number
  minLeechers: number
  maxSizeGb: string
  resultSort: ResultSortKey
  resultSortAsc: boolean
  onlyPlayable: boolean
}

export const FALLBACK_FILTERS: FilterDefaults = {
  // minSeeders=1 é o único filtro ligado por padrão: esconde torrents mortos
  // (0 seeds) sem mexer em mais nada. onlyPlayable nasce sempre desligado e não
  // é persistido — antes ele escondia silenciosamente conteúdo sem magnet.
  trackerFilter: 'all', minSeeders: 1, minLeechers: 0, maxSizeGb: '',
  resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
}

// What we persist (NOT the live SSE results — those re-fetch when the user re-searches)
export type PersistedTab = {
  id: string
  query: string
  selectedIndexers: string[]
  selectedCategory: string
  titleFilter: string
  trackerFilter: string
  minSeeders: number
  minLeechers: number
  maxSizeGb: string
  resultSort: ResultSortKey
  resultSortAsc: boolean
  onlyPlayable: boolean
  resolution: string
  hdrOnly: boolean
  codecGroup: string
}

export type ResultSortKey = 'seeders' | 'leechers' | 'size' | 'title' | 'age'

export type TabState = {
  id: string
  query: string
  results: SearchResult[]
  phase: SearchPhase
  error: string
  summary: { total: number; live: number; cached: number } | null
  selectedIndexers: string[]
  selectedCategory: string
  // Filters (per-tab, persisted across tab switches)
  titleFilter: string
  trackerFilter: string
  minSeeders: number
  minLeechers: number
  maxSizeGb: string
  resultSort: ResultSortKey
  resultSortAsc: boolean
  onlyPlayable: boolean
  // Quality filters (onda 3). Per-tab, persisted; not part of the global
  // FilterDefaults (quality is per-search, unlike "min seeders").
  resolution: string
  hdrOnly: boolean
  codecGroup: string
}

export function newTab(id: string): TabState {
  // Seed filters from the user's last-used preferences so a new search keeps
  // e.g. the "min 10 seeders" threshold instead of starting at zero.
  const d = load<FilterDefaults>(FILTER_DEFAULTS_KEY, FALLBACK_FILTERS)
  return {
    id, query: '', results: [], phase: 'idle', error: '', summary: null,
    selectedIndexers: [], selectedCategory: 'all',
    titleFilter: '',
    trackerFilter: d.trackerFilter,
    minSeeders: d.minSeeders, minLeechers: d.minLeechers, maxSizeGb: d.maxSizeGb,
    resultSort: d.resultSort, resultSortAsc: d.resultSortAsc,
    // Nunca herdado/persistido: o toggle vale só para a sessão atual.
    onlyPlayable: false,
    resolution: '', hdrOnly: false, codecGroup: '',
  }
}

let tabCounter = 1

export function hydrateTabs(): { tabs: TabState[]; activeId: string } {
  // Migração one-shot dos defaults: floor de minSeeders em 1 (não derrubamos um
  // valor que o usuário tenha subido) e onlyPlayable desligado.
  const migrated = load<boolean>(FILTER_MIGRATION_KEY, false)
  if (!migrated) {
    const d = load<FilterDefaults>(FILTER_DEFAULTS_KEY, FALLBACK_FILTERS)
    if (d.minSeeders < 1 || d.onlyPlayable) {
      save<FilterDefaults>(FILTER_DEFAULTS_KEY, { ...d, minSeeders: Math.max(1, d.minSeeders), onlyPlayable: false })
    }
  }

  const persisted = load<PersistedTab[]>(TABS_KEY, [])
  if (persisted.length === 0) {
    if (!migrated) save(FILTER_MIGRATION_KEY, true)
    const id = String(tabCounter++)
    return { tabs: [newTab(id)], activeId: id }
  }
  // Restore counter so new tabs get unique IDs beyond persisted ones
  const maxId = persisted.reduce((m, t) => Math.max(m, Number.parseInt(t.id) || 0), 0)
  tabCounter = maxId + 1
  // onlyPlayable nunca é restaurado (deixou de esconder sem-magnet); na migração
  // inicial, abas que estavam em 0 seeds passam a 1 — sem mexer em valores >0.
  const tabs = persisted.map(p => {
    const t = { ...newTab(p.id), ...p, onlyPlayable: false }
    if (!migrated && t.minSeeders < 1) t.minSeeders = 1
    // localStorage never stores results — pull them back from the in-memory
    // cache (same tab id + same query) so SPA navigation keeps the search.
    return mergeCachedResults(t, getTabResults(t.id))
  })
  if (!migrated) save(FILTER_MIGRATION_KEY, true)
  const savedActive = load<string>(ACTIVE_KEY, '')
  const activeId = tabs.some(t => t.id === savedActive) ? savedActive : tabs[0].id
  return { tabs, activeId }
}

export function persistTabs(tabs: TabState[], activeId: string) {
  // Incognito must not leave a trace in the browser either — skip persisting
  // the search tabs/queries to localStorage while it's active (the backend
  // already skips history/library writes).
  if (isIncognito()) return
  const stripped: PersistedTab[] = tabs.map(t => ({
    id: t.id,
    query: t.query,
    selectedIndexers: t.selectedIndexers,
    selectedCategory: t.selectedCategory,
    titleFilter: t.titleFilter,
    trackerFilter: t.trackerFilter,
    minSeeders: t.minSeeders,
    minLeechers: t.minLeechers,
    maxSizeGb: t.maxSizeGb,
    resultSort: t.resultSort,
    resultSortAsc: t.resultSortAsc,
    onlyPlayable: t.onlyPlayable,
    resolution: t.resolution,
    hdrOnly: t.hdrOnly,
    codecGroup: t.codecGroup,
  }))
  save(TABS_KEY, stripped)
  save(ACTIVE_KEY, activeId)
}

// appendResult dedupes by infoHash (or tracker|title|size) — an SSE reconnect
// replays the backend's cache phase, so re-received results must be absorbed
// instead of duplicating cards. Returns `prev` untouched on a duplicate so
// React skips the re-render.
export function appendResult(prev: TabState[], tabId: string, result: SearchResult): TabState[] {
  const tab = prev.find(t => t.id === tabId)
  if (!tab) return prev
  const next = appendUnique(tab.results, result)
  if (next === tab.results) return prev
  return prev.map(t => t.id === tabId ? { ...t, results: next as SearchResult[] } : t)
}

export function setErrorMsg(prev: TabState[], tabId: string, message: string): TabState[] {
  return prev.map(t => t.id === tabId ? { ...t, error: message } : t)
}

// nextTabId devolve um id único de aba (contador do módulo), substituindo o
// `tabCounter++` inline que morava no SearchPage.
export function nextTabId(): string {
  return String(tabCounter++)
}
