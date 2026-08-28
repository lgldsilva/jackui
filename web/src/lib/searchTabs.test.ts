import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  newTab, nextTabId, hydrateTabs, persistTabs, appendResult, setErrorMsg,
  TABS_KEY, ACTIVE_KEY,
  type TabState, type PersistedTab,
} from './searchTabs'
import { save, load } from './storage'
import type { SearchResult } from '../api/client'
import { setTabResults, clearTabResults } from './searchResultsCache'

function mkResult(over: Partial<SearchResult> = {}): SearchResult {
  return {
    title: 'Movie', tracker: 'tracker1', categoryId: 0, category: '', size: 1024,
    seeders: 10, leechers: 2, age: '1d', magnetUri: '', link: '', infoHash: 'hash1',
    publishDate: '', ...over,
  }
}

describe('searchTabs', () => {
  beforeEach(() => {
    localStorage.clear()
    clearTabResults()
    vi.restoreAllMocks()
  })

  describe('newTab & nextTabId', () => {
    it('generates unique IDs with nextTabId', () => {
      const ids = new Set<string>()
      for (let i = 0; i < 50; i++) {
        const id = nextTabId()
        expect(typeof id).toBe('string')
        expect(id.length).toBeGreaterThan(0)
        expect(ids.has(id)).toBe(false)
        ids.add(id)
      }
      expect(ids.size).toBe(50)
    })

    it('newTab generates a unique ID by default and sets initial state', () => {
      const t1 = newTab()
      const t2 = newTab()
      expect(t1.id).not.toBe(t2.id)
      expect(t1.phase).toBe('idle')
      expect(t1.query).toBe('')
      expect(t1.results).toEqual([])
      expect(t1.error).toBe('')
      expect(t1.summary).toBeNull()
      expect(t1.minSeeders).toBe(1)
    })

    it('newTab accepts a custom ID', () => {
      const t = newTab('custom-id-123')
      expect(t.id).toBe('custom-id-123')
    })
  })

  describe('hydrateTabs', () => {
    it('creates 1 tab when localStorage is empty', () => {
      const { tabs, activeId } = hydrateTabs()
      expect(tabs).toHaveLength(1)
      expect(tabs[0].id).toBe(activeId)
      expect(tabs[0].phase).toBe('idle')
    })

    it('restores persisted tabs and selects active tab', () => {
      const persisted: PersistedTab[] = [
        {
          id: 'tab-1', query: 'matrix', selectedIndexers: [], selectedCategory: 'all',
          titleFilter: '', trackerFilter: 'all', minSeeders: 2, minLeechers: 0,
          maxSizeGb: '', resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
          resolution: '', hdrOnly: false, codecGroup: '',
        },
        {
          id: 'tab-2', query: 'dune', selectedIndexers: [], selectedCategory: 'all',
          titleFilter: '', trackerFilter: 'all', minSeeders: 5, minLeechers: 0,
          maxSizeGb: '', resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
          resolution: '', hdrOnly: false, codecGroup: '',
        },
      ]
      save(TABS_KEY, persisted)
      save(ACTIVE_KEY, 'tab-2')

      const { tabs, activeId } = hydrateTabs()
      expect(tabs).toHaveLength(2)
      expect(tabs[0].id).toBe('tab-1')
      expect(tabs[0].query).toBe('matrix')
      expect(tabs[1].id).toBe('tab-2')
      expect(tabs[1].query).toBe('dune')
      expect(activeId).toBe('tab-2')
    })

    it('heals legacy corrupted storage where multiple tabs share the exact same ID', () => {
      // Simulate bug scenario where multiple tabs got saved with id '2'
      const corrupted: PersistedTab[] = [
        {
          id: '1', query: 'query1', selectedIndexers: [], selectedCategory: 'all',
          titleFilter: '', trackerFilter: 'all', minSeeders: 1, minLeechers: 0,
          maxSizeGb: '', resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
          resolution: '', hdrOnly: false, codecGroup: '',
        },
        {
          id: '2', query: 'query2', selectedIndexers: [], selectedCategory: 'all',
          titleFilter: '', trackerFilter: 'all', minSeeders: 1, minLeechers: 0,
          maxSizeGb: '', resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
          resolution: '', hdrOnly: false, codecGroup: '',
        },
        {
          id: '2', query: 'query3', selectedIndexers: [], selectedCategory: 'all',
          titleFilter: '', trackerFilter: 'all', minSeeders: 1, minLeechers: 0,
          maxSizeGb: '', resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
          resolution: '', hdrOnly: false, codecGroup: '',
        },
        {
          id: '2', query: 'query4', selectedIndexers: [], selectedCategory: 'all',
          titleFilter: '', trackerFilter: 'all', minSeeders: 1, minLeechers: 0,
          maxSizeGb: '', resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
          resolution: '', hdrOnly: false, codecGroup: '',
        },
      ]
      save(TABS_KEY, corrupted)
      save(ACTIVE_KEY, '2')

      const { tabs, activeId } = hydrateTabs()
      expect(tabs).toHaveLength(4)
      const ids = tabs.map(t => t.id)
      const uniqueIds = new Set(ids)
      expect(uniqueIds.size).toBe(4) // All IDs must now be distinct!
      expect(ids[0]).toBe('1')
      expect(ids[1]).toBe('2')
      expect(ids[2]).not.toBe('2')
      expect(ids[3]).not.toBe('2')
      expect(tabs.some(t => t.id === activeId)).toBe(true)
    })

    it('merges in-memory cached results during hydration', () => {
      const results = [mkResult({ title: 'Cached Matrix' })]
      setTabResults('tab-1', { query: 'matrix', results, phase: 'done', summary: { total: 1, live: 1, cached: 0 } })

      const persisted: PersistedTab[] = [
        {
          id: 'tab-1', query: 'matrix', selectedIndexers: [], selectedCategory: 'all',
          titleFilter: '', trackerFilter: 'all', minSeeders: 1, minLeechers: 0,
          maxSizeGb: '', resultSort: 'seeders', resultSortAsc: false, onlyPlayable: false,
          resolution: '', hdrOnly: false, codecGroup: '',
        },
      ]
      save(TABS_KEY, persisted)

      const { tabs } = hydrateTabs()
      expect(tabs[0].results).toHaveLength(1)
      expect(tabs[0].results[0].title).toBe('Cached Matrix')
      expect(tabs[0].phase).toBe('done')
    })
  })

  describe('persistTabs', () => {
    it('persists tabs and activeId to localStorage', () => {
      const tabs: TabState[] = [
        newTab('t1'),
        newTab('t2'),
      ]
      tabs[0].query = 'search1'
      tabs[1].query = 'search2'

      persistTabs(tabs, 't2')

      const savedTabs = load<PersistedTab[]>(TABS_KEY, [])
      const savedActive = load<string>(ACTIVE_KEY, '')

      expect(savedTabs).toHaveLength(2)
      expect(savedTabs[0].id).toBe('t1')
      expect(savedTabs[0].query).toBe('search1')
      expect(savedTabs[1].id).toBe('t2')
      expect(savedTabs[1].query).toBe('search2')
      expect(savedActive).toBe('t2')
    })
  })

  describe('appendResult & setErrorMsg', () => {
    it('appends result only to the targeted tab', () => {
      const t1 = newTab('t1')
      const t2 = newTab('t2')
      const t3 = newTab('t3')
      const tabs = [t1, t2, t3]

      const res = mkResult({ infoHash: 'abc', title: 'Test Torrent' })
      const updated = appendResult(tabs, 't2', res)

      expect(updated[0].results).toHaveLength(0)
      expect(updated[1].results).toHaveLength(1)
      expect(updated[1].results[0].title).toBe('Test Torrent')
      expect(updated[2].results).toHaveLength(0)

      // References for untouched tabs are preserved
      expect(updated[0]).toBe(t1)
      expect(updated[2]).toBe(t3)
    })

    it('sets error only on the targeted tab', () => {
      const t1 = newTab('t1')
      const t2 = newTab('t2')
      const tabs = [t1, t2]

      const updated = setErrorMsg(tabs, 't2', 'Network failed')
      expect(updated[0].error).toBe('')
      expect(updated[1].error).toBe('Network failed')
    })
  })

  describe('Multi-tab concurrent operations & isolation', () => {
    it('supports 4+ independent tabs with distinct updates and independent close', () => {
      // Simulate user opening 4 tabs
      let tabs: TabState[] = [
        newTab('tab-1'),
        newTab('tab-2'),
        newTab('tab-3'),
        newTab('tab-4'),
      ]
      let activeId = 'tab-4'

      // Search in tab 2
      tabs = tabs.map(t => t.id === 'tab-2' ? { ...t, query: 'term-a', phase: 'live' as const } : t)
      tabs = appendResult(tabs, 'tab-2', mkResult({ infoHash: 'h1', title: 'Result A' }))

      // Search in tab 3
      tabs = tabs.map(t => t.id === 'tab-3' ? { ...t, query: 'term-b', phase: 'live' as const } : t)
      tabs = appendResult(tabs, 'tab-3', mkResult({ infoHash: 'h2', title: 'Result B' }))

      // Search in tab 4
      tabs = tabs.map(t => t.id === 'tab-4' ? { ...t, query: 'term-c', phase: 'live' as const } : t)
      tabs = appendResult(tabs, 'tab-4', mkResult({ infoHash: 'h3', title: 'Result C' }))

      // Verify all tabs maintain independent state
      expect(tabs[0].results).toHaveLength(0)
      expect(tabs[1].results).toHaveLength(1)
      expect(tabs[1].results[0].title).toBe('Result A')
      expect(tabs[2].results).toHaveLength(1)
      expect(tabs[2].results[0].title).toBe('Result B')
      expect(tabs[3].results).toHaveLength(1)
      expect(tabs[3].results[0].title).toBe('Result C')

      // Close tab 3 (which is active)
      const closeTarget = 'tab-3'
      activeId = 'tab-3'
      const remaining = tabs.filter(t => t.id !== closeTarget)
      if (activeId === closeTarget) {
        const idx = tabs.findIndex(t => t.id === closeTarget)
        activeId = remaining[Math.max(0, idx - 1)].id
      }
      tabs = remaining

      // Verify ONLY tab 3 was closed; tabs 1, 2, 4 remain intact and activeId switched to tab-2
      expect(activeId).toBe('tab-2')
      expect(tabs).toHaveLength(3)
      expect(tabs.map(t => t.id)).toEqual(['tab-1', 'tab-2', 'tab-4'])
      expect(tabs[1].results[0].title).toBe('Result A')
      expect(tabs[2].results[0].title).toBe('Result C')
    })
  })
})
