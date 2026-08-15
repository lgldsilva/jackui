import { cleanup, fireEvent, render, screen } from '@testing-library/react'
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
  streamArtURL: vi.fn(() => ''),
}))
vi.mock('../api/music', () => ({ musicTrending: vi.fn(() => Promise.reject(new Error('music unavailable'))) }))

import {
  downloadsListFiltered,
  getHealth,
  libraryList,
  tmdbRecommendations,
  tmdbTrending,
} from '../api/client'
import { musicTrending } from '../api/music'
import HomePage from './HomePage'

const renderHome = () => render(<MemoryRouter><HomePage /></MemoryRouter>)

afterEach(cleanup)

describe('HomePage resilience', () => {
  beforeEach(() => {
    navigate.mockReset()
    for (const mock of [libraryList, downloadsListFiltered, tmdbRecommendations, tmdbTrending, getHealth, musicTrending]) {
      vi.mocked(mock).mockReset()
    }
    vi.mocked(libraryList).mockRejectedValue(new Error('library unavailable'))
    vi.mocked(downloadsListFiltered).mockRejectedValue(new Error('downloads unavailable'))
    vi.mocked(tmdbRecommendations).mockRejectedValue(new Error('recommendations unavailable'))
    vi.mocked(tmdbTrending).mockRejectedValue(new Error('trending unavailable'))
    vi.mocked(getHealth).mockRejectedValue(new Error('health unavailable'))
    vi.mocked(musicTrending).mockRejectedValue(new Error('music unavailable'))
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
})
