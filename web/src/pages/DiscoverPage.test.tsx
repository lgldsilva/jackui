import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { deferred, type Deferred } from '../lib/deferred.testutil'

const m = vi.hoisted(() => ({ trending: vi.fn() }))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    tmdbTrending: (...a: unknown[]) => m.trending(...a),
    tmdbGenres: vi.fn().mockResolvedValue([]),
    tmdbRecommendations: vi.fn().mockResolvedValue([]),
  }
})
vi.mock('../components/NavHeader', () => ({ default: () => null }))

import DiscoverPage from './DiscoverPage'

const match = (tmdbId: number, title: string) => ({
  tmdbId, title, year: 2020, posterUrl: '', overview: '', voteAverage: 0, kind: 'movie' as const,
})

describe('DiscoverPage request ordering (#223)', () => {
  beforeEach(() => { vi.clearAllMocks(); localStorage.clear() })
  afterEach(cleanup)

  it('keeps the trending list of the current year when the previous filter answers late', async () => {
    const pending = new Map<number, Deferred<unknown[]>>()
    m.trending.mockImplementation((opts: { year: number }) => {
      const d = deferred<unknown[]>()
      pending.set(opts.year, d)
      return d.promise
    })
    render(<MemoryRouter><DiscoverPage /></MemoryRouter>)
    await waitFor(() => expect(pending.has(0)).toBe(true))

    fireEvent.change(screen.getByTitle('Filter by year'), { target: { value: '2020' } })
    await waitFor(() => expect(pending.has(2020)).toBe(true))

    await act(async () => { pending.get(2020)?.resolve([match(2, 'Picked year hit')]) })
    await act(async () => { pending.get(0)?.resolve([match(1, 'Any year hit')]) })

    expect(screen.getByAltText('Picked year hit')).toBeInTheDocument()
    expect(screen.queryByAltText('Any year hit')).not.toBeInTheDocument()
  })
})
