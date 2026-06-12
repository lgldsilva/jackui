import { describe, it, expect } from 'vitest'
import { mergePromoteFiles } from './localPromote'
import type { LocalEntry } from '../api/client'

const file = (path: string, extra: Partial<LocalEntry> = {}): LocalEntry => ({
  name: path.split('/').pop() || path,
  path,
  isDir: false,
  size: 100,
  modTime: '2026-01-01T00:00:00Z',
  isPlayable: true,
  ...extra,
})
const dir = (path: string): LocalEntry => file(path, { isDir: true, isPlayable: false, size: 0 })

describe('mergePromoteFiles', () => {
  it('returns loose files unchanged when no folders were walked', () => {
    const loose = [file('a.mkv'), file('b.mp4')]
    expect(mergePromoteFiles(loose, [])).toEqual(loose)
  })

  it('includes files discovered inside selected folders', () => {
    const loose = [file('top.mkv')]
    const walked = [file('Show/S01/ep1.mkv'), file('Show/S01/ep2.mkv')]
    const out = mergePromoteFiles(loose, walked)
    expect(out.map((e) => e.path)).toEqual(['top.mkv', 'Show/S01/ep1.mkv', 'Show/S01/ep2.mkv'])
  })

  it('deduplicates by path — a file selected loosely AND inside a folder appears once', () => {
    const loose = [file('Show/ep1.mkv')]
    const walked = [file('Show/ep1.mkv'), file('Show/ep2.mkv')]
    const out = mergePromoteFiles(loose, walked)
    expect(out.map((e) => e.path)).toEqual(['Show/ep1.mkv', 'Show/ep2.mkv'])
  })

  it('keeps the loose entry on a path collision (loose wins)', () => {
    const loose = [file('x.mkv', { size: 999 })]
    const walked = [file('x.mkv', { size: 1 })]
    const out = mergePromoteFiles(loose, walked)
    expect(out).toHaveLength(1)
    expect(out[0].size).toBe(999)
  })

  it('drops directory entries that slip through', () => {
    const loose = [dir('Folder'), file('a.mkv')]
    const walked = [dir('Folder/Sub'), file('Folder/a.mkv')]
    const out = mergePromoteFiles(loose, walked)
    expect(out.every((e) => !e.isDir)).toBe(true)
    expect(out.map((e) => e.path).sort()).toEqual(['Folder/a.mkv', 'a.mkv'])
  })

  it('returns empty when nothing but directories were selected and none had media', () => {
    expect(mergePromoteFiles([dir('Empty')], [])).toEqual([])
  })
})
