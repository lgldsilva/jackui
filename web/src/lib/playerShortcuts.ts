// Pure interpreter for player keyboard shortcuts. The hook applies the action
// to the media element; this file stays DOM-free so the mapping is unit-tested.

export type ShortcutAction =
  | { readonly kind: 'toggle' }
  | { readonly kind: 'seek'; readonly delta: number }
  | { readonly kind: 'seekToFraction'; readonly fraction: number }
  | { readonly kind: 'volume'; readonly delta: number }
  | { readonly kind: 'mute' }
  | { readonly kind: 'fullscreen' }
  | { readonly kind: 'next' }
  | { readonly kind: 'prev' }
  | { readonly kind: 'skipIntro' }
  | { readonly kind: 'pip' }
  | { readonly kind: 'speed'; readonly delta: number }

const DIGIT = /^[0-9]$/

export function interpretPlayerKey(key: string): ShortcutAction | null {
  switch (key) {
    case ' ':
    case 'k':
    case 'K':
      return { kind: 'toggle' }
    case 'ArrowRight':
    case 'l':
    case 'L':
      return { kind: 'seek', delta: 10 }
    case 'ArrowLeft':
    case 'j':
    case 'J':
      return { kind: 'seek', delta: -10 }
    case 'ArrowUp':
      return { kind: 'volume', delta: 0.1 }
    case 'ArrowDown':
      return { kind: 'volume', delta: 0.1 * -1 }
    case 'm':
    case 'M':
      return { kind: 'mute' }
    case 'f':
    case 'F':
      return { kind: 'fullscreen' }
    case 'n':
    case 'N':
      return { kind: 'next' }
    case 'p':
    case 'P':
      return { kind: 'prev' }
    case 'i':
    case 'I':
      return { kind: 'skipIntro' }
    case 'w':
    case 'W':
      return { kind: 'pip' }
    case '>':
    case '.':
      return { kind: 'speed', delta: 1 }
    case '<':
    case ',':
      return { kind: 'speed', delta: -1 }
    default:
      if (DIGIT.test(key)) return { kind: 'seekToFraction', fraction: Number(key) / 10 }
      return null
  }
}

export function nextSpeed(current: number, delta: number, options: readonly number[]): number {
  if (options.length === 0) return current
  let idx = 0
  let best = Math.abs(options[0] - current)
  for (let i = 1; i < options.length; i++) {
    const d = Math.abs(options[i] - current)
    if (d < best) { best = d; idx = i }
  }
  const next = idx + delta
  if (next < 0) return options[0]
  if (next >= options.length) return options[options.length - 1]
  return options[next]
}
