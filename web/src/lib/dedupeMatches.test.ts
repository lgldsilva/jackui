import { describe, it, expect } from 'vitest'
import { dedupeMatches } from './dedupeMatches'

type Item = { kind: string; tmdbId: number; title?: string }

const mk = (kind: string, tmdbId: number, title?: string): Item => ({ kind, tmdbId, title })

describe('dedupeMatches', () => {
  it('returns an empty array for an empty input', () => {
    expect(dedupeMatches([])).toEqual([])
  })

  it('leaves an already-unique list untouched (order preserved)', () => {
    const list = [mk('movie', 1), mk('tv', 2), mk('movie', 3)]
    expect(dedupeMatches(list)).toEqual(list)
  })

  it('collapses repeats by kind-tmdbId, keeping the first occurrence', () => {
    const out = dedupeMatches([
      mk('movie', 1, 'first'),
      mk('tv', 2),
      mk('movie', 1, 'dup'), // repeat of movie 1 → dropped
      mk('tv', 2), // repeat of tv 2 → dropped
      mk('movie', 3),
    ])
    expect(out.map(m => `${m.kind}-${m.tmdbId}`)).toEqual(['movie-1', 'tv-2', 'movie-3'])
    expect(out[0].title).toBe('first') // first occurrence wins
  })

  it('keeps a movie and a tv show that share the same numeric id', () => {
    // TMDB movie ids and tv ids are separate namespaces and collide often.
    const out = dedupeMatches([mk('movie', 1399), mk('tv', 1399)])
    expect(out).toHaveLength(2)
  })

  it('does not mutate the input', () => {
    const list = [mk('movie', 1), mk('movie', 1)]
    dedupeMatches(list)
    expect(list).toHaveLength(2)
  })
})
