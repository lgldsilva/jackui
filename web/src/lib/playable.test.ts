import { describe, it, expect } from 'vitest'
import { detectKind, isAudioResult } from './playable'
import type { SearchResult } from '../api/client'

const mkResult = (over: Partial<SearchResult>): SearchResult => ({
  title: '', tracker: '', categoryId: 0, category: '', size: 0,
  seeders: 0, leechers: 0, age: '', magnetUri: '', link: '',
  infoHash: '', publishDate: '', ...over,
})

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

describe('isAudioResult (music-mode search filter)', () => {
  it('prefers the backend-resolved mediaKind over the title', () => {
    expect(isAudioResult(mkResult({ mediaKind: 'audio', title: 'Movie.2024.1080p.mkv' }))).toBe(true)
    expect(isAudioResult(mkResult({ mediaKind: 'video', title: 'artist - album.flac' }))).toBe(false)
  })

  it('falls back to the heuristic when mediaKind is other/absent', () => {
    expect(isAudioResult(mkResult({ title: 'Pink Floyd - The Wall [FLAC]' }))).toBe(true)
    expect(isAudioResult(mkResult({ categoryId: 3010, title: 'whatever' }))).toBe(true)
    expect(isAudioResult(mkResult({ mediaKind: 'other', title: 'Movie.2024.1080p.x264' }))).toBe(false)
  })
})
