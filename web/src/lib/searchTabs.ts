// Search tab state/persistence — extracted from SearchPage.tsx (pure module:
// types + pure functions, no JSX). The tab counter becomes nextTabId() (via uid()).
import { load, save } from './storage'
import type { SearchResult } from '../api/client'
import { mergeCachedResults, getTabResults } from './searchResultsCache'
import type { SearchPhase } from './searchResultsCache'
import { appendUnique } from './searchStream'
import { isIncognito } from './incognito'
import { uid } from './uid'

export const TABS_KEY = 'searchTabs'
export const ACTIVE_KEY = 'activeTabId'
// Last-used filter preferences, applied to every NEW tab/search so a setting
// like "min 10 seeders" sticks instead of resetting to 0 on each fresh search.
export const FILTER_DEFAULTS_KEY = 'searchFilterDefaults'
// One-shot flag: fixes old persisted browser filters that hid
// results — `onlyPlayable` on killed any torrent without a magnet (private
// trackers like amigos-share only expose the .torrent), and `minSeeders=0` let
// dead torrents through. Migrates once to the new defaults.
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
  // minSeeders=1 is the only filter on by default: hides dead torrents
  // (0 seeds) without touching anything else. onlyPlayable always starts off and is
  // not persisted — it used to silently hide content without a magnet.
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
  // Quality filters (wave 3). Per-tab, persisted; not part of the global
  // FilterDefaults (quality is per-search, unlike "min seeders").
  resolution: string
  hdrOnly: boolean
  codecGroup: string
}

export function newTab(id: string = uid()): TabState {
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
    // Never inherited/persisted: the toggle applies to the current session only.
    onlyPlayable: false,
    resolution: '', hdrOnly: false, codecGroup: '',
  }
}

export function hydrateTabs(): { tabs: TabState[]; activeId: string } {
  // One-shot migration of the defaults: floor minSeeders at 1 (we don't lower a
  // value the user raised) and onlyPlayable off.
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
    const id = uid()
    return { tabs: [newTab(id)], activeId: id }
  }

  // Deduplicate any corrupted/collided IDs from legacy persisted state
  const seenIds = new Set<string>()
  // onlyPlayable is never restored (it stopped hiding no-magnet results); in the
  // initial migration, tabs that were at 0 seeds move to 1 — without touching values >0.
  const tabs = persisted.map(p => {
    let id = p.id
    if (!id || seenIds.has(id)) {
      id = uid()
    }
    seenIds.add(id)
    const t = { ...newTab(id), ...p, id, onlyPlayable: false }
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

// nextTabId returns a unique tab id (UUID), replacing the
// sequential counter that suffered collisions in parallel tabs.
export function nextTabId(): string {
  return uid()
}
