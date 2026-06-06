import { describe, it, expect } from 'vitest'
import { SearchResult } from '../api/client'
import { seriesKeyOf, buildSeriesLayout, SeriesLayoutItem } from './seriesGroup'

function mk(title: string, season?: number, episode?: number): SearchResult {
  return {
    title, tracker: 't', categoryId: 5000, category: 'TV', size: 1, seeders: 1,
    leechers: 0, age: '1d', magnetUri: `magnet:?xt=urn:btih:${title}`, link: title,
    infoHash: title, publishDate: '2024-01-01',
    quality: season != null || episode != null ? { season, episode } : undefined,
  }
}

const headers = (l: SeriesLayoutItem[]) => l.filter(i => i.kind === 'header') as Extract<SeriesLayoutItem, { kind: 'header' }>[]
const titles = (l: SeriesLayoutItem[]) => l.filter(i => i.kind === 'result').map(i => (i as Extract<SeriesLayoutItem, { kind: 'result' }>).result.title)

describe('seriesKeyOf', () => {
  it('extracts the series name from SxxExx releases', () => {
    expect(seriesKeyOf('The.Show.S01E02.1080p.WEB-DL')).toBe('the show')
    expect(seriesKeyOf('Some Show S03E10 HDTV')).toBe('some show')
  })
  it('handles the NxNN form', () => {
    expect(seriesKeyOf('Another_Show_2x05_720p')).toBe('another show')
  })
  it('returns empty for non-episode titles', () => {
    expect(seriesKeyOf('Big Movie 2021 1080p BluRay')).toBe('')
    expect(seriesKeyOf('Just A Name')).toBe('')
  })
})

describe('buildSeriesLayout', () => {
  it('groups a series with >= 2 episodes under a season header, sorted by episode', () => {
    const layout = buildSeriesLayout([
      mk('The.Show.S01E02.1080p', 1, 2),
      mk('The.Show.S01E01.1080p', 1, 1),
    ])
    const hs = headers(layout)
    expect(hs).toHaveLength(1)
    expect(hs[0].series).toBe('The Show')
    expect(hs[0].season).toBe(1)
    expect(hs[0].count).toBe(2)
    // Episodes ordered E01 then E02.
    expect(titles(layout)).toEqual(['The.Show.S01E01.1080p', 'The.Show.S01E02.1080p'])
  })

  it('splits seasons into separate headers', () => {
    const layout = buildSeriesLayout([
      mk('Show.S02E01', 2, 1),
      mk('Show.S01E01', 1, 1),
      mk('Show.S01E02', 1, 2),
      mk('Show.S02E02', 2, 2),
    ])
    const hs = headers(layout)
    expect(hs.map(h => h.season)).toEqual([1, 2]) // seasons ascending
  })

  it('leaves a lone episode and non-episodes loose at the end (original order)', () => {
    const layout = buildSeriesLayout([
      mk('A.Movie.2021.1080p'),          // not an episode
      mk('Lone.Show.S01E01', 1, 1),      // single episode → not grouped
      mk('The.Show.S01E01', 1, 1),       // grouped (2 eps)
      mk('The.Show.S01E02', 1, 2),
    ])
    expect(headers(layout)).toHaveLength(1)
    // Grouped episodes come first (under the header), loose afterwards in order.
    expect(titles(layout)).toEqual([
      'The.Show.S01E01', 'The.Show.S01E02',
      'A.Movie.2021.1080p', 'Lone.Show.S01E01',
    ])
  })

  it('does not group when nothing qualifies', () => {
    const layout = buildSeriesLayout([mk('A 2021'), mk('B 2020')])
    expect(headers(layout)).toHaveLength(0)
    expect(titles(layout)).toEqual(['A 2021', 'B 2020'])
  })
})
