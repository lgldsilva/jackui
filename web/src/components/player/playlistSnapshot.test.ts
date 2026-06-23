import { describe, it, expect, beforeEach, vi } from 'vitest'
import { savePlaylistSnapshot, loadPlaylistSnapshot, clearPlaylistSnapshot, snapshotIndexOfHash } from './playlistSnapshot'
import type { PlaylistItem } from '../../api/client'
import { save } from '../../lib/storage'

// O ambiente node do vitest não tem localStorage — polyfill mínimo em memória.
const store = new Map<string, string>()
const mockStorage: Storage = {
  getItem: k => store.get(k) ?? null,
  setItem: (k, v) => { store.set(k, String(v)) },
  removeItem: k => { store.delete(k) },
  clear: () => store.clear(),
  key: i => Array.from(store.keys())[i] ?? null,
  get length() { return store.size },
}
vi.stubGlobal('localStorage', mockStorage)

const mkItem = (infoHash: string, title: string, position: number): PlaylistItem => ({
  id: position, playlistId: 0, position, title,
  magnet: `magnet:?xt=urn:btih:${infoHash}`, infoHash, fileIndex: 0, addedAt: '',
})

const items = [
  mkItem('a'.repeat(40), 'One', 0),
  mkItem('b'.repeat(40), 'Two', 1),
  mkItem('c'.repeat(40), 'Three', 2),
]

describe('playlistSnapshot', () => {
  beforeEach(() => { clearPlaylistSnapshot() })

  it('round-trips save → load', () => {
    savePlaylistSnapshot('My List', items, 1)
    const snap = loadPlaylistSnapshot()
    expect(snap?.name).toBe('My List')
    expect(snap?.items.length).toBe(3)
    expect(snap?.currentItemIndex).toBe(1)
  })

  it('returns null when nothing was saved', () => {
    expect(loadPlaylistSnapshot()).toBeNull()
  })

  it('does not persist an empty playlist', () => {
    savePlaylistSnapshot('Empty', [], 0)
    expect(loadPlaylistSnapshot()).toBeNull()
  })

  it('finds the index of a hash, -1 when absent', () => {
    savePlaylistSnapshot('My List', items, 0)
    const snap = loadPlaylistSnapshot()!
    expect(snapshotIndexOfHash(snap, 'b'.repeat(40))).toBe(1)
    expect(snapshotIndexOfHash(snap, 'z'.repeat(40))).toBe(-1)
  })

  it('expires snapshots older than the TTL', () => {
    // Grava direto via storage com savedAt antigo (8 dias) — load deve descartar.
    save('player.playlistSnapshot', { name: 'Old', items, currentItemIndex: 0, savedAt: Date.now() - 8 * 24 * 60 * 60 * 1000 })
    expect(loadPlaylistSnapshot()).toBeNull()
  })

  it('clear removes the snapshot', () => {
    savePlaylistSnapshot('My List', items, 0)
    clearPlaylistSnapshot()
    expect(loadPlaylistSnapshot()).toBeNull()
  })
})
