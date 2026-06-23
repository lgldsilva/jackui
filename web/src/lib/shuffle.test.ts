import { describe, it, expect } from 'vitest'
import { shuffledOrder } from './shuffle'

describe('shuffledOrder', () => {
  it('devolve uma permutação completa de [0..n-1]', () => {
    const out = shuffledOrder(10, 3)
    expect(out).toHaveLength(10)
    expect([...out].sort((a, b) => a - b)).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 8, 9])
  })

  it('fixa startIndex na posição 0', () => {
    for (let start = 0; start < 6; start++) {
      expect(shuffledOrder(6, start)[0]).toBe(start)
    }
  })

  it('não repete nenhum índice (bag shuffle)', () => {
    const out = shuffledOrder(50, 0)
    expect(new Set(out).size).toBe(out.length)
  })

  it('n===1 → [start]', () => {
    expect(shuffledOrder(1, 0)).toEqual([0])
  })

  it('n===0 → []', () => {
    expect(shuffledOrder(0, 0)).toEqual([])
  })

  it('startIndex inválido (-1) não entra na permutação', () => {
    const out = shuffledOrder(5, -1)
    expect(out).toHaveLength(5)
    expect([...out].sort((a, b) => a - b)).toEqual([0, 1, 2, 3, 4])
    expect(out).not.toContain(-1)
  })
})
