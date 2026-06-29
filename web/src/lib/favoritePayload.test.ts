import { describe, it, expect, vi } from 'vitest'
import {
  buildFavoritePayload,
  extractInfoHashFromMagnet,
  magnetFromInfoHash,
  TorrentLinkResolver,
} from './favoritePayload'

const HASH = 'a'.repeat(40)
const OTHER = 'b'.repeat(40)
const MAGNET = `magnet:?xt=urn:btih:${HASH}&dn=Some.Release&tr=http%3A%2F%2Ftracker%2Fannounce`

const neverResolver: TorrentLinkResolver = vi.fn(() => {
  throw new Error('resolver must not be called')
})

describe('extractInfoHashFromMagnet', () => {
  it('extracts and lowercases a canonical 40-hex hash', () => {
    expect(extractInfoHashFromMagnet(`magnet:?xt=urn:btih:${HASH.toUpperCase()}`)).toBe(HASH)
  })

  it('returns empty for missing or non-canonical (base32) hashes', () => {
    expect(extractInfoHashFromMagnet('magnet:?dn=NoHash')).toBe('')
    expect(extractInfoHashFromMagnet('magnet:?xt=urn:btih:ZOCMZQIPFFW7OLLMIC5HUB6BPCSDEOQU')).toBe('')
    expect(extractInfoHashFromMagnet('')).toBe('')
  })
})

describe('magnetFromInfoHash', () => {
  it('synthesizes a tracker-less magnet', () => {
    expect(magnetFromInfoHash(HASH)).toBe(`magnet:?xt=urn:btih:${HASH}`)
  })
})

describe('buildFavoritePayload', () => {
  it('uses the magnet as-is when present (no resolver call)', async () => {
    const p = await buildFavoritePayload({ magnetUri: MAGNET, infoHash: HASH, link: 'http://x/t.torrent' }, neverResolver)
    expect(p).toEqual({ infoHash: HASH, magnet: MAGNET, source: 'magnet' })
    expect(neverResolver).not.toHaveBeenCalled()
  })

  it('extracts the infoHash from the magnet when the field is missing', async () => {
    const p = await buildFavoritePayload({ magnetUri: MAGNET }, neverResolver)
    expect(p.infoHash).toBe(HASH)
    expect(p.source).toBe('magnet')
  })

  it('converts a .torrent-only result through the resolver (the history bug)', async () => {
    const resolver = vi.fn(async () => ({ magnet: MAGNET, infoHash: HASH }))
    const p = await buildFavoritePayload({ link: 'http://indexer/file.torrent' }, resolver)
    expect(resolver).toHaveBeenCalledWith('http://indexer/file.torrent')
    expect(p).toEqual({ infoHash: HASH, magnet: MAGNET, source: 'link' })
  })

  it('synthesizes a magnet when the resolver only returns an infoHash', async () => {
    const resolver: TorrentLinkResolver = async () => ({ magnet: '', infoHash: OTHER })
    const p = await buildFavoritePayload({ link: 'http://x/t.torrent' }, resolver)
    expect(p).toEqual({ infoHash: OTHER, magnet: magnetFromInfoHash(OTHER), source: 'link' })
  })

  it('extracts the infoHash when the resolver only returns a magnet', async () => {
    const resolver: TorrentLinkResolver = async () => ({ magnet: MAGNET, infoHash: '' })
    const p = await buildFavoritePayload({ link: 'http://x/t.torrent' }, resolver)
    expect(p).toEqual({ infoHash: HASH, magnet: MAGNET, source: 'link' })
  })

  it('falls back to the bare infoHash when the resolver fails', async () => {
    const resolver: TorrentLinkResolver = async () => { throw new Error('dead link') }
    const p = await buildFavoritePayload({ link: 'http://x/t.torrent', infoHash: HASH }, resolver)
    expect(p).toEqual({ infoHash: HASH, magnet: magnetFromInfoHash(HASH), source: 'infoHash' })
  })

  it('falls back to the bare infoHash when the resolver returns nothing usable', async () => {
    const resolver: TorrentLinkResolver = async () => ({ magnet: '', infoHash: '' })
    const p = await buildFavoritePayload({ link: 'http://x/t.torrent', infoHash: HASH }, resolver)
    expect(p.source).toBe('infoHash')
    expect(p.magnet).toBe(magnetFromInfoHash(HASH))
  })

  it('keeps the raw .torrent link as the magnet when the resolver fails and there is no infoHash', async () => {
    // Recoverable favorite beats an inert one: streamAdd re-resolves a .torrent
    // /magnetdownload URL at Play time, and favHasValidMagnet accepts http(s).
    const resolver: TorrentLinkResolver = async () => { throw new Error('dead link') }
    const p = await buildFavoritePayload({ link: 'http://x/t.torrent' }, resolver)
    expect(p).toEqual({ infoHash: '', magnet: 'http://x/t.torrent', source: 'link' })
  })

  it('synthesizes a magnet for a bare infoHash without magnet or link', async () => {
    const p = await buildFavoritePayload({ infoHash: HASH }, neverResolver)
    expect(p).toEqual({ infoHash: HASH, magnet: magnetFromInfoHash(HASH), source: 'infoHash' })
  })

  it('returns the empty payload when the result has no linkage at all', async () => {
    const p = await buildFavoritePayload({}, neverResolver)
    expect(p).toEqual({ infoHash: '', magnet: '', source: 'none' })
  })
})
