import { describe, it, expect } from 'vitest'
import { EQ_PRESETS, activePresetKey } from './eqPresets'

describe('EQ_PRESETS', () => {
  it('every preset has exactly 10 bands', () => {
    for (const p of EQ_PRESETS) expect(p.gains).toHaveLength(10)
  })
  it('includes a flat preset of all zeros', () => {
    const flat = EQ_PRESETS.find((p) => p.key === 'flat')
    expect(flat?.gains).toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0])
  })
})

describe('activePresetKey', () => {
  it('matches an exact preset curve', () => {
    const rock = EQ_PRESETS.find((p) => p.key === 'rock')!
    expect(activePresetKey(rock.gains)).toBe('rock')
  })
  it('returns flat for all-zero gains', () => {
    expect(activePresetKey([0, 0, 0, 0, 0, 0, 0, 0, 0, 0])).toBe('flat')
  })
  it('returns null for a custom curve', () => {
    expect(activePresetKey([5, 0, 0, 0, 0, 0, 0, 0, 0, 7])).toBeNull()
  })
})
