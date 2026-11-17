import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { deferred, type Deferred } from '../lib/deferred.testutil'
import { setRevealHidden } from '../lib/reveal'

const m = vi.hoisted(() => ({ favorites: vi.fn(), folders: vi.fn(), remove: vi.fn() }))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    favoritesList: (...a: unknown[]) => m.favorites(...a),
    folderList: (...a: unknown[]) => m.folders(...a),
    favoriteRemove: (...a: unknown[]) => m.remove(...a),
    downloadsList: vi.fn().mockResolvedValue([]),
  }
})
vi.mock('../auth/AuthContext', async () => {
  const actual = await vi.importActual<typeof import('../auth/AuthContext')>('../auth/AuthContext')
  return { ...actual, useAuth: () => ({ isAdmin: false }) }
})
vi.mock('../components/PlayerProvider', () => ({ usePlayer: () => ({ playSingle: vi.fn() }) }))
vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/DownloadModal', () => ({ default: () => null }))
vi.mock('../components/TorrentContentsModal', () => ({ default: () => null }))
vi.mock('../components/ConfirmDialog', () => ({ useConfirm: () => vi.fn(async () => true) }))
vi.mock('../components/Toast', () => ({ useToast: () => ({ notify: vi.fn(), notifyError: vi.fn() }) }))
vi.mock('../components/favorites/FavoriteCard', () => ({
  default: ({ fav, onRemove }: { fav: { name: string }; onRemove: () => void }) => (
    <div>
      <span data-testid="fav">{fav.name}</span>
      <button onClick={onRemove}>remove {fav.name}</button>
    </div>
  ),
}))

import FavoritesPage from './FavoritesPage'

const fav = (name: string) => ({
  name, infoHash: name, magnet: '', userId: 1, favoritedAt: '2026-01-01T00:00:00Z',
  reason: 'manual' as const, folderId: null,
})
const VISIBLE = [fav('Public movie')]
const WITH_HIDDEN = [fav('Public movie'), fav('Secret movie')]
const SECRET_FOLDER = { id: 9, userId: 1, name: 'Secret folder', parentId: null, hidden: true, createdAt: '' }
const names = () => screen.queryAllByTestId('fav').map(el => el.textContent)

// Every favoritesList call is held open; the test settles each one by hand.
type Call = { includeHidden: boolean; d: Deferred<unknown[]> }
let calls: Call[]
const settle = (call: Call, value: unknown[]) => act(async () => { call.d.resolve(value) })
const closeCurtain = () => act(async () => { setRevealHidden(false) })

describe('FavoritesPage — hidden curtain stays closed (#223)', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    localStorage.clear()
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    calls = []
    m.folders.mockImplementation(async (includeHidden = false) => (includeHidden ? [SECRET_FOLDER] : []))
    m.favorites.mockImplementation((includeHidden = false) => {
      const d = deferred<unknown[]>()
      calls.push({ includeHidden: includeHidden as boolean, d })
      return d.promise
    })
    setRevealHidden(true)
    render(<MemoryRouter><FavoritesPage /></MemoryRouter>)
    await act(async () => { await Promise.resolve() })
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
    setRevealHidden(false)
  })

  it('drops a revealed load that answers after the curtain was closed', async () => {
    const [revealed] = calls
    expect(revealed.includeHidden).toBe(true)

    await closeCurtain()
    const closed = calls[calls.length - 1]
    expect(closed.includeHidden).toBe(false)
    await settle(closed, VISIBLE)
    await settle(revealed, WITH_HIDDEN)

    expect(names()).toEqual(['Public movie'])
  })

  it('does not run the delayed seed reload with the reveal flag it was scheduled under', async () => {
    await settle(calls[0], WITH_HIDDEN)
    expect(names()).toContain('Secret movie')
    fireEvent.click(screen.getByText('Refresh seeds'))

    await closeCurtain()
    await settle(calls[calls.length - 1], VISIBLE)
    const before = calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(11_000) })
    for (const late of calls.slice(before)) {
      expect(late.includeHidden).toBe(false)
      await settle(late, WITH_HIDDEN)
    }

    expect(names()).toEqual(['Public movie'])
  })

  it('drops a delayed seed reload still in flight when the curtain closes', async () => {
    await settle(calls[0], WITH_HIDDEN)
    fireEvent.click(screen.getByText('Refresh seeds'))
    await act(async () => { await vi.advanceTimersByTimeAsync(11_000) })
    const quiet = calls[calls.length - 1]
    expect(calls).toHaveLength(2)

    await closeCurtain()
    await settle(calls[calls.length - 1], VISIBLE)
    await settle(quiet, WITH_HIDDEN)

    expect(names()).toEqual(['Public movie'])
  })

  it('still applies the delayed seed reload while the curtain state is unchanged', async () => {
    await settle(calls[0], VISIBLE)
    fireEvent.click(screen.getByText('Refresh seeds'))
    await act(async () => { await vi.advanceTimersByTimeAsync(11_000) })
    expect(calls).toHaveLength(2)
    await settle(calls[1], WITH_HIDDEN)

    expect(names()).toEqual(['Public movie', 'Secret movie'])
  })

  it('removes revealed folders and favorites at once, even if the reload fails', async () => {
    await settle(calls[0], WITH_HIDDEN)
    expect(screen.getAllByText('Secret folder').length).toBeGreaterThan(0)

    await closeCurtain()
    await act(async () => { calls[calls.length - 1].d.reject(new Error('offline')) })

    expect(screen.queryByText('Secret folder')).not.toBeInTheDocument()
    expect(names()).toEqual([])
  })

  it('does not restore revealed favorites when a mutation started before the curtain closed finishes', async () => {
    const removing = deferred<void>()
    m.remove.mockReturnValue(removing.promise)
    await settle(calls[0], WITH_HIDDEN)
    fireEvent.click(screen.getByText('remove Public movie'))
    await act(async () => { await Promise.resolve() })
    expect(m.remove).toHaveBeenCalledWith('Public movie')

    await closeCurtain()
    await settle(calls[calls.length - 1], VISIBLE)
    await act(async () => { removing.resolve() })

    expect(names()).toEqual([])
  })
})
