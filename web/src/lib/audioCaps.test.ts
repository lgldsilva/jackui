import { describe, it, expect } from 'vitest'
import { computeAudioCaps } from './audioCaps'

describe('computeAudioCaps', () => {
  it('advertises codecs the browser reports as playable', () => {
    const canPlay = (t: string) =>
      t.includes('flac') ? 'probably' : t.includes('opus') ? 'maybe' : ''
    const caps = computeAudioCaps(canPlay)
    expect(caps).toContain('flac')
    expect(caps).toContain('opus')
    expect(caps).not.toContain('vorbis')
    expect(caps).not.toContain('wav')
  })

  it('returns empty when the browser supports none of the extra codecs', () => {
    expect(computeAudioCaps(() => '')).toEqual([])
  })

  it('treats "no" / "" as unsupported', () => {
    expect(computeAudioCaps(() => 'no')).toEqual([])
  })
})
