import { useEffect, useRef } from 'react'
import { getHistoryResults } from '../api/client'
import type { SearchResult } from '../api/client'
import type { SearchPhase } from './searchResultsCache'
import { hasAnyTabResults } from './searchResultsCache'

// Post-RELOAD rehydration: the in-memory results cache (searchResultsCache)
// dies with the page, so after a full reload a restored tab has its query and
// filters but zero results. For tabs restored AT MOUNT only, silently ask the
// backend for the cached results of that exact query (GET /api/history/results
// ?q=) — never a live Jackett search — and fill the tab if something comes
// back. Empty cache → the tab stays as it is.

type RehydratableTab = {
  query: string
  phase: SearchPhase
  resultCount: number
}

type CandidateTab = {
  id: string
  query: string
  phase: SearchPhase
  results: readonly unknown[]
}

// Decision: only an idle tab with a non-empty query and no results needs
// rehydration. A tab that is searching ('cache'/'live'), already populated,
// or finished ('done'/'error') is left alone.
export function shouldRehydrate(tab: RehydratableTab): boolean {
  return tab.phase === 'idle' && tab.resultCount === 0 && tab.query.trim() !== ''
}

// Candidates are captured ONCE, at SearchPage mount, from the tabs restored
// out of localStorage — and only when the in-memory results cache is empty,
// which is the real post-reload signal (module state dies with the page; on
// an SPA remount the cache still holds whatever the session produced, and
// hydrateTabs already merged it). Tabs created or edited later must NEVER
// become candidates: SearchBar updates tab.query per keystroke, and reacting
// to those edits would fire one backend fetch per key press.
export function restoredCandidates(
  tabs: readonly CandidateTab[],
  cacheEmpty: boolean,
): Map<string, string> {
  if (!cacheEmpty) return new Map()
  return new Map(
    tabs
      .filter(t => shouldRehydrate({ query: t.query, phase: t.phase, resultCount: t.results.length }))
      .map(t => [t.id, t.query.trim()]),
  )
}

export type RehydrateDecision = 'fetch' | 'drop' | 'skip'

// Per-render decision for the ACTIVE tab against its mount-time snapshot:
// 'skip'  → not a candidate (new tab, already consumed, or never restored).
// 'drop'  → the tab moved on (user edit, search started, results arrived):
//           its candidacy is consumed WITHOUT fetching — a user typing must
//           never trigger rehydration, even if the text later matches again.
// 'fetch' → the tab still looks exactly as restored: one shot at the backend.
export function decideRehydrate(tab: CandidateTab, restoredQuery: string | undefined): RehydrateDecision {
  if (restoredQuery === undefined) return 'skip'
  const unchanged = tab.query.trim() === restoredQuery
    && shouldRehydrate({ query: tab.query, phase: tab.phase, resultCount: tab.results.length })
  return unchanged ? 'fetch' : 'drop'
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
  tabs: readonly CandidateTab[],
  activeTabId: string,
  apply: (tabId: string, query: string, results: SearchResult[]) => void,
) {
  // Mount-time snapshot (lazy ref init runs during the first render, before
  // the syncTabsToCache effect repopulates the cache). A candidate left alone
  // keeps its slot until visited — switching to a restored tab later in the
  // session still rehydrates it, as long as it was never touched.
  const candidates = useRef<Map<string, string> | null>(null)
  candidates.current ??= restoredCandidates(tabs, !hasAnyTabResults())

  useEffect(() => {
    const tab = tabs.find(t => t.id === activeTabId)
    if (!tab) return
    const decision = decideRehydrate(tab, candidates.current?.get(tab.id))
    if (decision === 'skip') return
    // Both outcomes consume the single attempt BEFORE the fetch resolves: under
    // React.StrictMode the mount effect runs twice and the second run must not
    // refire. There is deliberately no cancellation in the cleanup — a
    // StrictMode remount would discard the only response — correctness at
    // apply time is owned by canApplyRehydrated (via the `apply` callback).
    candidates.current?.delete(tab.id)
    if (decision === 'drop') return
    getHistoryResults(tab.query.trim())
      .then(results => {
        if (results.length > 0) apply(tab.id, tab.query, results)
      })
      .catch(() => { /* offline or no cache: leave the tab as-is */ })
  }, [tabs, activeTabId, apply])
}
