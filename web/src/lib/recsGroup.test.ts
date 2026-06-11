import { describe, it, expect } from 'vitest'
import { TmdbRecommendation } from '../api/client'
import { groupRecommendations, OTHER_GROUP_KEY, OTHER_GROUP_LABEL } from './recsGroup'

function mk(tmdbId: number, becauseOf?: string, kind: 'movie' | 'tv' = 'movie'): TmdbRecommendation {
  return {
    tmdbId, title: `Title ${tmdbId}`, year: 2020, posterUrl: '', overview: '',
    voteAverage: 0, kind, ...(becauseOf !== undefined ? { becauseOf } : {}),
  }
}

const keys = (gs: ReturnType<typeof groupRecommendations>) => gs.map(g => g.key)
const ids = (gs: ReturnType<typeof groupRecommendations>, key: string) =>
  gs.find(g => g.key === key)!.items.map(i => i.tmdbId)

describe('groupRecommendations', () => {
  it('returns [] for empty/nullish input', () => {
    expect(groupRecommendations([])).toEqual([])
    expect(groupRecommendations(null)).toEqual([])
    expect(groupRecommendations(undefined)).toEqual([])
  })

  it('groups by becauseOf', () => {
    const groups = groupRecommendations([
      mk(1, 'Matrix'),
      mk(2, 'Matrix'),
      mk(3, 'Inception'),
    ])
    expect(groups).toHaveLength(2)
    expect(ids(groups, 'because:matrix')).toEqual([1, 2])
    expect(ids(groups, 'because:inception')).toEqual([3])
  })

  it('preserves first-seen group order and item order', () => {
    const groups = groupRecommendations([
      mk(10, 'Bravo'),
      mk(20, 'Alpha'),
      mk(30, 'Bravo'),  // Bravo seen first → stays first even though Alpha is alphabetically earlier
      mk(40, 'Alpha'),
    ])
    expect(keys(groups)).toEqual(['because:bravo', 'because:alpha'])
    expect(ids(groups, 'because:bravo')).toEqual([10, 30])
    expect(ids(groups, 'because:alpha')).toEqual([20, 40])
  })

  it('builds a human label from the source title', () => {
    const groups = groupRecommendations([mk(1, 'The Matrix')])
    expect(groups[0].label).toBe('Porque você viu The Matrix')
  })

  it('is case-insensitive on the key but keeps the first-seen label casing', () => {
    const groups = groupRecommendations([
      mk(1, 'Matrix'),
      mk(2, 'MATRIX'),
    ])
    expect(groups).toHaveLength(1)
    expect(groups[0].key).toBe('because:matrix')
    expect(groups[0].label).toBe('Porque você viu Matrix')
    expect(ids(groups, 'because:matrix')).toEqual([1, 2])
  })

  it('collects recs without becauseOf into a trailing generic group', () => {
    const groups = groupRecommendations([
      mk(1, 'Matrix'),
      mk(2),            // no becauseOf
      mk(3, ''),        // empty becauseOf
      mk(4, '   '),     // whitespace-only becauseOf
    ])
    expect(keys(groups)).toEqual(['because:matrix', OTHER_GROUP_KEY])
    // the generic group is always last
    expect(groups[groups.length - 1].key).toBe(OTHER_GROUP_KEY)
    expect(groups[groups.length - 1].label).toBe(OTHER_GROUP_LABEL)
    expect(ids(groups, OTHER_GROUP_KEY)).toEqual([2, 3, 4])
  })

  it('omits the generic group when every rec has a becauseOf', () => {
    const groups = groupRecommendations([mk(1, 'Matrix'), mk(2, 'Inception')])
    expect(keys(groups)).not.toContain(OTHER_GROUP_KEY)
  })

  it('trims surrounding whitespace from the source when grouping', () => {
    const groups = groupRecommendations([
      mk(1, 'Matrix'),
      mk(2, '  Matrix  '),
    ])
    expect(groups).toHaveLength(1)
    expect(ids(groups, 'because:matrix')).toEqual([1, 2])
  })
})
