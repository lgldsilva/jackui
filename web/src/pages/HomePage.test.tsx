import { cleanup, render, screen, waitFor } from '@testing-library/react'
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

import HomePage from './HomePage'

afterEach(cleanup)

describe('HomePage resilience', () => {
  beforeEach(() => navigate.mockReset())

  it('shows a recoverable error instead of a misleading empty state when all rails fail', async () => {
    render(<HomePage />)

    await waitFor(() => expect(screen.getByText("Couldn't load Home")).toBeInTheDocument())
    expect(screen.queryByText('Nothing here yet')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument()
  })
})
