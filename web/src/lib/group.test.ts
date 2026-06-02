import { describe, it, expect } from 'vitest'
import { groupByInfoHash } from './group'
import type { SearchResult } from '../api/client'

function mk(over: Partial<SearchResult>): SearchResult {
  return {
    title: 'X',
    tracker: 't',
    categoryId: 0,
    category: '',
    size: 1024,
    seeders: 1,
    leechers: 0,
    age: '',
    magnetUri: '',
    link: '',
    infoHash: '',
    publishDate: '',
    ...over,
  }
}

const HASH = 'c12fe1c06bba254a9dc9f519b335aa7c1367a88a'

describe('groupByInfoHash canonicalization (defense-in-depth)', () => {
  // The backend canonicalizes infoHash, but deep links, history rows and
  // synthetic results can still reach the grouper with a raw/uppercase hash or
  // none at all. Titles below are deliberately distinct so the name|size
  // fallback can't mask a broken infoHash key — grouping must hinge on the hash.

  it('collapses the same torrent across hash CASE into one card', () => {
    const out = groupByInfoHash([
      mk({ title: 'The Mandalorian Season 1 COMPLETE 1080p', infoHash: HASH.toUpperCase(), tracker: 'A', seeders: 5, magnetUri: `magnet:?xt=urn:btih:${HASH.toUpperCase()}` }),
      mk({ title: 'Mandalorian S01 x265 HEVC', infoHash: HASH, tracker: 'B', seeders: 3, magnetUri: `magnet:?xt=urn:btih:${HASH}` }),
    ])
    expect(out).toHaveLength(1)
  })

  it('groups a magnet-only result with a hash-bearing one (derives hash from magnet)', () => {
    const out = groupByInfoHash([
      mk({ title: 'Mandalorian COMPLETE Season One', infoHash: '', tracker: 'A', seeders: 5, magnetUri: `magnet:?xt=urn:btih:${HASH}&tr=udp%3A%2F%2Ftracker.example` }),
      mk({ title: 'The Mandalorian 2019 S01 WEB-DL', infoHash: HASH, tracker: 'B', seeders: 3, magnetUri: `magnet:?xt=urn:btih:${HASH}` }),
    ])
    expect(out).toHaveLength(1)
  })

  it('keeps genuinely different torrents as separate cards', () => {
    const other = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
    const out = groupByInfoHash([
      mk({ title: 'Mandalorian S01', infoHash: HASH, tracker: 'A', magnetUri: `magnet:?xt=urn:btih:${HASH}` }),
      mk({ title: 'Andor S01', infoHash: other, tracker: 'B', magnetUri: `magnet:?xt=urn:btih:${other}` }),
    ])
    expect(out).toHaveLength(2)
  })
})
