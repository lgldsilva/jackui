import { describe, it, expect } from 'vitest'
import { sortFavorites } from './favSort'
import type { StreamFavorite as Fav } from '../api/client'

const mk = (over: Partial<Fav>): Fav => ({
  name: 'x', infoHash: '', magnet: '', userId: 0,
  favoritedAt: '2024-01-01T00:00:00Z', reason: 'manual', folderId: null,
  ...over,
})

describe('sortFavorites', () => {
  const a = mk({ name: 'Banana', favoritedAt: '2024-01-01T00:00:00Z', seeders: 5, totalSize: 100 })
  const b = mk({ name: 'apple', favoritedAt: '2024-03-01T00:00:00Z', seeders: 20, totalSize: 50 })
  const c = mk({ name: 'cherry', favoritedAt: '2024-02-01T00:00:00Z' }) // unknown seeds/size
  const list = [a, b, c]

  it('sorts by date desc (most recent first)', () => {
    expect(sortFavorites(list, 'date', 'desc').map(f => f.name)).toEqual(['apple', 'cherry', 'Banana'])
  })

  it('sorts by date asc', () => {
    expect(sortFavorites(list, 'date', 'asc').map(f => f.name)).toEqual(['Banana', 'cherry', 'apple'])
  })

  it('sorts by name case-insensitively (localeCompare)', () => {
    expect(sortFavorites(list, 'name', 'asc').map(f => f.name)).toEqual(['apple', 'Banana', 'cherry'])
  })

  it('sorts by seeds desc, unknown last', () => {
    expect(sortFavorites(list, 'seeds', 'desc').map(f => f.name)).toEqual(['apple', 'Banana', 'cherry'])
  })

  it('sorts by size desc, unknown last', () => {
    expect(sortFavorites(list, 'size', 'desc').map(f => f.name)).toEqual(['Banana', 'apple', 'cherry'])
  })

  it('does not mutate the input array', () => {
    const input = [a, b, c]
    sortFavorites(input, 'name', 'asc')
    expect(input.map(f => f.name)).toEqual(['Banana', 'apple', 'cherry'])
  })
})
