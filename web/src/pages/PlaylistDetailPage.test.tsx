import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { deferred, type Deferred } from '../lib/deferred.testutil'

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

describe('PlaylistDetailPage request ordering (#223)', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  const named = (id: number, name: string) => ({
    playlist: { ...data.playlist, id, name },
    items: [],
  })

  it('keeps the playlist of the current route when the previous one answers late', async () => {
    const pending = new Map<number, Deferred<unknown>>()
    m.get.mockImplementation((id: number) => {
      const d = deferred<unknown>()
      pending.set(id, d)
      return d.promise
    })
    let go: (to: string) => void = () => {}
    const Nav = () => { go = useNavigate(); return null }
    render(
      <MemoryRouter initialEntries={['/playlists/7']}>
        <Nav />
        <Routes><Route path="/playlists/:id" element={<PlaylistDetailPage />} /></Routes>
      </MemoryRouter>,
    )
    await waitFor(() => expect(pending.has(7)).toBe(true))
    act(() => go('/playlists/8'))
    await waitFor(() => expect(pending.has(8)).toBe(true))

    await act(async () => { pending.get(8)?.resolve(named(8, 'Current playlist')) })
    await act(async () => { pending.get(7)?.resolve(named(7, 'Previous playlist')) })

    expect(screen.getByText('Current playlist')).toBeInTheDocument()
    expect(screen.queryByText('Previous playlist')).not.toBeInTheDocument()
  })

  it('does not toast a late failure of the previous playlist', async () => {
    const pending = new Map<number, Deferred<unknown>>()
    m.get.mockImplementation((id: number) => {
      const d = deferred<unknown>()
      pending.set(id, d)
      return d.promise
    })
    let go: (to: string) => void = () => {}
    const Nav = () => { go = useNavigate(); return null }
    render(
      <MemoryRouter initialEntries={['/playlists/7']}>
        <Nav />
        <Routes><Route path="/playlists/:id" element={<PlaylistDetailPage />} /></Routes>
      </MemoryRouter>,
    )
    await waitFor(() => expect(pending.has(7)).toBe(true))
    act(() => go('/playlists/8'))
    await waitFor(() => expect(pending.has(8)).toBe(true))

    await act(async () => { pending.get(8)?.resolve(named(8, 'Current playlist')) })
    await act(async () => { pending.get(7)?.reject(boom) })

    expect(m.notifyError).not.toHaveBeenCalled()
    expect(screen.getByText('Current playlist')).toBeInTheDocument()
  })

  it('shows not-found instead of an endless spinner for a non-numeric id', async () => {
    render(
      <MemoryRouter initialEntries={['/playlists/abc']}>
        <Routes><Route path="/playlists/:id" element={<PlaylistDetailPage />} /></Routes>
      </MemoryRouter>,
    )
    expect(await screen.findByText('Playlist not found')).toBeInTheDocument()
    expect(m.get).not.toHaveBeenCalled()
  })
})
