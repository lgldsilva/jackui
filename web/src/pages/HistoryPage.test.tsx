import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { deferred, type Deferred } from '../lib/deferred.testutil'

const ptr = vi.hoisted(() => ({ onRefresh: undefined as undefined | (() => Promise<void>) }))
const m = vi.hoisted(() => ({
  notifyError: vi.fn(),
  confirm: vi.fn(),
  history: vi.fn(),
  results: vi.fn(),
  clear: vi.fn(),
  del: vi.fn(),
  cache: vi.fn(),
  search: vi.fn(),
  refresh: vi.fn(),
}))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    getHistory: (...a: unknown[]) => m.history(...a),
    getHistoryResults: (...a: unknown[]) => m.results(...a),
    clearHistory: (...a: unknown[]) => m.clear(...a),
    deleteHistoryEntry: (...a: unknown[]) => m.del(...a),
    searchCache: (...a: unknown[]) => m.cache(...a),
    searchTorrents: (...a: unknown[]) => m.search(...a),
    historyRefresh: (...a: unknown[]) => m.refresh(...a),
  }
})
vi.mock('../lib/usePullToRefresh', () => ({
  usePullToRefresh: (opts: { onRefresh: () => Promise<void> }) => {
    ptr.onRefresh = opts.onRefresh
    return { pull: 0, progress: 0, refreshing: false }
  },
}))
vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/DownloadModal', () => ({ default: () => null }))
vi.mock('../components/TorrentContentsModal', () => ({ default: () => null }))
vi.mock('../components/PlaylistPickerModal', () => ({ default: () => null }))
vi.mock('../components/ResultCard', () => ({
  default: ({ result, onRefresh }: { result: { title: string }; onRefresh: (r: unknown) => void }) => (
    <div>
      <span data-testid="result">{result.title}</span>
      <button onClick={() => onRefresh(result)}>reseed {result.title}</button>
    </div>
  ),
}))
vi.mock('../components/ConfirmDialog', () => ({ useConfirm: () => m.confirm }))
vi.mock('../components/PlayerProvider', () => ({ usePlayer: () => ({ playSingle: vi.fn() }) }))
vi.mock('../components/Toast', () => ({ notifyError: (e: unknown) => m.notifyError(e) }))

import HistoryPage from './HistoryPage'

const entry = (query: string) => ({ query, resultCount: 1, lastSaved: '2026-01-01T00:00:00Z' })
const result = (title: string, id?: number) => ({
  id, title, tracker: 'tr', categoryId: 0, category: '', size: 1, seeders: 5, leechers: 0,
  publishDate: '', link: title, magnetUri: '', infoHash: title, source: '', cached: true,
})
const boom = { response: { status: 500, data: { error: 'db down' } } }
const titles = () => screen.queryAllByTestId('result').map(el => el.textContent)

const renderPage = () => render(<MemoryRouter><HistoryPage /></MemoryRouter>)

