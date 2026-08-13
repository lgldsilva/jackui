import { describe, it, expect, vi } from 'vitest'
import { parseFileIndex, parsePositiveInt, applyPlayHash, type PlayUrlDeps } from './providerHelpers'

function deps(over: Partial<PlayUrlDeps> = {}): PlayUrlDeps {
  return {
    playSingle: vi.fn(),
    playPlaylist: vi.fn(),
    close: vi.fn(),
    hasCurrent: false,
    loadSnapshot: () => null,
    isLocalHash: () => false,
    parseLocalHash: () => null,
    setLastSynced: vi.fn(),
    ...over,
  }
}

describe('parseFileIndex', () => {
  it('keeps 0 (first file in a season pack)', () => {
    expect(parseFileIndex('0')).toBe(0)
  })
  it('parses a positive index', () => {
    expect(parseFileIndex('3')).toBe(3)
  })
  it('treats missing, negative, and garbage as unset', () => {
    expect(parseFileIndex(null)).toBeUndefined()
    expect(parseFileIndex('')).toBeUndefined()
    expect(parseFileIndex('-1')).toBeUndefined()
    expect(parseFileIndex('nope')).toBeUndefined()
  })
})

describe('parsePositiveInt still rejects 0', () => {
  it('does not treat file-index 0 as a seek/count value', () => {
    expect(parsePositiveInt('0')).toBeUndefined()
    expect(parsePositiveInt('4')).toBe(4)
  })
})

describe('applyPlayHash file index', () => {
  it('passes f=0 through to playSingle on a local deep-link', () => {
    const d = deps({
      isLocalHash: () => true,
      parseLocalHash: () => ({ path: 'Season/E01.mkv' }),
    })
    applyPlayHash('local-abc', '0', '12.5', d)
    expect(d.playSingle).toHaveBeenCalledWith(expect.objectContaining({ infoHash: 'local-abc' }), 0, 12.5, true)
  })
  it('omits the file index when f is missing', () => {
    const d = deps({
      isLocalHash: () => true,
      parseLocalHash: () => ({ path: 'movie.mkv' }),
    })
    applyPlayHash('local-abc', null, null, d)
    expect(d.playSingle).toHaveBeenCalledWith(expect.anything(), undefined, undefined, true)
  })
})
