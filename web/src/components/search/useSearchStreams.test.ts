import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, cleanup } from '@testing-library/react'
import { useSearchStreams } from './useSearchStreams'
import { newTab, type TabState } from '../../lib/searchTabs'
import type { SearchStreamCallbacks, SearchStreamHandle } from '../../lib/searchStream'

let lastStreamCallbacks: SearchStreamCallbacks | null = null
let lastStreamUrl: string = ''
const mockHandle: SearchStreamHandle = {
  close: vi.fn(),
}

vi.mock('../../lib/searchStream', async () => {
  const actual = await vi.importActual<typeof import('../../lib/searchStream')>('../../lib/searchStream')
  return {
    ...actual,
    openSearchStream: vi.fn((url: string, cb: SearchStreamCallbacks) => {
      lastStreamUrl = url
      lastStreamCallbacks = cb
      return mockHandle
    }),
  }
})

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    withToken: (url: string) => url,
  }
})

describe('useSearchStreams', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    lastStreamCallbacks = null
    lastStreamUrl = ''
  })

  afterEach(() => {
    cleanup()
  })

  it('handleSearch sets phase to cache and opens search stream with query params', () => {
    const tab = newTab('tab-1')
    tab.query = 'inception'
    tab.selectedIndexers = ['indexer1', 'indexer2']
    tab.selectedCategory = 'movies'

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result } = renderHook(({ tabs }) => useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab] },
    })

    act(() => {
      result.current.handleSearch('tab-1')
    })

    expect(updateTab).toHaveBeenCalledWith('tab-1', {
      results: [], error: '', summary: null, phase: 'cache',
    })
    expect(lastStreamUrl).toContain('/api/search/stream?')
    expect(lastStreamUrl).toContain('q=inception')
    expect(lastStreamUrl).toContain('indexers=indexer1%2Cindexer2')
    expect(lastStreamUrl).toContain('category=movies')
  })

  it('handleSearch supports queryOverride parameter', () => {
    const tab = newTab('tab-1')
    tab.query = ''

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result } = renderHook(({ tabs }) => useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab] },
    })

    act(() => {
      result.current.handleSearch('tab-1', 'override query')
    })

    expect(updateTab).toHaveBeenCalledWith('tab-1', {
      results: [], error: '', summary: null, phase: 'cache',
    })
    expect(lastStreamUrl).toContain('q=override+query')
  })

  it('handleSearch ignores missing tab or empty query', () => {
    const tab = newTab('tab-1')
    tab.query = '   '

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result } = renderHook(({ tabs }) => useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab] },
    })

    act(() => {
      result.current.handleSearch('tab-1')
      result.current.handleSearch('non-existent')
    })

    expect(updateTab).not.toHaveBeenCalled()
    expect(lastStreamUrl).toBe('')
  })

  it('handleSearch cancels previous stream for the same tab before starting new one', () => {
    const tab = newTab('tab-1')
    tab.query = 'first query'

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result } = renderHook(({ tabs }) => useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab] },
    })

    act(() => {
      result.current.handleSearch('tab-1')
    })
    expect(mockHandle.close).not.toHaveBeenCalled()

    act(() => {
      result.current.handleSearch('tab-1', 'second query')
    })
    expect(mockHandle.close).toHaveBeenCalledTimes(1)
  })

  it('tabsRef updates when tabs prop changes allowing searches on new tabs', () => {
    const tab1 = newTab('tab-1')
    tab1.query = 'q1'
    const tab2 = newTab('tab-2')
    tab2.query = 'q2'

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result, rerender } = renderHook(({ tabs }: { tabs: TabState[] }) =>
      useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab1] },
    })

    // Rerender with tab2 added
    rerender({ tabs: [tab1, tab2] })

    act(() => {
      result.current.handleSearch('tab-2')
    })

    expect(updateTab).toHaveBeenCalledWith('tab-2', {
      results: [], error: '', summary: null, phase: 'cache',
    })
    expect(lastStreamUrl).toContain('q=q2')
  })

  it('forwards stream events (onResult, onLive, onServerError, onDone, onGiveUp) to callbacks', () => {
    const tab = newTab('tab-1')
    tab.query = 'matrix'

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result } = renderHook(({ tabs }) => useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab] },
    })

    act(() => {
      result.current.handleSearch('tab-1')
    })
    expect(lastStreamCallbacks).not.toBeNull()

    // Test onResult
    act(() => {
      lastStreamCallbacks?.onResult({ title: 'Matrix 1999', infoHash: 'hash1' })
    })
    expect(setTabs).toHaveBeenCalledTimes(1)

    // Test onLive
    act(() => {
      lastStreamCallbacks?.onLive()
    })
    expect(updateTab).toHaveBeenCalledWith('tab-1', { phase: 'live' })

    // Test onServerError
    act(() => {
      lastStreamCallbacks?.onServerError('Indexer timeout')
    })
    expect(setTabs).toHaveBeenCalledTimes(2)

    // Test onDone
    const summary = { total: 10, live: 8, cached: 2 }
    act(() => {
      lastStreamCallbacks?.onDone(summary)
    })
    expect(updateTab).toHaveBeenCalledWith('tab-1', { summary, phase: 'done' })

    // Re-search and test onGiveUp
    act(() => {
      result.current.handleSearch('tab-1')
    })
    act(() => {
      lastStreamCallbacks?.onGiveUp()
    })
    expect(updateTab).toHaveBeenCalledWith('tab-1', expect.objectContaining({ phase: 'error' }))
  })

  it('stopSearch closes stream and sets phase to done', () => {
    const tab = newTab('tab-1')
    tab.query = 'matrix'

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result } = renderHook(({ tabs }) => useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab] },
    })

    act(() => {
      result.current.handleSearch('tab-1')
    })

    act(() => {
      result.current.stopSearch('tab-1')
    })

    expect(mockHandle.close).toHaveBeenCalled()
    expect(updateTab).toHaveBeenCalledWith('tab-1', { phase: 'done' })
  })

  it('closeStream closes stream and removes from map', () => {
    const tab = newTab('tab-1')
    tab.query = 'matrix'

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result } = renderHook(({ tabs }) => useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab] },
    })

    act(() => {
      result.current.handleSearch('tab-1')
    })

    act(() => {
      result.current.closeStream('tab-1')
    })

    expect(mockHandle.close).toHaveBeenCalledTimes(1)

    // Second close is a no-op
    act(() => {
      result.current.closeStream('tab-1')
    })
    expect(mockHandle.close).toHaveBeenCalledTimes(1)
  })

  it('closes all active streams on unmount', () => {
    const tab1 = newTab('tab-1')
    tab1.query = 'q1'
    const tab2 = newTab('tab-2')
    tab2.query = 'q2'

    const updateTab = vi.fn()
    const setTabs = vi.fn()

    const { result, unmount } = renderHook(({ tabs }) => useSearchStreams(tabs, updateTab, setTabs), {
      initialProps: { tabs: [tab1, tab2] },
    })

    act(() => {
      result.current.handleSearch('tab-1')
      result.current.handleSearch('tab-2')
    })

    unmount()
    expect(mockHandle.close).toHaveBeenCalledTimes(2)
  })
})
