import { describe, it, expect } from 'vitest'
import { scrollKey, clampScroll } from './useScrollRestoration'

describe('scrollKey', () => {
  it('usa a location.key por entrada de histórico quando disponível', () => {
    expect(scrollKey('abc123', '/downloads')).toBe('jackui.scroll:abc123')
  })
  it('cai no pathname quando a key é "default" (primeira entrada / reload)', () => {
    expect(scrollKey('default', '/downloads')).toBe('jackui.scroll:path:/downloads')
  })
  it('cai no pathname quando a key é vazia', () => {
    expect(scrollKey('', '/library')).toBe('jackui.scroll:path:/library')
  })
})

describe('clampScroll', () => {
  it('mantém o alvo quando cabe no documento', () => {
    expect(clampScroll(300, 1000)).toBe(300)
  })
  it('limita ao máximo rolável quando o conteúdo é mais curto', () => {
    expect(clampScroll(900, 400)).toBe(400)
  })
  it('nunca retorna negativo (documento menor que a viewport)', () => {
    expect(clampScroll(900, -50)).toBe(0)
  })
})
