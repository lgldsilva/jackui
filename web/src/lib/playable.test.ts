import { describe, it, expect } from 'vitest'
import { detectKind } from './playable'

describe('detectKind fallback (Cinema/Música preference)', () => {
  it('clear signals ignore the fallback', () => {
    // Obvious video by ext/hint → always video, even with audio fallback.
    expect(detectKind('Movie.2024.1080p.x264.mkv', 0, 'audio')).toBe('video')
    // Obvious audio by ext → always audio, even with video fallback.
    expect(detectKind('track.flac', 0, 'video')).toBe('audio')
  })

  it('ambiguous titles follow the fallback', () => {
    const ambiguous = 'Some Live Session 2024'
    expect(detectKind(ambiguous, 0, 'audio')).toBe('audio')
    expect(detectKind(ambiguous, 0, 'video')).toBe('video')
  })

  it('defaults to video when no fallback is passed (legacy behaviour)', () => {
    expect(detectKind('Some Live Session 2024')).toBe('video')
  })
})
