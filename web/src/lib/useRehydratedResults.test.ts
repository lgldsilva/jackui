import { describe, it, expect } from 'vitest'
import { shouldRehydrate, canApplyRehydrated, restoredCandidates, decideRehydrate } from './useRehydratedResults'
import type { SearchPhase } from './searchResultsCache'

type Tab = { id: string; query: string; phase: SearchPhase; results: unknown[] }
const tab = (over: Partial<Tab> = {}): Tab => ({
  id: '1', query: 'dune', phase: 'idle', results: [], ...over,
})

describe('shouldRehydrate — post-reload decision', () => {
  it('rehydrates an idle tab with a non-empty query and zero results', () => {
    expect(shouldRehydrate({ query: 'dune', phase: 'idle', resultCount: 0 })).toBe(true)
  })

  it('does NOT touch a search in progress (cache/live phases)', () => {
    expect(shouldRehydrate({ query: 'dune', phase: 'cache', resultCount: 0 })).toBe(false)
    expect(shouldRehydrate({ query: 'dune', phase: 'live', resultCount: 0 })).toBe(false)
  })

  it('does NOT refetch a tab that already has results', () => {
    expect(shouldRehydrate({ query: 'dune', phase: 'idle', resultCount: 12 })).toBe(false)
  })

  it('does NOT fetch for an empty/whitespace query', () => {
    expect(shouldRehydrate({ query: '', phase: 'idle', resultCount: 0 })).toBe(false)
    expect(shouldRehydrate({ query: '   ', phase: 'idle', resultCount: 0 })).toBe(false)
  })

  it("leaves finished tabs alone ('done' with 0 results = genuine empty search)", () => {
    expect(shouldRehydrate({ query: 'dune', phase: 'done', resultCount: 0 })).toBe(false)
    expect(shouldRehydrate({ query: 'dune', phase: 'error', resultCount: 0 })).toBe(false)
  })
})

describe('canApplyRehydrated — guard when the async cache response lands', () => {
  const t = (over: Partial<{ id: string; query: string; phase: SearchPhase; results: unknown[] }> = {}) => ({
    id: '1', query: 'dune', phase: 'idle' as SearchPhase, results: [] as unknown[], ...over,
  })

  it('applies to the same still-idle, still-empty tab with the same query', () => {
    expect(canApplyRehydrated(t(), '1', 'dune')).toBe(true)
    // Trim-insensitive: the tab keeps what the user typed.
    expect(canApplyRehydrated(t({ query: ' dune ' }), '1', 'dune')).toBe(true)
  })

  it('NEVER overwrites a search that started in the meantime', () => {
    expect(canApplyRehydrated(t({ phase: 'cache' }), '1', 'dune')).toBe(false)
    expect(canApplyRehydrated(t({ phase: 'live' }), '1', 'dune')).toBe(false)
  })

  it('refuses when the tab got results by other means meanwhile', () => {
    expect(canApplyRehydrated(t({ results: [{}] }), '1', 'dune')).toBe(false)
  })

  it('refuses when the user changed the query meanwhile', () => {
    expect(canApplyRehydrated(t({ query: 'matrix' }), '1', 'dune')).toBe(false)
  })

  it('only targets the tab that requested the rehydration', () => {
    expect(canApplyRehydrated(t({ id: '2' }), '1', 'dune')).toBe(false)
  })
})

describe('restoredCandidates — mount-time snapshot of rehydratable tabs', () => {
  it('captures restored tabs with a query and no results (post-reload)', () => {
    const got = restoredCandidates([tab(), tab({ id: '2', query: ' matrix ' })], true)
    expect(got.get('1')).toBe('dune')
    expect(got.get('2')).toBe('matrix') // trimmed
  })

  it('captures NOTHING when the in-memory cache is not empty (SPA remount, not a reload)', () => {
    expect(restoredCandidates([tab()], false).size).toBe(0)
  })

  it('skips tabs that do not need rehydration (empty query, results, busy/finished)', () => {
    const got = restoredCandidates([
      tab({ id: 'a', query: '' }),
      tab({ id: 'b', results: [{}] }),
      tab({ id: 'c', phase: 'done' }),
      tab({ id: 'd', phase: 'live' }),
    ], true)
    expect(got.size).toBe(0)
  })
})

describe('decideRehydrate — keystrokes must never trigger a fetch', () => {
  it("fetches only for the tab exactly as restored ('fetch')", () => {
    expect(decideRehydrate(tab(), 'dune')).toBe('fetch')
    expect(decideRehydrate(tab({ query: ' dune ' }), 'dune')).toBe('fetch')
  })

  it("skips tabs that were never candidates — a NEW tab being typed into fires nothing", () => {
    // SearchBar updates tab.query per keystroke: 'd' → 'du' → 'dun' → 'dune'.
    // None of these prefixes is a restored query, so no request ever leaves.
    for (const prefix of ['d', 'du', 'dun', 'dune']) {
      expect(decideRehydrate(tab({ id: 'new', query: prefix }), undefined)).toBe('skip')
    }
  })

  it("drops the candidacy on the first user edit ('drop'), without fetching", () => {
    expect(decideRehydrate(tab({ query: 'dun' }), 'dune')).toBe('drop')
    expect(decideRehydrate(tab({ query: 'dune 2' }), 'dune')).toBe('drop')
  })

  it("drops when the tab moved on by other means (search started / results / finished)", () => {
    expect(decideRehydrate(tab({ phase: 'cache' }), 'dune')).toBe('drop')
    expect(decideRehydrate(tab({ phase: 'live' }), 'dune')).toBe('drop')
    expect(decideRehydrate(tab({ phase: 'done' }), 'dune')).toBe('drop')
    expect(decideRehydrate(tab({ results: [{}] }), 'dune')).toBe('drop')
  })
})
