import { describe, it, expect } from 'vitest'
import { formatHlsLevel, levelsFromHls } from './HlsQualityMenu'
import type Hls from 'hls.js'

describe('formatHlsLevel', () => {
  it('prefers height, then bitrate', () => {
    expect(formatHlsLevel({ index: 0, height: 720, bitrate: 3_000_000 })).toBe('720p')
    expect(formatHlsLevel({ index: 1, height: 0, bitrate: 800_000 })).toBe('800 kbps')
    expect(formatHlsLevel({ index: 2, height: 0, bitrate: 0 })).toBe('#2')
  })
})

describe('levelsFromHls', () => {
  it('maps hls.levels', () => {
    const hls = { levels: [{ height: 480, bitrate: 1 }, { height: 1080, bitrate: 5 }] } as unknown as Hls
    expect(levelsFromHls(hls)).toEqual([
      { index: 0, height: 480, bitrate: 1 },
      { index: 1, height: 1080, bitrate: 5 },
    ])
  })
})
