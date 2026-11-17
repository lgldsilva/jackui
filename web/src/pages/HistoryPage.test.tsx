import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { deferred, type Deferred } from '../lib/deferred.testutil'

const m = vi.hoisted(() => ({
  notifyError: vi.fn(),
  confirm: vi.fn(),
  history: vi.fn(),
  results: vi.fn(),
  clear: vi.fn(),
  del: vi.fn(),
  cache: vi.fn(),
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
  }
})
vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/DownloadModal', () => ({ default: () => null }))
vi.mock('../components/TorrentContentsModal', () => ({ default: () => null }))
vi.mock('../components/PlaylistPickerModal', () => ({ default: () => null }))
vi.mock('../components/ResultCard', () => ({
  default: ({ result }: { result: { title: string } }) => <div data-testid="result">{result.title}</div>,
}))
vi.mock('../components/ConfirmDialog', () => ({ useConfirm: () => m.confirm }))
vi.mock('../components/PlayerProvider', () => ({ usePlayer: () => ({ playSingle: vi.fn() }) }))
vi.mock('../components/Toast', () => ({ notifyError: (e: unknown) => m.notifyError(e) }))

import HistoryPage from './HistoryPage'

const entry = (query: string) => ({ query, resultCount: 1, lastSaved: '2026-01-01T00:00:00Z' })
const result = (title: string) => ({
  title, tracker: 'tr', categoryId: 0, category: '', size: 1, seeders: 5, leechers: 0,
  publishDate: '', link: title, magnetUri: '', infoHash: title, source: '', cached: true,
})
const boom = { response: { status: 500, data: { error: 'db down' } } }
const titles = () => screen.queryAllByTestId('result').map(el => el.textContent)

const renderPage = () => render(<MemoryRouter><HistoryPage /></MemoryRouter>)

describe('HistoryPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
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
  })
})
