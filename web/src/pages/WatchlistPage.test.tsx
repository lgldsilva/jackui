import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'

const m = vi.hoisted(() => ({
  notifyError: vi.fn(),
  list: vi.fn(),
  del: vi.fn(),
  hits: vi.fn(),
}))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    watchlistsList: (...a: unknown[]) => m.list(...a),
    watchlistsDelete: (...a: unknown[]) => m.del(...a),
    watchlistsHits: (...a: unknown[]) => m.hits(...a),
  }
})
vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/ConfirmDialog', () => ({ useConfirm: () => vi.fn(async () => true) }))
vi.mock('../components/PlayerProvider', () => ({ usePlayer: () => ({ playSingle: vi.fn() }) }))
vi.mock('../components/Toast', () => ({ notifyError: (e: unknown) => m.notifyError(e) }))

import WatchlistPage from './WatchlistPage'

const watch = {
  id: 3, userId: 1, query: 'Rick and Morty', category: '', minSeeders: 1, ntfyTopic: '',
  schedKind: 'interval', schedMinutes: 15, schedWeekday: 0, schedHour: 0, schedMinute: 0,
  nextCheckAt: '', lastChecked: '', createdAt: '', hitCount: 2,
  autoDownload: false, minResolution: '', maxSizeBytes: 0, codec: '',
}
const boom = { response: { status: 500, data: { error: 'db down' } } }

const renderPage = () => render(<MemoryRouter><WatchlistPage /></MemoryRouter>)

describe('WatchlistPage failures surface as toasts (#200)', () => {
  beforeEach(() => { vi.clearAllMocks(); m.list.mockResolvedValue([watch]) })
  afterEach(cleanup)

  it('toasts a failed initial load', async () => {
    m.list.mockRejectedValue(boom)
    renderPage()
    await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
  })

  it('toasts a failed delete and keeps the watch', async () => {
    m.del.mockRejectedValue(boom)
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: /delete watchlist/i }))
    await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
    expect(screen.getByText('Rick and Morty')).toBeInTheDocument()
  })

  it('toasts a failed hits fetch', async () => {
    m.hits.mockRejectedValue(boom)
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: /2/ }))
    await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
  })
})
