import { describe, it, expect } from 'vitest'
import { interpretPlayerKey, nextSpeed } from './playerShortcuts'

describe('interpretPlayerKey', () => {
  it('maps YouTube/Netflix-style keys', () => {
    expect(interpretPlayerKey(' ')).toEqual({ kind: 'toggle' })
    expect(interpretPlayerKey('k')).toEqual({ kind: 'toggle' })
    expect(interpretPlayerKey('j')).toEqual({ kind: 'seek', delta: -10 })
    expect(interpretPlayerKey('l')).toEqual({ kind: 'seek', delta: 10 })
    expect(interpretPlayerKey('n')).toEqual({ kind: 'next' })
    expect(interpretPlayerKey('p')).toEqual({ kind: 'prev' })
    expect(interpretPlayerKey('i')).toEqual({ kind: 'skipIntro' })
    expect(interpretPlayerKey('w')).toEqual({ kind: 'pip' })
    expect(interpretPlayerKey('5')).toEqual({ kind: 'seekToFraction', fraction: 0.5 })
    expect(interpretPlayerKey('0')).toEqual({ kind: 'seekToFraction', fraction: 0 })
    expect(interpretPlayerKey('.')).toEqual({ kind: 'speed', delta: 1 })
    expect(interpretPlayerKey(',')).toEqual({ kind: 'speed', delta: -1 })
  })
  it('ignores unknown keys', () => {
    expect(interpretPlayerKey('x')).toBeNull()
    expect(interpretPlayerKey('Escape')).toBeNull()
  })
})

describe('nextSpeed', () => {
  const opts = [0.75, 1, 1.25, 1.5, 2]
  it('steps along the ladder', () => {
    expect(nextSpeed(1, 1, opts)).toBe(1.25)
    expect(nextSpeed(1, -1, opts)).toBe(0.75)
  })
  it('clamps at the ends', () => {
    expect(nextSpeed(0.75, -1, opts)).toBe(0.75)
    expect(nextSpeed(2, 1, opts)).toBe(2)
  })
})
