import { describe, it, expect } from 'vitest'
import { mergeQuery, pickEnum } from './useQueryState'

// Parse a query string back into a map for order-independent assertions.
const parse = (qs: string) => Object.fromEntries(new URLSearchParams(qs))

describe('mergeQuery', () => {
  it('define uma chave nova preservando as existentes (inclui ?play=)', () => {
    const out = parse(mergeQuery('play=abc&f=2', { tab: 'paused' }))
    expect(out).toEqual({ play: 'abc', f: '2', tab: 'paused' })
  })

  it('overwrites an existing key', () => {
    expect(parse(mergeQuery('tab=all', { tab: 'paused' }))).toEqual({ tab: 'paused' })
  })

  it('removes the key when the value is empty or null (clean URL)', () => {
    expect(parse(mergeQuery('tab=all&play=x', { tab: '' }))).toEqual({ play: 'x' })
    expect(mergeQuery('tab=all', { tab: null })).toBe('')
  })

  it('applies several keys at once, set and delete together', () => {
    const out = parse(mergeQuery('play=keep&old=1', { q: 'foo', old: null }))
    expect(out).toEqual({ play: 'keep', q: 'foo' })
  })

  it('never touches ?play= when changing another key', () => {
    expect(parse(mergeQuery('play=HASH&f=3&t=10', { status: 'completed' })).play).toBe('HASH')
  })
})

describe('pickEnum', () => {
  const tabs = ['all', 'paused', 'completed'] as const
  it('returns the value when it is allowed', () => {
    expect(pickEnum('paused', tabs, 'all')).toBe('paused')
  })
  it('falls back for an invalid or missing value', () => {
    expect(pickEnum('garbage', tabs, 'all')).toBe('all')
    expect(pickEnum(null, tabs, 'all')).toBe('all')
    expect(pickEnum('', tabs, 'all')).toBe('all')
  })
})
