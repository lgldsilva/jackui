import { describe, it, expect } from 'vitest'
import { reorder } from './reorder'

describe('reorder', () => {
  it('moves an item to the right', () => {
    expect(reorder(['a', 'b', 'c', 'd'], 0, 2)).toEqual(['b', 'c', 'a', 'd'])
  })
  it('moves an item to the left', () => {
    expect(reorder(['a', 'b', 'c', 'd'], 3, 1)).toEqual(['a', 'd', 'b', 'c'])
  })
  it('is a no-op when from === to', () => {
    expect(reorder(['a', 'b', 'c'], 1, 1)).toEqual(['a', 'b', 'c'])
  })
  it('returns a NEW array (does not mutate the input)', () => {
    const input = ['a', 'b', 'c']
    const out = reorder(input, 0, 2)
    expect(out).not.toBe(input)
    expect(input).toEqual(['a', 'b', 'c'])
  })
  it('ignores out-of-range indices (returns a copy)', () => {
    expect(reorder(['a', 'b'], 5, 0)).toEqual(['a', 'b'])
    expect(reorder(['a', 'b'], 0, -1)).toEqual(['a', 'b'])
  })
  it('moves the last item to the front', () => {
    expect(reorder(['a', 'b', 'c'], 2, 0)).toEqual(['c', 'a', 'b'])
  })
})
