import { useEffect, useRef } from 'react'
import { searchCache } from '../api/client'
import type { SearchResult } from '../api/client'
import type { SearchPhase } from './searchResultsCache'

// Post-RELOAD rehydration: the in-memory results cache (searchResultsCache)
// dies with the page, so after a full reload a restored tab has its query and
// filters but zero results. For the ACTIVE tab only, silently ask the backend
// search cache (GET /api/history/cache?q=) — never a live Jackett search — and
// fill the tab if something comes back. Empty cache → the tab stays as it is.

type RehydratableTab = {
  query: string
  phase: SearchPhase
  resultCount: number
}

// Decision: only an idle tab with a non-empty query and no results needs
// rehydration. A tab that is searching ('cache'/'live'), already populated,
// or finished ('done'/'error') is left alone.
export function shouldRehydrate(tab: RehydratableTab): boolean {
  return tab.phase === 'idle' && tab.resultCount === 0 && tab.query.trim() !== ''
}

// Apply-time guard: the fetch is async — by the time cached results arrive the
// user may have started a live search, typed another query, or the results may
// have been filled by other means. Only an unchanged, still-idle, still-empty
// tab accepts them; an in-flight search is NEVER overwritten.
export function canApplyRehydrated(
  tab: { id: string; query: string; phase: SearchPhase; results: readonly unknown[] },
  tabId: string,
  query: string,
): boolean {
  return tab.id === tabId
    && tab.phase === 'idle'
    && tab.results.length === 0
    && tab.query.trim() === query.trim()
}

export function useRehydratedResults(
  tabId: string,
  query: string,
  phase: SearchPhase,
  resultCount: number,
  apply: (tabId: string, query: string, results: SearchResult[]) => void,
) {
  // One attempt per (tab, query): a cache miss must not re-fire on every
  // tab switch / render while the tab stays idle and empty.
  const attempted = useRef(new Set<string>())
  useEffect(() => {
    if (!shouldRehydrate({ query, phase, resultCount })) return
    const trimmed = query.trim()
    const key = `${tabId}|${trimmed}`
    if (attempted.current.has(key)) return
    attempted.current.add(key)
    let cancelled = false
    searchCache(trimmed)
      .then(results => {
        if (!cancelled && results.length > 0) apply(tabId, query, results)
      })
      .catch(() => { /* offline or no cache: leave the tab as-is */ })
    return () => { cancelled = true }
  }, [tabId, query, phase, resultCount, apply])
}
