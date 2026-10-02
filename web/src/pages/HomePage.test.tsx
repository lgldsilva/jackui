import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const navigate = vi.fn()

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return {
    ...actual,
    useNavigate: () => navigate,
    useSearchParams: () => [new URLSearchParams()],
  }
})

vi.mock('../components/NavHeader', () => ({ default: () => <nav aria-label="main navigation" /> }))
vi.mock('../components/PlayerProvider', () => ({ usePlayer: () => ({ playSingle: vi.fn() }) }))
vi.mock('../lib/mediaMode', () => ({ useMediaMode: () => ['video', vi.fn()] }))
vi.mock('../api/client', () => ({
  libraryList: vi.fn(() => Promise.reject(new Error('library unavailable'))),
  downloadsListFiltered: vi.fn(() => Promise.reject(new Error('downloads unavailable'))),
  tmdbRecommendations: vi.fn(() => Promise.reject(new Error('recommendations unavailable'))),
  tmdbTrending: vi.fn(() => Promise.reject(new Error('trending unavailable'))),
  getHealth: vi.fn(() => Promise.reject(new Error('health unavailable'))),
  streamArtURL: vi.fn((hash: string) => `/api/stream/art/${hash}`),
  resolveArtBatch: vi.fn(() => Promise.resolve({})),
  tmdbMatch: vi.fn(() => Promise.resolve(null)),
}))
vi.mock('../api/music', () => ({ musicTrending: vi.fn(() => Promise.reject(new Error('music unavailable'))) }))

import {
  downloadsListFiltered,
  getHealth,
  libraryList,
  resolveArtBatch,
  tmdbMatch,
  tmdbRecommendations,
  tmdbTrending,
} from '../api/client'
import { musicTrending } from '../api/music'
import { setRevealHidden } from '../lib/reveal'
import HomePage from './HomePage'

const renderHome = () => render(<MemoryRouter><HomePage /></MemoryRouter>)

afterEach(() => {
  cleanup()
  // reveal is a module-level flag: reset so tests below start curtain-closed.
  setRevealHidden(false)
})

