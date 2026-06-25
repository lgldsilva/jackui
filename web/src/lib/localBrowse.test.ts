import { describe, it, expect } from 'vitest'
import { localBrowseTarget, localBrowseHref } from './localBrowse'
import type { LocalMount } from '../api/client'

const mounts: LocalMount[] = [
  { name: 'Meus downloads', path: '/mnt/storage/jacktrack/download', userSubpath: true },
  { name: 'Filmes', path: '/mnt/storage/movies' },
]

describe('localBrowseTarget', () => {
  it('opens the folder containing a single file, stripping the per-user subdir', () => {
    const t = localBrowseTarget('/mnt/storage/jacktrack/download/admin/Movie/Movie.mkv', mounts, 'admin')
    expect(t).toEqual({ mount: 'Meus downloads', path: 'Movie' })
  })

  it('opens the torrent folder itself for a whole-torrent download (targetIsDir)', () => {
    const t = localBrowseTarget('/mnt/storage/jacktrack/download/admin/Pack', mounts, 'admin', true)
    expect(t).toEqual({ mount: 'Meus downloads', path: 'Pack' })
  })

  it('does not strip a subdir that merely starts with the username substring', () => {
    const t = localBrowseTarget('/mnt/storage/jacktrack/download/adminstuff/x.mkv', mounts, 'admin')
    expect(t).toEqual({ mount: 'Meus downloads', path: 'adminstuff' })
  })

  it('handles a non-UserSubpath mount (no stripping)', () => {
    const t = localBrowseTarget('/mnt/storage/movies/Dune/Dune.mkv', mounts, 'admin')
    expect(t).toEqual({ mount: 'Filmes', path: 'Dune' })
  })

  it('returns null when the path is not under any mount', () => {
    expect(localBrowseTarget('/data/streams/x.mkv', mounts, 'admin')).toBeNull()
    expect(localBrowseTarget('', mounts, 'admin')).toBeNull()
  })
})

describe('localBrowseHref', () => {
  it('builds an encoded LocalPage deep-link', () => {
    expect(localBrowseHref('/mnt/storage/jacktrack/download/admin/Movie/Movie.mkv', mounts, 'admin'))
      .toBe('/local?mount=Meus%20downloads&path=Movie')
  })

  it('returns null when unresolvable', () => {
    expect(localBrowseHref('/elsewhere/x.mkv', mounts, 'admin')).toBeNull()
  })
})
