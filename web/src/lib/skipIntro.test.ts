import { describe, it, expect } from 'vitest'
import { findIntroSkip, shouldShowSkipIntro } from './skipIntro'
import type { MediaChapter } from '../api/stream-types'

function ch(title: string, startSec: number, endSec: number): MediaChapter {
  return { index: 0, title, startSec, endSec }
}

describe('findIntroSkip', () => {
  it('returns null when there are no chapters', () => {
    expect(findIntroSkip(undefined)).toBeNull()
    expect(findIntroSkip([])).toBeNull()
  })

  it('finds an Opening chapter near the start', () => {
    const intro = findIntroSkip([
      ch('Cold Open', 0, 45),
      ch('Act 1', 45, 1200),
    ])
    expect(intro).toEqual({ startSec: 0, endSec: 45 })
  })

  it('matches Portuguese abertura', () => {
    expect(findIntroSkip([ch('Abertura', 8, 92), ch('Episódio', 92, 2400)]))
      .toEqual({ startSec: 8, endSec: 92 })
  })

  it('ignores credits / outro after halfway', () => {
    expect(findIntroSkip([
      ch('Act 1', 0, 2000),
      ch('Opening Credits', 2100, 2180),
    ])).toBeNull()
  })

  it('ignores tiny or huge mis-tagged chapters', () => {
    expect(findIntroSkip([ch('Intro', 0, 2)])).toBeNull()
    expect(findIntroSkip([ch('Intro', 0, 400)])).toBeNull()
  })
})

describe('shouldShowSkipIntro', () => {
  const intro = { startSec: 10, endSec: 70 }
  it('is hidden before the intro and in the last second', () => {
    expect(shouldShowSkipIntro(intro, 9.9)).toBe(false)
    expect(shouldShowSkipIntro(intro, 69.2)).toBe(false)
    expect(shouldShowSkipIntro(null, 20)).toBe(false)
  })
  it('is visible inside the intro', () => {
    expect(shouldShowSkipIntro(intro, 10)).toBe(true)
    expect(shouldShowSkipIntro(intro, 40)).toBe(true)
  })
})
