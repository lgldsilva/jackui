import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { TorrentCard } from './TorrentCard'
import type { TorrentInfo } from '../../api/client'

// Switchable per test: the card must render the global-drop trash ONLY for
// admins — DELETE /api/stream/:hash is admin-only (shared swarm), so a
// non-admin button would do nothing but collect a 403.
const authState = vi.hoisted(() => ({ isAdmin: false }))

vi.mock('../../auth/AuthContext', async () => {
  const actual = await vi.importActual<typeof import('../../auth/AuthContext')>('../../auth/AuthContext')
  return { ...actual, useAuth: () => ({ enabled: true, isGuest: false, isAdmin: authState.isAdmin }) }
})

afterEach(cleanup)

function torrent(over: Partial<TorrentInfo> = {}): TorrentInfo {
  return {
    infoHash: 'a'.repeat(40),
    name: 'Some.Show.S01.1080p',
    totalSize: 1000,
    files: [],
    peers: 3,
    seeders: 2,
    downRate: 0,
    upRate: 0,
    progress: 0.5,
    primaryFile: 0,
    ...over,
  }
}

function renderCard() {
  const onDelete = vi.fn()
  render(
    <TorrentCard
      t={torrent()}
      busy={false}
      onPause={vi.fn()}
      onResume={vi.fn()}
      onPriority={vi.fn()}
      onDelete={onDelete}
    />,
  )
  return onDelete
}

// The trash is identified by its title (the streaming-drop tooltip), not the
// generic "Stop" label shared with other actions.
const trashTitle = /ends and removes the torrent from streaming/i

describe('TorrentCard — global drop is admin-only', () => {
  it('hides the trash for a non-admin (streamDrop is not reachable from the UI)', () => {
    authState.isAdmin = false
    renderCard()
    expect(screen.queryByTitle(trashTitle)).toBeNull()
    expect(screen.queryByRole('button', { name: /^stop$/i })).toBeNull()
  })

  it('offers the trash to an admin and wires it to the drop action', () => {
    authState.isAdmin = true
    const onDelete = renderCard()
    fireEvent.click(screen.getByTitle(trashTitle))
    expect(onDelete).toHaveBeenCalledTimes(1)
  })
})
