import type { MediaChapter } from '../api/stream-types'

// Titles that mean "the opening I want to skip", in EN/PT and common release tags.
const INTRO_NAME = /\b(intro|opening|op\b|cold\s*open|abertura|vinheta|opening\s*credits)\b/i

export type IntroSkip = {
  readonly startSec: number
  readonly endSec: number
}

// findIntroSkip returns the first chapter that looks like an intro/opening
// and has a usable end. Credits at the end of the file are ignored (start
// after the halfway point) so we don't offer "skip intro" on the outro.
export function findIntroSkip(chapters: readonly MediaChapter[] | undefined): IntroSkip | null {
  if (!chapters || chapters.length === 0) return null
  let fileEnd = 0
  for (const ch of chapters) {
    const end = ch.endSec ?? 0
    if (end > fileEnd) fileEnd = end
  }
  const halfway = fileEnd > 0 ? fileEnd / 2 : Number.POSITIVE_INFINITY
  for (const ch of chapters) {
    const title = ch.title?.trim() ?? ''
    if (!title || !INTRO_NAME.test(title)) continue
    const start = ch.startSec
    const end = ch.endSec ?? 0
    if (!(end > start) || start >= halfway) continue
    // Ignore tiny markers (<3s) and hour-long "intro" mis-tags.
    if (end - start < 3 || end - start > 300) continue
    return { startSec: start, endSec: end }
  }
  return null
}

// shouldShowSkipIntro is true while the playhead is inside the intro and
// there is still more than a second left (so the button doesn't flash at the end).
export function shouldShowSkipIntro(intro: IntroSkip | null, currentTime: number): boolean {
  if (!intro) return false
  return currentTime >= intro.startSec && currentTime < intro.endSec - 1
}
