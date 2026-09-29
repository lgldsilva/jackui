import { describe, it, expect } from 'vitest'
import { matchesEntryStatus } from './localFilter'
import type { LocalEntry } from '../api/client'

const mk = (extra: Partial<LocalEntry>): LocalEntry => ({
  name: 'x', path: 'x', isDir: false, size: 0, modTime: '2026-01-01',
  isPlayable: false, ...extra,
})

describe('matchesEntryStatus', () => {
  it("'all' aceita qualquer entrada", () => {
    expect(matchesEntryStatus(mk({ incomplete: true }), 'all')).toBe(true)
    expect(matchesEntryStatus(mk({ incomplete: false }), 'all')).toBe(true)
    expect(matchesEntryStatus(mk({}), 'all')).toBe(true)
  })

  it("'downloading' accepts only incomplete", () => {
    expect(matchesEntryStatus(mk({ incomplete: true }), 'downloading')).toBe(true)
    expect(matchesEntryStatus(mk({ incomplete: false }), 'downloading')).toBe(false)
    expect(matchesEntryStatus(mk({}), 'downloading')).toBe(false) // absent = complete
  })

  it("'done' accepts only complete (falsy incomplete)", () => {
    expect(matchesEntryStatus(mk({ incomplete: true }), 'done')).toBe(false)
    expect(matchesEntryStatus(mk({ incomplete: false }), 'done')).toBe(true)
    expect(matchesEntryStatus(mk({}), 'done')).toBe(true)
  })

  it('applies equally to folders (a folder with a .part is incomplete)', () => {
    expect(matchesEntryStatus(mk({ isDir: true, incomplete: true }), 'downloading')).toBe(true)
    expect(matchesEntryStatus(mk({ isDir: true, incomplete: true }), 'done')).toBe(false)
    expect(matchesEntryStatus(mk({ isDir: true }), 'done')).toBe(true)
  })
})
