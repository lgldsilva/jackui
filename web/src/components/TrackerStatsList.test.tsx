import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import TrackerStatsList from './TrackerStatsList'
import type { TrackerScrape } from '../api/client'

const mocks = { streamTrackers: vi.fn<(hash: string, magnet?: string) => Promise<TrackerScrape[]>>() }

// TrackerStatsList calls streamTrackers(infoHash, magnet) on mount. Mock only
// that symbol on the client barrel; everything else stays real.
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    streamTrackers: (...args: [string, string?]) => mocks.streamTrackers(...args),
  }
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

function scrape(over: Partial<TrackerScrape> = {}): TrackerScrape {
  return {
    tracker: 'udp://tracker.example.com:1337/announce',
    seeders: 12,
    leechers: 3,
    ok: true,
    ...over,
  }
}

const HASH = 'a'.repeat(40)

// The header + per-row strings were translated to English; these tests pin the
// exact English copy for every branch (loading, empty, ok row, failed row).
describe('TrackerStatsList', () => {
  it('renders nothing without an infoHash', () => {
    const { container } = render(<TrackerStatsList />)
    expect(container).toBeEmptyDOMElement()
    expect(mocks.streamTrackers).not.toHaveBeenCalled()
  })

  it('shows "Querying trackers…" while the scrape request is in flight', async () => {
    let resolve!: (rows: TrackerScrape[]) => void
    mocks.streamTrackers.mockReturnValue(
      new Promise<TrackerScrape[]>(r => { resolve = r }),
    )
    render(<TrackerStatsList infoHash={HASH} />)

    expect(screen.getByText('Trackers (real seeds)')).toBeInTheDocument()
    expect(screen.getByText('Querying trackers…')).toBeInTheDocument()

    resolve([scrape()])
    await waitFor(() => expect(screen.getByText('12')).toBeInTheDocument())
    expect(screen.queryByText('Querying trackers…')).not.toBeInTheDocument()
  })

  it('shows the empty-state message when no trackers are returned', async () => {
    mocks.streamTrackers.mockResolvedValue([])
    render(<TrackerStatsList infoHash={HASH} />)

    expect(await screen.findByText('No trackers to query (magnet/.torrent without announce).')).toBeInTheDocument()
    expect(screen.queryByText('Querying trackers…')).not.toBeInTheDocument()
  })

  it('renders an ok tracker with its seeders and leech counts', async () => {
    mocks.streamTrackers.mockResolvedValue([
      scrape({ tracker: 'https://fast.example.org/announce', seeders: 42, leechers: 7 }),
    ])
    render(<TrackerStatsList infoHash={HASH} />)

    expect(await screen.findByText('https://fast.example.org/announce')).toBeInTheDocument()
    expect(screen.getByText('42')).toBeInTheDocument()
    expect(screen.getByText('7 leech')).toBeInTheDocument()
    expect(screen.getByText('Trackers (real seeds)')).toBeInTheDocument()
  })

  it('renders a failed tracker as "no response"', async () => {
    mocks.streamTrackers.mockResolvedValue([scrape({ ok: false, seeders: 0, leechers: 0 })])
    render(<TrackerStatsList infoHash={HASH} />)

    expect(await screen.findByText(/no response/)).toBeInTheDocument()
    expect(screen.queryByText('leech')).not.toBeInTheDocument()
  })
})
