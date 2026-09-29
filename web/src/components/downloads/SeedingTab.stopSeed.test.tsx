import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SeedingTab } from './SeedingTab'
import type { DownloadEntry } from '../../api/client'

vi.mock('../../auth/AuthContext', async () => {
  const actual = await vi.importActual<typeof import('../../auth/AuthContext')>('../../auth/AuthContext')
  return { ...actual, useAuth: () => ({ isGuest: false, isAdmin: false }) }
})

afterEach(cleanup)

function entry(over: Partial<DownloadEntry> & { id: number }): DownloadEntry {
  return {
    userId: 1,
    infoHash: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    fileIndex: 0,
    filePath: 'Pack/ep' + over.id + '.mkv',
    fileSize: 100,
    name: 'Pack',
    magnet: 'magnet:?xt=urn:btih:aaaa',
    status: 'completed',
    bytesDownloaded: 100,
    progress: 1,
    createdAt: '2026-01-01T00:00:00Z',
    ...over,
  }
}

// SeedingTab props the test doesn't exercise — all no-ops.
function noopProps() {
  return {
    completedFilter: 'all' as const,
    torrentsLoaded: true,
    busyHash: null,
    busyID: null,
    onTorrentPause: vi.fn(), onTorrentResume: vi.fn(), onTorrentPriority: vi.fn(),
    onTorrentDelete: vi.fn(), onTorrentPlay: vi.fn(),
    onPause: vi.fn(), onResume: vi.fn(), onDelete: vi.fn(), onPromote: vi.fn(),
    onSetPriority: vi.fn(), onPromoteMany: vi.fn(), onDeleteMany: vi.fn(),
    onRetryMany: vi.fn(),
    selected: new Set<number>(), onToggleSelected: vi.fn(),
    onPlay: vi.fn(), onInspect: vi.fn(), openLocalFor: () => undefined,
  }
}

// The 100%-downloaded multi-file torrent must offer "stop & remove" —
// it's the only action that takes the torrent off the list WHILE KEEPING the files on disk.
// Bug: the button was conditioned on `g.seeding`, and since DownloadsTabContent passes
// `torrents={[]}` to SeedingTab, `seeding` was always false → the button never
// showed up on a multi-file pack and the item stayed stuck on the list.
describe('SeedingTab — stop & remove on a completed group', () => {
  it('offers the action even without a live torrent on the list ("On disk" group)', async () => {
    const onStopSeedMany = vi.fn()
    const files = [entry({ id: 1 }), entry({ id: 2, fileIndex: 1 })]
    render(
      <SeedingTab
        {...noopProps()}
        torrents={[]}
        downloads={files}
        onStopSeed={vi.fn()}
        onStopSeedMany={onStopSeedMany}
      />,
    )

    const btn = screen.getByRole('button', { name: /stop & remove all/i })
    await userEvent.click(btn)
    expect(onStopSeedMany).toHaveBeenCalledTimes(1)
    expect(onStopSeedMany.mock.calls[0][0]).toHaveLength(2)
  })

  it('keeps the action when the torrent is live ("Seeding" group)', () => {
    const files = [entry({ id: 1 }), entry({ id: 2, fileIndex: 1 })]
    render(
      <SeedingTab
        {...noopProps()}
        torrents={[]}
        liveTorrents={[{ infoHash: files[0].infoHash } as never]}
        downloads={files}
        onStopSeed={vi.fn()}
        onStopSeedMany={vi.fn()}
      />,
    )

    expect(screen.getByRole('button', { name: /parar e remover todos|stop & remove all/i })).toBeInTheDocument()
  })
})
