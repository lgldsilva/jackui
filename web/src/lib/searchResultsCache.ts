import type { SearchResult } from '../api/client'

// In-memory (module-level) cache of search results per tab. Search tabs are
// persisted to localStorage WITHOUT their results (see PersistedTab in
// SearchPage) — so navigating away and back used to wipe them. This cache
// survives SPA navigation (module state outlives the page component) and is
// intentionally lost on a full reload — keeping potentially hundreds of
// results out of localStorage. Post-reload recovery goes through the backend
// search cache instead (see useRehydratedResults).
export type SearchPhase = 'idle' | 'cache' | 'live' | 'done' | 'error'

export type SearchSummary = { total: number; live: number; cached: number }

export type CachedTabResults = {
  // Query the results belong to — guards against gluing old results onto a
  // tab whose query has since changed (e.g. stale localStorage in incognito).
  query: string
  results: SearchResult[]
  phase: SearchPhase
  summary: SearchSummary | null
  savedAt: number
}

export type SyncableTab = {
  id: string
  query: string
  results: SearchResult[]
  phase: SearchPhase
  summary: SearchSummary | null
}

// Cap so a long session with many tabs can't grow memory without bound:
// only the N most recently updated tabs keep their results.
const MAX_TABS = 8

// Map preserves insertion order — entries are re-inserted on update, so the
// first key is always the least recently updated (eviction victim).
const cache = new Map<string, CachedTabResults>()

export function getTabResults(tabId: string): CachedTabResults | undefined {
  return cache.get(tabId)
}

// True while at least one tab keeps results in memory. An EMPTY cache at
// SearchPage mount is the post-reload signal (module state dies with the
// page) — an SPA remount still sees whatever the session produced.
export function hasAnyTabResults(): boolean {
  return cache.size > 0
}

export function setTabResults(tabId: string, entry: Omit<CachedTabResults, 'savedAt'>): void {
  cache.delete(tabId) // re-insert so this tab becomes the most recent
  cache.set(tabId, { ...entry, savedAt: Date.now() })
  while (cache.size > MAX_TABS) {
    const oldest = cache.keys().next().value
    if (oldest === undefined) break
    cache.delete(oldest)
  }
}

export function deleteTabResults(tabId: string): void {
  cache.delete(tabId)
}

export function clearTabResults(): void {
  cache.clear()
}

// Mirrors the live tab state into the cache: tabs with results are upserted,
// tabs without results — and entries for closed tabs — are dropped. Skipping
// unchanged tabs (results is replaced immutably on every change, so reference
// equality suffices) keeps the recency order meaningful: a tab only becomes
// "most recent" when its results actually change.
//
// Known degradation with MORE than MAX_TABS live tabs holding results: an
// evicted entry is re-inserted by the next sync, evicting another live one —
// the retained set converges to "last MAX_TABS in array order" instead of
// true recency. Accepted: memory stays bounded, merge guards make a stale
// miss harmless, and >8 result-bearing tabs is already pathological.
export function syncTabsToCache(tabs: readonly SyncableTab[]): void {
  const alive = new Set(tabs.map(t => t.id))
  for (const id of [...cache.keys()]) {
    if (!alive.has(id)) cache.delete(id)
  }
  for (const t of tabs) {
    if (t.results.length === 0) {
      // A fresh search clears results before streaming new ones — the stale
      // entry must not outlive that (it belongs to the previous query).
      cache.delete(t.id)
      continue
    }
    const existing = cache.get(t.id)
    if (existing?.results === t.results && existing?.query === t.query && existing?.phase === t.phase) continue
    setTabResults(t.id, { query: t.query, results: t.results, phase: t.phase, summary: t.summary })
  }
}

// Pure merge used by SearchPage's hydrateTabs: restores the in-memory results
// into a tab rebuilt from localStorage. Never clobbers results the tab already
// has, and refuses entries whose query no longer matches the tab's. A search
// that was in flight when the page unmounted ('cache'/'live') comes back as
// 'done' — the EventSource was closed on unmount, mirroring stopSearch.
export function mergeCachedResults<T extends SyncableTab>(tab: T, entry: CachedTabResults | undefined): T {
  if (!entry || entry.results.length === 0) return tab
  if (tab.results.length > 0) return tab
  if (entry.query !== tab.query) return tab
  const phase: SearchPhase = entry.phase === 'error' ? 'error' : 'done'
  return { ...tab, results: entry.results, phase, summary: entry.summary }
}
