import { describe, it, expect } from 'vitest'
import { scrollKey, clampScroll } from './useScrollRestoration'

describe('scrollKey', () => {
  it('uses the location.key per history entry when available', () => {
    expect(scrollKey('abc123', '/downloads')).toBe('jackui.scroll:abc123')
  })
  it('falls back to the pathname when the key is "default" (first entry / reload)', () => {
    expect(scrollKey('default', '/downloads')).toBe('jackui.scroll:path:/downloads')
  })
  it('falls back to the pathname when the key is empty', () => {
    expect(scrollKey('', '/library')).toBe('jackui.scroll:path:/library')
  })
})

describe('clampScroll', () => {
  it('keeps the target when it fits the document', () => {
    expect(clampScroll(300, 1000)).toBe(300)
  })
  it('clamps to the max scrollable when the content is shorter', () => {
    expect(clampScroll(900, 400)).toBe(400)
  })
  it('never returns negative (document smaller than the viewport)', () => {
    expect(clampScroll(900, -50)).toBe(0)
  })
})
