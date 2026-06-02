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

describe('amigos-share regression: a hash-less listing must not be absorbed by a hash-bearing one', () => {
  // Private trackers (amigos-share & co.) come through Jackett as .torrent-only
  // listings: no infoHash, no magnet. The name|size fallback bucket
  // (dedupNameSizeBuckets) groups them with a PUBLIC result that happens to share
  // the normalized title + 10MB size bucket; the magnet-bearing public entry wins
  // as primary and the private one is folded into `alsoIn`, vanishing as a card.
  // Without a matching infoHash we cannot prove they are the same torrent, so the
  // private listing must remain visible — it is often the only source for the
  // obscure content the public trackers don't carry.
  const PUB = 'b34fe1c06bba254a9dc9f519b335aa7c1367a999'

  it('keeps the amigos-share result visible alongside a same-title/size magnet result', () => {
    const out = groupByInfoHash([
      mk({ title: 'Filme Obscuro 2024 1080p', size: 5_000_000_000, tracker: 'ThePirateBay', seeders: 50, infoHash: PUB, magnetUri: `magnet:?xt=urn:btih:${PUB}` }),
      mk({ title: 'Filme Obscuro 2024 1080p', size: 5_000_000_000, tracker: 'amigos-share', seeders: 2, infoHash: '', magnetUri: '', link: 'https://amigos-share.example/t/123.torrent' }),
    ])
    // BUG (current): out.length === 1 — amigos-share collapsed into the magnet card's alsoIn.
    expect(out).toHaveLength(2)
    expect(out.some(r => r.tracker === 'amigos-share')).toBe(true)
  })

  it('still collapses two hash-less listings of the same release (intended dedup must survive the fix)', () => {
    const out = groupByInfoHash([
      mk({ title: 'Serie X 2024 1080p', size: 3_000_000_000, tracker: 'TrackerA', seeders: 5, infoHash: '', magnetUri: '' }),
      mk({ title: 'Serie X 2024 1080p', size: 3_000_000_000, tracker: 'TrackerB', seeders: 3, infoHash: '', magnetUri: '' }),
    ])
    expect(out).toHaveLength(1)
  })
})
