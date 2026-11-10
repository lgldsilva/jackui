import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'

const notifyError = vi.fn()
const playlistsDelete = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    playlistsList: vi.fn().mockResolvedValue([{ id: 7, name: 'Road trip', itemCount: 2, createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' }]),
    playlistsDelete: (...a: unknown[]) => playlistsDelete(...a),
  }
})
vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/ConfirmDialog', () => ({ useConfirm: () => vi.fn(async () => true) }))
vi.mock('../components/Toast', () => ({ notifyError: (e: unknown) => notifyError(e) }))

import PlaylistsPage from './PlaylistsPage'

describe('PlaylistsPage — failed delete', () => {
  beforeEach(() => { vi.clearAllMocks() })
  afterEach(cleanup)

  // Regression (#200): a failed delete was an unhandled rejection — no feedback.
  it('toasts the error and keeps the playlist', async () => {
    const err = { response: { status: 500, data: { error: 'db down' } } }
    playlistsDelete.mockRejectedValue(err)
    render(<MemoryRouter><PlaylistsPage /></MemoryRouter>)

    await userEvent.click(await screen.findByRole('button', { name: /delete/i }))
    await waitFor(() => expect(notifyError).toHaveBeenCalledWith(err))
    expect(screen.getByText('Road trip')).toBeInTheDocument()
  })
})
