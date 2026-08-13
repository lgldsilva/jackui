// Up-Next overlay: show a "play the next episode" card in the last seconds
// of a long-enough title. Short clips and audio bumpers stay out.

export const UP_NEXT_WINDOW_SEC = 15
export const UP_NEXT_MIN_DURATION_SEC = 60

export function remainingPlayhead(currentTime: number, duration: number): number {
  if (!Number.isFinite(currentTime) || !Number.isFinite(duration) || duration <= 0) return 0
  return Math.max(0, duration - currentTime)
}

export function shouldShowUpNext(opts: {
  readonly currentTime: number
  readonly duration: number
  readonly hasNext: boolean
  readonly dismissed: boolean
  readonly audioMode?: boolean
}): boolean {
  if (!opts.hasNext || opts.dismissed || opts.audioMode) return false
  if (opts.duration < UP_NEXT_MIN_DURATION_SEC) return false
  const left = remainingPlayhead(opts.currentTime, opts.duration)
  return left > 0 && left <= UP_NEXT_WINDOW_SEC
}

export function upNextCountdown(currentTime: number, duration: number): number {
  return Math.max(0, Math.ceil(remainingPlayhead(currentTime, duration)))
}
