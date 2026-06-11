import { describe, it, expect } from 'vitest'
import {
  EXTERNAL_PLAYERS,
  availableExternalPlayers,
  resolveExternalPlayer,
  type ExternalPlayerURLs,
} from './externalPlayers'

// A fully-populated set of media URLs (every player applies). Mirrors what
// computeMediaUrls emits when a file is selected: vlc playlist, the scheme
// links, and the absolute direct URL.
const fullURLs: ExternalPlayerURLs = {
  vlcURL: '/api/stream/playlist/abc/0?token=t',
  iinaURL: 'iina://weblink?url=https%3A%2F%2Fhost%2Fapi%2Fstream%2Fabc%2F0%3Ftoken%3Dt',
  infuseURL: 'infuse://x-callback-url/play?url=https%3A%2F%2Fhost%2Fapi%2Fstream%2Fabc%2F0%3Ftoken%3Dt',
  directURL: 'https://host/api/stream/abc/0?token=t',
}

// No file selected ⇒ every URL is empty (computeMediaUrls gates on info+idx).
const emptyURLs: ExternalPlayerURLs = { vlcURL: '', iinaURL: '', infuseURL: '', directURL: '' }

describe('externalPlayers.build (per-player URL wiring)', () => {
  it('each player maps to its own media URL unchanged', () => {
    const byId = Object.fromEntries(EXTERNAL_PLAYERS.map(p => [p.id, p]))
    expect(byId.vlc.build(fullURLs)).toBe(fullURLs.vlcURL)
    expect(byId.iina.build(fullURLs)).toBe(fullURLs.iinaURL)
    expect(byId.infuse.build(fullURLs)).toBe(fullURLs.infuseURL)
    expect(byId.copy.build(fullURLs)).toBe(fullURLs.directURL)
  })

  it('the scheme links keep their proprietary prefixes', () => {
    const byId = Object.fromEntries(EXTERNAL_PLAYERS.map(p => [p.id, p]))
    expect(byId.iina.build(fullURLs).startsWith('iina://weblink?url=')).toBe(true)
    expect(byId.infuse.build(fullURLs).startsWith('infuse://x-callback-url/play?url=')).toBe(true)
  })

  it('copy is a clipboard kind; the rest are links', () => {
    const copy = EXTERNAL_PLAYERS.find(p => p.id === 'copy')!
    expect(copy.kind).toBe('clipboard')
    for (const p of EXTERNAL_PLAYERS.filter(x => x.id !== 'copy')) {
      expect(p.kind).toBe('link')
    }
  })
})

describe('availableExternalPlayers', () => {
  it('shows every player when all URLs are present', () => {
    expect(availableExternalPlayers(fullURLs).map(p => p.id)).toEqual(['vlc', 'iina', 'infuse', 'copy'])
  })

  it('hides players whose URL is empty (matches the old conditional buttons)', () => {
    const noScheme: ExternalPlayerURLs = { ...fullURLs, iinaURL: '', infuseURL: '' }
    expect(availableExternalPlayers(noScheme).map(p => p.id)).toEqual(['vlc', 'copy'])
  })

  it('returns nothing when no file is selected', () => {
    expect(availableExternalPlayers(emptyURLs)).toEqual([])
  })

  it('preserves the catalogue order (VLC, IINA, Infuse, then Copy)', () => {
    expect(availableExternalPlayers(fullURLs).map(p => p.id)).toEqual(EXTERNAL_PLAYERS.map(p => p.id))
  })
})

describe('resolveExternalPlayer (remembered choice)', () => {
  it('returns the remembered player when it is still available', () => {
    expect(resolveExternalPlayer(fullURLs, 'infuse')?.id).toBe('infuse')
  })

  it('falls back to the first available when no preference is set', () => {
    expect(resolveExternalPlayer(fullURLs, null)?.id).toBe('vlc')
  })

  it('falls back to the first available when the remembered player vanished', () => {
    // User last picked IINA, but now there's no scheme link (e.g. nothing
    // selected for direct-play) — pick the first that survived.
    const noScheme: ExternalPlayerURLs = { ...fullURLs, iinaURL: '', infuseURL: '' }
    expect(resolveExternalPlayer(noScheme, 'iina')?.id).toBe('vlc')
  })

  it('returns null when there is nothing playable', () => {
    expect(resolveExternalPlayer(emptyURLs, 'vlc')).toBeNull()
  })

  it('ignores an unknown remembered id', () => {
    expect(resolveExternalPlayer(fullURLs, 'mxplayer')?.id).toBe('vlc')
  })
})