describe('HomePage resilience', () => {
  beforeEach(() => {
    navigate.mockReset()
    for (const mock of [libraryList, downloadsListFiltered, tmdbRecommendations, tmdbTrending, getHealth, musicTrending, resolveArtBatch, tmdbMatch]) {
      vi.mocked(mock).mockReset()
    }
    vi.mocked(libraryList).mockRejectedValue(new Error('library unavailable'))
    vi.mocked(downloadsListFiltered).mockRejectedValue(new Error('downloads unavailable'))
    vi.mocked(tmdbRecommendations).mockRejectedValue(new Error('recommendations unavailable'))
    vi.mocked(tmdbTrending).mockRejectedValue(new Error('trending unavailable'))
    vi.mocked(getHealth).mockRejectedValue(new Error('health unavailable'))
    vi.mocked(musicTrending).mockRejectedValue(new Error('music unavailable'))
    vi.mocked(resolveArtBatch).mockResolvedValue({})
    vi.mocked(tmdbMatch).mockResolvedValue(null)
  })

  it('shows a recoverable error instead of a misleading empty state when all rails fail', async () => {
    renderHome()

    await screen.findByText("Couldn't load Home")
    expect(screen.queryByText('Nothing here yet')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument()
  })

  it('keeps an existing rail visible while a retry is still pending', async () => {
    vi.mocked(libraryList).mockResolvedValue([{
      id: 1, userId: 1, infoHash: 'hash', magnet: 'magnet:?xt=urn:btih:hash', name: 'Previously playing',
      primaryFileIndex: 0, lastFileIndex: 0, totalSize: 100, resumeSeconds: 20, durationSeconds: 100,
      kind: 'video', lastPlayedAt: 'now', addedAt: 'now',
    }])

    renderHome()
    await screen.findByText('Previously playing')

    let rejectRetry: (reason?: unknown) => void = () => undefined
    const retryLibrary = new Promise<never>((_, reject) => { rejectRetry = reject })
    vi.mocked(libraryList).mockImplementation(() => retryLibrary)
    fireEvent.click(screen.getByRole('button', { name: /try again/i }))

    expect(screen.getByText('Previously playing')).toBeInTheDocument()
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument()

    rejectRetry(new Error('library unavailable'))
    await screen.findByText("Couldn't load Home")
    expect(screen.getByText('Previously playing')).toBeInTheDocument()
  })

  // THE stale-Continue-rail bug: the Continue Watching rail used to keep the
  // list fetched while the curtain was open after the user disabled the easter
  // egg (Library/Downloads/Favorites/Local re-fetch on the flip; Home didn't).
  it('re-fetches the rails when the hidden curtain flips', async () => {
    vi.mocked(libraryList).mockResolvedValue([{
      id: 1, userId: 1, infoHash: 'hash', magnet: 'magnet:?xt=urn:btih:hash', name: 'Previously playing',
      primaryFileIndex: 0, lastFileIndex: 0, totalSize: 100, resumeSeconds: 20, durationSeconds: 100,
      kind: 'video', lastPlayedAt: 'now', addedAt: 'now',
    }])

    renderHome()
    await screen.findByText('Previously playing')
    const callsBefore = vi.mocked(libraryList).mock.calls.length

    setRevealHidden(true)

    await waitFor(() => expect(vi.mocked(libraryList).mock.calls.length).toBeGreaterThan(callsBefore))
  })
})

describe('Home card art', () => {
  const libHash = 'a'.repeat(40)
  const dlHash = 'b'.repeat(40)

  // The global test-setup stubs IntersectionObserver as a no-op, so
  // useThumbnail never fires. These tests need the TMDB layer: fire the
  // callback on observe, like the hook's own no-observer fallback does.
  beforeEach(() => {
    vi.stubGlobal('IntersectionObserver', class {
      private cb: IntersectionObserverCallback
      constructor(cb: IntersectionObserverCallback) { this.cb = cb }
      observe() {
        this.cb([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver)
      }
      unobserve() { return }
      disconnect() { return }
      takeRecords() { return [] }
    })
  })

  const entry = (infoHash: string, name = 'Previously playing') => ({
    id: 1, userId: 1, infoHash, magnet: `magnet:?xt=urn:btih:${infoHash}`, name,
    primaryFileIndex: 0, lastFileIndex: 0, totalSize: 100, resumeSeconds: 20, durationSeconds: 100,
    kind: 'video', lastPlayedAt: 'now', addedAt: 'now',
  })

  const download = (infoHash: string) => ({
    id: 2, userId: 1, infoHash, fileIndex: 0, filePath: 'file.mp4', fileSize: 100,
    name: 'Moana.2026.1080p', magnet: `magnet:?xt=urn:btih:${infoHash}`,
    status: 'completed' as const, bytesDownloaded: 100, progress: 1,
    createdAt: 'now',
  })

  beforeEach(() => {
    navigate.mockReset()
    for (const mock of [libraryList, downloadsListFiltered, tmdbRecommendations, tmdbTrending, getHealth, musicTrending, resolveArtBatch, tmdbMatch]) {
      vi.mocked(mock).mockReset()
    }
    vi.mocked(libraryList).mockRejectedValue(new Error('library unavailable'))
    vi.mocked(downloadsListFiltered).mockRejectedValue(new Error('downloads unavailable'))
    vi.mocked(tmdbRecommendations).mockRejectedValue(new Error('recommendations unavailable'))
    vi.mocked(tmdbTrending).mockRejectedValue(new Error('trending unavailable'))
    vi.mocked(getHealth).mockRejectedValue(new Error('health unavailable'))
    vi.mocked(musicTrending).mockRejectedValue(new Error('music unavailable'))
    vi.mocked(resolveArtBatch).mockResolvedValue({})
    vi.mocked(tmdbMatch).mockResolvedValue(null)
  })

  // The batch resolve is what makes the rails recover art without a play:
  // one call must carry every rail hash (file -1 — frames stay play-only).
  it('seeds one art batch covering both rails', async () => {
    vi.mocked(libraryList).mockResolvedValue([entry(libHash)])
    vi.mocked(downloadsListFiltered).mockResolvedValue([download(dlHash)])

    renderHome()

    await waitFor(() => expect(vi.mocked(resolveArtBatch)).toHaveBeenCalledTimes(1))
    const items = vi.mocked(resolveArtBatch).mock.calls[0][0]
    expect(items.map(i => i.hash)).toEqual(expect.arrayContaining([libHash, dlHash]))
    for (const item of items) {
      expect(item.file).toBe(-1)
      expect(item.name).toBeTruthy()
    }
  })

  // THE grey-card bug: a resolved art miss (204) used to leave the rail tile
  // blank; now the title's TMDB poster shows instead and the art GET is skipped.
  it('falls back to the TMDB poster when the torrent art is missing', async () => {
    vi.mocked(libraryList).mockResolvedValue([entry(libHash, 'Coyote.vs.Acme.2026.1080p.WEBRip')])
    vi.mocked(resolveArtBatch).mockResolvedValue({ [libHash]: { resolved: false } })
    vi.mocked(tmdbMatch).mockResolvedValue({
      tmdbId: 1, title: 'Coyote vs. Acme', year: 2026,
      posterUrl: 'https://image.tmdb.org/t/p/w300/coyote.jpg',
      overview: '', voteAverage: 7, kind: 'movie',
    })

    renderHome()

    await waitFor(() =>
      expect(document.querySelector('img[src="https://image.tmdb.org/t/p/w300/coyote.jpg"]')).not.toBeNull(),
    )
    expect(document.querySelector(`img[src="/api/stream/art/${libHash}"]`)).toBeNull()
  })

  // THE batch-vs-204 race: while the batch is in flight the card must fire NO
  // art GET (a premature GET 204s first and poisons artFailed, so the art the
  // batch then persists would never mount). Once the batch answers with art,
  // the <img> must mount already carrying the cache-bust.
  it('keeps the art GET off while the batch is pending, then mounts it with the bust', async () => {
    let resolveBatch: (r: Record<string, { source: string; reused: boolean }>) => void = () => undefined
    vi.mocked(resolveArtBatch).mockImplementation(() => new Promise(resolve => { resolveBatch = resolve }))
    vi.mocked(libraryList).mockResolvedValue([entry(libHash)])

    renderHome()
    await screen.findByText('Previously playing')
    expect(document.querySelector(`img[src^="/api/stream/art/${libHash}"]`)).toBeNull()

    resolveBatch({ [libHash]: { source: 'tmdb', reused: true } })
    await waitFor(() =>
      expect(document.querySelector(`img[src^="/api/stream/art/${libHash}?_="]`)).not.toBeNull(),
    )
  })

  // resolveArtBatch swallows network/5xx into {} — the legacy mount must take
  // over, and a genuine 204 (onError) still hides just the art layer.
  it('treats an empty batch result as a failure and falls back to legacy art', async () => {
    vi.mocked(libraryList).mockResolvedValue([entry(libHash)])
    vi.mocked(resolveArtBatch).mockResolvedValue({})

    renderHome()
    const art = await waitFor(() => {
      const el = document.querySelector(`img[src="/api/stream/art/${libHash}"]`)
      expect(el).not.toBeNull()
      return el as HTMLImageElement
    })
    expect(art.getAttribute('src')).not.toContain('?_=')

    fireEvent.error(art)
    await waitFor(() => expect(document.querySelector(`img[src^="/api/stream/art/${libHash}"]`)).toBeNull())
  })
})
