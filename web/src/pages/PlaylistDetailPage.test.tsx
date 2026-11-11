import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

const m = vi.hoisted(() => ({
  notifyError: vi.fn(),
  get: vi.fn(),
  update: vi.fn(),
  removeItem: vi.fn(),
  reorder: vi.fn(),
}))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    playlistsGet: (...a: unknown[]) => m.get(...a),
    playlistsUpdate: (...a: unknown[]) => m.update(...a),
    playlistsRemoveItem: (...a: unknown[]) => m.removeItem(...a),
    playlistsReorderItem: (...a: unknown[]) => m.reorder(...a),
  }
})
vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/PlayerProvider', () => ({ usePlayer: () => ({ playPlaylist: vi.fn() }) }))
vi.mock('../components/Toast', () => ({ notifyError: (e: unknown) => m.notifyError(e) }))

import PlaylistDetailPage from './PlaylistDetailPage'

const item = (id: number, title: string) => ({
  id, playlistId: 7, position: id - 1, title, magnet: '', infoHash: String(id).repeat(40), fileIndex: 0, addedAt: '',
})
const data = {
  playlist: { id: 7, name: 'Road trip', description: '', createdAt: '', updatedAt: '' },
  items: [item(1, 'First song'), item(2, 'Second song')],
}
const boom = { response: { status: 500, data: { error: 'db down' } } }

const renderPage = () => render(
  <MemoryRouter initialEntries={['/playlists/7']}>
    <Routes><Route path="/playlists/:id" element={<PlaylistDetailPage />} /></Routes>
  </MemoryRouter>,
)

describe('PlaylistDetailPage failures surface as toasts (#200)', () => {
  beforeEach(() => { vi.clearAllMocks(); m.get.mockResolvedValue(data) })
  afterEach(cleanup)

  it('toasts a failed load', async () => {
    m.get.mockRejectedValue(boom)
    renderPage()
    await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
  })

  it('toasts a failed item removal and keeps the item', async () => {
    m.removeItem.mockRejectedValue(boom)
    renderPage()
    const [first] = await screen.findAllByRole('button', { name: 'Remove from playlist' })
    await userEvent.click(first)
    await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
    expect(screen.getAllByRole('button', { name: 'Remove from playlist' })).toHaveLength(2)
  })

  it('toasts a failed rename', async () => {
    m.update.mockRejectedValue(boom)
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: /edit/i }))
    await userEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
  })

  it('toasts a failed reorder and reloads from the server', async () => {
    m.reorder.mockRejectedValue(boom)
    renderPage()
    const [down] = await screen.findAllByRole('button', { name: 'Move down' })
    await userEvent.click(down)
    await waitFor(() => expect(m.notifyError).toHaveBeenCalledWith(boom))
    await waitFor(() => expect(m.get).toHaveBeenCalledTimes(2))
  })
})
