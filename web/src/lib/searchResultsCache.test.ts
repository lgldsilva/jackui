import { describe, it, expect, beforeEach } from 'vitest'
import {
  getTabResults, setTabResults, deleteTabResults, clearTabResults,
  syncTabsToCache, mergeCachedResults,
} from './searchResultsCache'
import type { SyncableTab, SearchPhase } from './searchResultsCache'
import type { SearchResult } from '../api/client'

function mk(over: Partial<SearchResult>): SearchResult {
  return {
    title: 'X', tracker: 't', categoryId: 0, category: '', size: 1024,
    seeders: 1, leechers: 0, age: '', magnetUri: '', link: '', infoHash: '',
    publishDate: '', ...over,
  }
}

function tab(id: string, query: string, results: SearchResult[], phase: SearchPhase = 'done'): SyncableTab {
  return { id, query, results, phase, summary: null }
}

beforeEach(() => clearTabResults())

describe('searchResultsCache set/get/delete/clear', () => {
  it('round-trips an entry and stamps savedAt', () => {
    const results = [mk({ title: 'Dune' })]
    setTabResults('1', { query: 'dune', results, phase: 'done', summary: { total: 1, live: 1, cached: 0 } })
    const e = getTabResults('1')
    expect(e?.query).toBe('dune')
    expect(e?.results).toBe(results)
    expect(e?.phase).toBe('done')
    expect(e?.summary).toEqual({ total: 1, live: 1, cached: 0 })
    expect(e?.savedAt).toBeGreaterThan(0)
  })

  it('delete removes only the targeted tab; clear removes everything', () => {
    setTabResults('1', { query: 'a', results: [mk({})], phase: 'done', summary: null })
    setTabResults('2', { query: 'b', results: [mk({})], phase: 'done', summary: null })
    deleteTabResults('1')
    expect(getTabResults('1')).toBeUndefined()
    expect(getTabResults('2')).toBeDefined()
    clearTabResults()
    expect(getTabResults('2')).toBeUndefined()
  })
})

describe('eviction — only the 8 most recently updated tabs are kept', () => {
  it('drops the oldest entry when a 9th tab is added', () => {
    for (let i = 1; i <= 9; i++) {
      setTabResults(String(i), { query: `q${i}`, results: [mk({})], phase: 'done', summary: null })
    }
    expect(getTabResults('1')).toBeUndefined()
    for (let i = 2; i <= 9; i++) expect(getTabResults(String(i))).toBeDefined()
  })

  it('updating an existing tab refreshes its recency (it survives the next eviction)', () => {
    for (let i = 1; i <= 8; i++) {
      setTabResults(String(i), { query: `q${i}`, results: [mk({})], phase: 'done', summary: null })
    }
    // Touch tab 1 → tab 2 becomes the oldest → the 9th insert evicts 2, not 1.
    setTabResults('1', { query: 'q1', results: [mk({ title: 'new' })], phase: 'done', summary: null })
    setTabResults('9', { query: 'q9', results: [mk({})], phase: 'done', summary: null })
    expect(getTabResults('1')).toBeDefined()
    expect(getTabResults('2')).toBeUndefined()
  })
})

describe('syncTabsToCache', () => {
  it('upserts tabs with results and drops entries of closed tabs', () => {
    const a = tab('1', 'dune', [mk({ title: 'Dune' })])
    syncTabsToCache([a])
    expect(getTabResults('1')?.results).toBe(a.results)

    // Tab 1 closed; tab 2 open with results.
    const b = tab('2', 'tron', [mk({ title: 'Tron' })])
    syncTabsToCache([b])
    expect(getTabResults('1')).toBeUndefined()
    expect(getTabResults('2')?.query).toBe('tron')
  })

  it('drops the entry when a tab clears its results (new search starting)', () => {
    syncTabsToCache([tab('1', 'dune', [mk({})])])
    expect(getTabResults('1')).toBeDefined()
    syncTabsToCache([tab('1', 'matrix', [], 'cache')])
    expect(getTabResults('1')).toBeUndefined()
  })

  it('skips unchanged tabs so recency only moves when results actually change', () => {
    const a = tab('1', 'dune', [mk({})])
    syncTabsToCache([a])
    const first = getTabResults('1')
    syncTabsToCache([a]) // same references → no rewrite
    expect(getTabResults('1')).toBe(first)
  })

  it('rewrites when the query changes even if the results reference is the same', () => {
    const results = [mk({})]
    syncTabsToCache([tab('1', 'dune', results)])
    syncTabsToCache([tab('1', 'dune part two', results)])
    expect(getTabResults('1')?.query).toBe('dune part two')
  })
})

describe('mergeCachedResults (hydrate merge, pure)', () => {
  const cached = (query: string, phase: SearchPhase = 'done') => ({
    query, results: [mk({ title: 'R' })], phase, summary: { total: 1, live: 0, cached: 1 }, savedAt: 1,
  })

  it('fills an empty tab whose query matches, restoring summary too', () => {
    const t = tab('1', 'dune', [], 'idle')
    const out = mergeCachedResults(t, cached('dune'))
    expect(out.results).toHaveLength(1)
    expect(out.phase).toBe('done')
    expect(out.summary).toEqual({ total: 1, live: 0, cached: 1 })
  })

  it('is a no-op without a cache entry', () => {
    const t = tab('1', 'dune', [], 'idle')
    expect(mergeCachedResults(t, undefined)).toBe(t)
  })

  it('never clobbers results the tab already has', () => {
    const mine = [mk({ title: 'mine' })]
    const t = tab('1', 'dune', mine)
    expect(mergeCachedResults(t, cached('dune')).results).toBe(mine)
  })

  it('refuses an entry whose query no longer matches the tab', () => {
    const t = tab('1', 'matrix', [], 'idle')
    expect(mergeCachedResults(t, cached('dune'))).toBe(t)
  })

  it("normalizes an in-flight phase to 'done' (the search died with the unmount)", () => {
    const out = mergeCachedResults(tab('1', 'dune', [], 'idle'), cached('dune', 'live'))
    expect(out.phase).toBe('done')
  })

  it("preserves 'error' so the tab still shows the search failed", () => {
    const out = mergeCachedResults(tab('1', 'dune', [], 'idle'), cached('dune', 'error'))
    expect(out.phase).toBe('error')
  })
})