describe('HistoryPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    // Drop any unconsumed mockResolvedValueOnce left by an earlier test.
    m.results.mockReset()
    m.history.mockReset()
    m.history.mockResolvedValue([entry('alpha'), entry('beta')])
    m.confirm.mockResolvedValue(true)
  })
  afterEach(() => { vi.useRealTimers(); cleanup() })

  describe('out-of-order responses (#223)', () => {
    it('keeps the results of the last selected query when an earlier one answers late', async () => {
      const pending = new Map<string, Deferred<unknown[]>>()
      m.results.mockImplementation((q: string) => {
        const d = deferred<unknown[]>()
        pending.set(q, d)
        return d.promise
      })
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      fireEvent.click(screen.getByText('beta'))

      await act(async () => { pending.get('beta')?.resolve([result('Beta result')]) })
      await act(async () => { pending.get('alpha')?.resolve([result('Alpha result')]) })

      expect(titles()).toEqual(['Beta result'])
    })

    it('pull-to-refresh reloads the query selected while the history list was refreshing', async () => {
      const pending = new Map<string, Deferred<unknown[]>>()
      m.results.mockImplementation((q: string) => {
        const d = deferred<unknown[]>()
        pending.set(q, d)
        return d.promise
      })
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      await act(async () => { pending.get('alpha')?.resolve([result('Alpha result')]) })

      const refreshing = deferred<unknown[]>()
      m.history.mockReturnValue(refreshing.promise)
      let done: Promise<void> = Promise.resolve()
      act(() => { done = ptr.onRefresh?.() ?? Promise.resolve() })
      fireEvent.click(screen.getByText('beta'))
      await act(async () => { refreshing.resolve([entry('alpha'), entry('beta')]) })
      await act(async () => { pending.get('beta')?.resolve([result('Beta result')]) })
      await act(async () => { pending.get('alpha')?.resolve([result('Alpha result')]) })
      await act(async () => { await done })

      expect(titles()).toEqual(['Beta result'])
    })

    it('ignores a pull-to-refresh answer once another query was opened', async () => {
      const pending = new Map<string, Deferred<unknown[]>>()
      m.results.mockImplementation((q: string) => {
        const d = deferred<unknown[]>()
        pending.set(q, d)
        return d.promise
      })
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      await act(async () => { pending.get('alpha')?.resolve([result('Alpha result')]) })

      let done: Promise<void> = Promise.resolve()
      await act(async () => { done = ptr.onRefresh?.() ?? Promise.resolve(); await Promise.resolve() })
      const refreshed = pending.get('alpha')
      fireEvent.click(screen.getByText('beta'))
      await act(async () => { pending.get('beta')?.resolve([result('Beta result')]) })
      await act(async () => { refreshed?.resolve([result('Alpha again')]); await done })

      expect(titles()).toEqual(['Beta result'])
    })

    it('ignores a late failure of an earlier selection', async () => {
      const pending = new Map<string, Deferred<unknown[]>>()
      m.results.mockImplementation((q: string) => {
        const d = deferred<unknown[]>()
        pending.set(q, d)
        return d.promise
      })
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      fireEvent.click(screen.getByText('beta'))
      await act(async () => { pending.get('beta')?.resolve([result('Beta result')]) })
      await act(async () => { pending.get('alpha')?.reject(boom) })

      expect(titles()).toEqual(['Beta result'])
    })

    it('live refresh results win over the cached load still in flight for the same query', async () => {
      const cached = deferred<unknown[]>()
      m.results.mockResolvedValueOnce([result('Cached')]).mockReturnValueOnce(cached.promise)
      m.search.mockResolvedValue([result('Live 1'), result('Live 2')])
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      fireEvent.click(await screen.findByText('Refresh search'))
      // Reopen the same query while the live search is running.
      fireEvent.click(screen.getByText('Back to searches'))
      fireEvent.click(screen.getByText('alpha'))
      await waitFor(() => expect(titles()).toEqual(['Live 1', 'Live 2']))
      await act(async () => { cached.resolve([result('Cached')]) })

      expect(titles()).toEqual(['Live 1', 'Live 2'])
      expect(m.search).toHaveBeenCalledWith('alpha', ['all'], 'all')
    })

    const openGlobal = async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
      const pending = new Map<string, Deferred<unknown[]>>()
      m.cache.mockImplementation((q: string) => {
        const d = deferred<unknown[]>()
        pending.set(q, d)
        return d.promise
      })
      renderPage()
      await act(async () => { await Promise.resolve() })
      fireEvent.click(screen.getByText('Global search'))
      const input = screen.getByPlaceholderText(/Full-text search/)
      const type = async (value: string) => {
        fireEvent.change(input, { target: { value } })
        await act(async () => { await vi.advanceTimersByTimeAsync(300) })
      }
      return { pending, type }
    }

    it('ignores a global-search response for an older keystroke', async () => {
      const { pending, type } = await openGlobal()
      await type('a')
      await type('ab')

      await act(async () => { pending.get('ab')?.resolve([result('AB hit')]) })
      await act(async () => { pending.get('a')?.resolve([result('A hit')]) })

      expect(titles()).toEqual(['AB hit'])
    })

    it('ignores a late global-search failure for an older keystroke', async () => {
      const { pending, type } = await openGlobal()
      await type('a')
      await type('ab')

      await act(async () => { pending.get('ab')?.resolve([result('AB hit')]) })
      await act(async () => { pending.get('a')?.reject(boom) })

      expect(titles()).toEqual(['AB hit'])
    })

    it('empties the global results when the current search fails', async () => {
      const { pending, type } = await openGlobal()
      await type('a')
      await act(async () => { pending.get('a')?.resolve([result('A hit')]) })
      expect(titles()).toEqual(['A hit'])
      await type('ab')
      await act(async () => { pending.get('ab')?.reject(boom) })

      expect(titles()).toEqual([])
    })

    it('ignores a global-search response that arrives after the query was cleared', async () => {
      const { pending, type } = await openGlobal()
      await type('a')
      await type('')

      await act(async () => { pending.get('a')?.resolve([result('A hit')]) })

      expect(titles()).toEqual([])
    })
  })

  describe('load and mutation failures (#223)', () => {
    it('shows a loading state, not the empty state, before the first response', async () => {
      const d = deferred<unknown[]>()
      m.history.mockReturnValue(d.promise)
      renderPage()
      expect(screen.queryByText('No saved searches')).not.toBeInTheDocument()
      expect(screen.getByRole('status')).toBeInTheDocument()
      await act(async () => { d.resolve([]) })
      expect(screen.getByText('No saved searches')).toBeInTheDocument()
    })

    it('shows an error with retry, not the empty state, when the load fails', async () => {
      m.history.mockRejectedValueOnce(boom)
      renderPage()
      expect(await screen.findByRole('alert')).toHaveTextContent('db down')
      expect(screen.queryByText('No saved searches')).not.toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
      expect(await screen.findByText('alpha')).toBeInTheDocument()
    })

    it('toasts a failed delete and keeps the entry', async () => {
      m.del.mockRejectedValue(boom)
      renderPage()
      await screen.findByText('alpha')
      fireEvent.click(screen.getAllByRole('button', { name: 'Remove from cache' })[0])
      await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
      expect(screen.getByText('alpha')).toBeInTheDocument()
    })

    it('toasts a failed clear and keeps the entries', async () => {
      m.clear.mockRejectedValue(boom)
      renderPage()
      await screen.findByText('alpha')
      fireEvent.click(screen.getByRole('button', { name: 'Clear cache' }))
      await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
      expect(screen.getByText('alpha')).toBeInTheDocument()
      expect(screen.getByText('beta')).toBeInTheDocument()
    })

    it('shows the empty state, not a stale error, after a delete empties the list', async () => {
      m.history.mockResolvedValueOnce([entry('alpha')])
      m.del.mockResolvedValue(undefined)
      renderPage()
      await screen.findByText('alpha')
      m.history.mockRejectedValueOnce(boom)
      await act(async () => { await ptr.onRefresh?.() })

      fireEvent.click(screen.getByRole('button', { name: 'Remove from cache' }))

      expect(await screen.findByText('No saved searches')).toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })

    it('treats an empty (null) history response as no saved searches', async () => {
      m.history.mockResolvedValue(null)
      renderPage()
      expect(await screen.findByText('No saved searches')).toBeInTheDocument()
    })

    it('keeps the list and toasts when a refresh fails with entries on screen', async () => {
      renderPage()
      await screen.findByText('alpha')
      m.history.mockRejectedValueOnce(boom)
      await act(async () => { await ptr.onRefresh?.() })

      expect(m.notifyError).toHaveBeenCalledWith(boom)
      expect(screen.getByText('alpha')).toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })

    it('clearing the cache empties the page and closes the open query', async () => {
      m.clear.mockResolvedValue(undefined)
      m.results.mockResolvedValue([result('Alpha result')])
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      await waitFor(() => expect(titles()).toEqual(['Alpha result']))
      m.history.mockRejectedValueOnce(boom)
      await act(async () => { await ptr.onRefresh?.() })

      fireEvent.click(screen.getByRole('button', { name: 'Clear cache' }))

      expect(await screen.findByText('No saved searches')).toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(titles()).toEqual([])
    })

    it('deleting the open query closes its results', async () => {
      m.del.mockResolvedValue(undefined)
      m.results.mockResolvedValue([result('Alpha result')])
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      await waitFor(() => expect(titles()).toEqual(['Alpha result']))

      fireEvent.click(screen.getAllByRole('button', { name: 'Remove from cache' })[0])

      await waitFor(() => expect(screen.queryByText('alpha')).not.toBeInTheDocument())
      expect(titles()).toEqual([])
      expect(screen.getByText('beta')).toBeInTheDocument()
    })

    it('shows the no-results message when the cached results fail to load or are null', async () => {
      m.results.mockRejectedValueOnce(boom).mockResolvedValueOnce(null)
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      expect(await screen.findByText('No results cached for "alpha"')).toBeInTheDocument()
      fireEvent.click(screen.getByText('beta'))
      expect(await screen.findByText('No results cached for "beta"')).toBeInTheDocument()
    })

    it('toasts a failed live refresh and keeps the cached results', async () => {
      m.results.mockResolvedValue([result('Alpha result')])
      m.search.mockRejectedValue(boom)
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      fireEvent.click(await screen.findByText('Refresh search'))

      await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
      expect(titles()).toEqual(['Alpha result'])
    })

    it('toasts a failed per-result refresh and keeps the result', async () => {
      m.results.mockResolvedValue([result('Alpha result', 42)])
      m.refresh.mockRejectedValue(boom)
      renderPage()
      fireEvent.click(await screen.findByText('alpha'))
      fireEvent.click(await screen.findByText('reseed Alpha result'))

      await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
      expect(m.refresh).toHaveBeenCalledWith(42)
      expect(titles()).toEqual(['Alpha result'])
    })
  })
})
