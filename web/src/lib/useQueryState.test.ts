import { describe, it, expect } from 'vitest'
import { mergeQuery, pickEnum } from './useQueryState'

// Parse a query string back into a map for order-independent assertions.
const parse = (qs: string) => Object.fromEntries(new URLSearchParams(qs))

describe('mergeQuery', () => {
  it('define uma chave nova preservando as existentes (inclui ?play=)', () => {
    const out = parse(mergeQuery('play=abc&f=2', { tab: 'paused' }))
    expect(out).toEqual({ play: 'abc', f: '2', tab: 'paused' })
  })

  it('sobrescreve uma chave existente', () => {
    expect(parse(mergeQuery('tab=all', { tab: 'paused' }))).toEqual({ tab: 'paused' })
  })

  it('remove a chave quando o valor é vazio ou null (URL limpa)', () => {
    expect(parse(mergeQuery('tab=all&play=x', { tab: '' }))).toEqual({ play: 'x' })
    expect(mergeQuery('tab=all', { tab: null })).toBe('')
  })

  it('aplica várias chaves de uma vez, set e delete juntos', () => {
    const out = parse(mergeQuery('play=keep&old=1', { q: 'foo', old: null }))
    expect(out).toEqual({ play: 'keep', q: 'foo' })
  })

  it('nunca toca em ?play= ao mexer noutra chave', () => {
    expect(parse(mergeQuery('play=HASH&f=3&t=10', { status: 'completed' })).play).toBe('HASH')
  })
})

describe('pickEnum', () => {
  const tabs = ['all', 'paused', 'completed'] as const
  it('retorna o valor quando é permitido', () => {
    expect(pickEnum('paused', tabs, 'all')).toBe('paused')
  })
  it('cai no fallback para valor inválido ou ausente', () => {
    expect(pickEnum('garbage', tabs, 'all')).toBe('all')
    expect(pickEnum(null, tabs, 'all')).toBe('all')
    expect(pickEnum('', tabs, 'all')).toBe('all')
  })
})
