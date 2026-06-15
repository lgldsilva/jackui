import { describe, it, expect } from 'vitest'
import { nextReveal } from './useIncrementalReveal'

describe('nextReveal', () => {
  it('revela mais um lote', () => {
    expect(nextReveal(100, 100, 500)).toBe(200)
    expect(nextReveal(0, 50, 500)).toBe(50)
  })
  it('não passa do total', () => {
    expect(nextReveal(450, 100, 500)).toBe(500)
    expect(nextReveal(500, 100, 500)).toBe(500)
  })
  it('total menor que o lote → para no total', () => {
    expect(nextReveal(0, 100, 30)).toBe(30)
  })
})
