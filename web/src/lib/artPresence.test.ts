import { describe, it, expect } from 'vitest'
import {
  artResultHasArt,
  artPresenceFromBatch,
  artBustsFromBatch,
  mergeArtBustMaps,
  mergeArtPresence,
  shouldMountArtImg,
  withArtBust,
} from './artPresence'

describe('artResultHasArt', () => {
  it('is false for empty / missing', () => {
    expect(artResultHasArt(undefined)).toBe(false)
    expect(artResultHasArt(null)).toBe(false)
    expect(artResultHasArt({})).toBe(false)
    expect(artResultHasArt({ resolved: false })).toBe(false)
  })

  it('is true when source is set (any art origin)', () => {
    expect(artResultHasArt({ source: 'torrent' })).toBe(true)
    expect(artResultHasArt({ source: 'tmdb' })).toBe(true)
    expect(artResultHasArt({ source: 'web' })).toBe(true)
    expect(artResultHasArt({ source: 'frame', status: 'processing' })).toBe(true)
  })

  it('is true for resolved:true without source', () => {
    expect(artResultHasArt({ resolved: true })).toBe(true)
  })
})

describe('artPresenceFromBatch', () => {
  it('maps each hash to a boolean presence flag', () => {
    expect(artPresenceFromBatch({
      aaa: { source: 'torrent' },
      bbb: { resolved: false },
      ccc: {},
    })).toEqual({ aaa: true, bbb: false, ccc: false })
  })
})

describe('artBustsFromBatch', () => {
  it('only stamps hashes that have art', () => {
    const now = 1_700_000_000_000
    expect(artBustsFromBatch({
      aaa: { source: 'tmdb' },
      bbb: { resolved: false },
    }, now)).toEqual({ aaa: now })
  })
})

describe('merge maps', () => {
  it('mergeArtBustMaps overlays bumps', () => {
    expect(mergeArtBustMaps({ a: 1 }, { b: 2, a: 3 })).toEqual({ a: 3, b: 2 })
  })
  it('mergeArtPresence overlays presence', () => {
    expect(mergeArtPresence({ a: false }, { a: true, b: false })).toEqual({ a: true, b: false })
  })
})

describe('shouldMountArtImg', () => {
  const hash = 'deadbeef'

  it('never mounts without infoHash or after artFailed', () => {
    expect(shouldMountArtImg({ hasArt: true })).toBe(false)
    expect(shouldMountArtImg({ infoHash: hash, hasArt: true, artFailed: true })).toBe(false)
  })

  it('mounts when hasArt is true', () => {
    expect(shouldMountArtImg({ infoHash: hash, hasArt: true })).toBe(true)
  })

  it('skips when hasArt is false', () => {
    expect(shouldMountArtImg({ infoHash: hash, hasArt: false })).toBe(false)
    expect(shouldMountArtImg({ infoHash: hash, hasArt: false, requireKnown: true })).toBe(false)
  })

  it('requireKnown skips unknown presence (library grid)', () => {
    expect(shouldMountArtImg({ infoHash: hash, requireKnown: true })).toBe(false)
    expect(shouldMountArtImg({ infoHash: hash, hasArt: undefined, requireKnown: true })).toBe(false)
  })

  it('legacy Thumbnail path mounts when presence unknown', () => {
    expect(shouldMountArtImg({ infoHash: hash })).toBe(true)
    expect(shouldMountArtImg({ infoHash: hash, requireKnown: false })).toBe(true)
  })
})

describe('withArtBust', () => {
  it('leaves url alone when bust missing or zero', () => {
    expect(withArtBust('/api/stream/art/x')).toBe('/api/stream/art/x')
    expect(withArtBust('/api/stream/art/x', 0)).toBe('/api/stream/art/x')
  })
  it('appends ?_ when no query', () => {
    expect(withArtBust('/api/stream/art/x', 9)).toBe('/api/stream/art/x?_=9')
  })
  it('appends &_ when query already present', () => {
    expect(withArtBust('/api/stream/art/x?token=1', 9)).toBe('/api/stream/art/x?token=1&_=9')
  })
})
