import { describe, it, expect } from 'vitest'
import { shouldRehydrate, canApplyRehydrated } from './useRehydratedResults'
import type { SearchPhase } from './searchResultsCache'

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
