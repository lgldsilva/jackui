import { describe, it, expect } from 'vitest'
import {
  splitTargetPath, buildEditableRows, rowTargetPath, rowIsEdited,
  buildOverrides, selectedPaths, categoryOptions, type ReclassifyRow,
} from './rows'
import type { PromotePreviewEntry } from '../../api/client'

const movie: PromotePreviewEntry = {
  path: 'raw.mkv',
  originalName: 'raw.mkv',
  cleanName: 'Inception',
  targetPath: 'Movies/Inception (2010)/Inception (2010).mkv',
  kind: 'movie',
  year: 2010,
}

const tv: PromotePreviewEntry = {
  path: 'show.s01e02.mkv',
  originalName: 'show.s01e02.mkv',
  cleanName: 'The Show',
  targetPath: 'Series/The Show/Season 01/The Show - S01E02.mkv',
  kind: 'tv',
}

describe('splitTargetPath', () => {
  it('splits a movie path into category/middle/name', () => {
    expect(splitTargetPath('Movies/Inception (2010)/Inception (2010).mkv')).toEqual({
      category: 'Movies',
      middle: ['Inception (2010)'],
      finalName: 'Inception (2010).mkv',
    })
  })

  it('keeps the TV middle segments (show + season)', () => {
    expect(splitTargetPath('Series/The Show/Season 01/The Show - S01E02.mkv')).toEqual({
      category: 'Series',
      middle: ['The Show', 'Season 01'],
      finalName: 'The Show - S01E02.mkv',
    })
  })

  it('treats a single segment as a bare filename (no category)', () => {
    expect(splitTargetPath('movie.mkv')).toEqual({ category: '', middle: [], finalName: 'movie.mkv' })
  })

  it('is defensive against empty input', () => {
    expect(splitTargetPath('')).toEqual({ category: '', middle: [], finalName: '' })
  })
})

describe('buildEditableRows', () => {
  it('builds selected rows from successful previews', () => {
    const rows = buildEditableRows([movie, tv])
    expect(rows).toHaveLength(2)
    expect(rows[0]).toMatchObject({
      path: 'raw.mkv', category: 'Movies', finalName: 'Inception (2010).mkv',
      selected: true, kind: 'movie',
    })
    expect(rows[1].middle).toEqual(['The Show', 'Season 01'])
  })

  it('renders an errored preview as an unselected row', () => {
    const rows = buildEditableRows([{ ...movie, error: 'boom' }])
    expect(rows[0].selected).toBe(false)
    expect(rows[0].error).toBe('boom')
  })

  it('falls back to originalName when path/finalName missing', () => {
    const rows = buildEditableRows([{ originalName: 'orphan.mkv', cleanName: '', targetPath: '', kind: 'movie' }])
    expect(rows[0].path).toBe('orphan.mkv')
    expect(rows[0].finalName).toBe('orphan.mkv')
  })
})

describe('rowTargetPath', () => {
  it('rebuilds the path from edited fields, preserving middle', () => {
    const rows = buildEditableRows([tv])
    rows[0].category = 'Anime'
    rows[0].finalName = 'The Show - S01E02 - Pilot.mkv'
    expect(rowTargetPath(rows[0])).toBe('Anime/The Show/Season 01/The Show - S01E02 - Pilot.mkv')
  })

  it('drops a cleared category (lands at base root)', () => {
    expect(rowTargetPath({ category: '  ', middle: [], finalName: 'x.mkv' })).toBe('x.mkv')
  })

  it('trims surrounding whitespace per segment', () => {
    expect(rowTargetPath({ category: ' Movies ', middle: [' Dir '], finalName: ' f.mkv ' }))
      .toBe('Movies/Dir/f.mkv')
  })
})

describe('rowIsEdited', () => {
  it('is false when untouched', () => {
    const rows = buildEditableRows([movie])
    expect(rowIsEdited(rows[0], movie.targetPath)).toBe(false)
  })

  it('is true after editing the category', () => {
    const rows = buildEditableRows([movie])
    rows[0].category = 'Filmes'
    expect(rowIsEdited(rows[0], movie.targetPath)).toBe(true)
  })
})

describe('buildOverrides', () => {
  const orig = { 'raw.mkv': movie.targetPath, 'show.s01e02.mkv': tv.targetPath }

  it('emits an entry only for selected + edited rows', () => {
    const rows = buildEditableRows([movie, tv])
    rows[0].finalName = 'Inception.mkv' // edited
    // tv left untouched → no override
    expect(buildOverrides(rows, orig)).toEqual({
      'raw.mkv': 'Movies/Inception (2010)/Inception.mkv',
    })
  })

  it('skips unselected rows even if edited', () => {
    const rows = buildEditableRows([movie])
    rows[0].category = 'Filmes'
    rows[0].selected = false
    expect(buildOverrides(rows, orig)).toEqual({})
  })

  it('skips errored rows', () => {
    const rows = buildEditableRows([{ ...movie, error: 'x' }])
    rows[0].selected = true
    rows[0].category = 'Filmes'
    expect(buildOverrides(rows, orig)).toEqual({})
  })

  it('skips a row whose target became empty', () => {
    // A bare-filename preview (no category/middle) cleared to nothing → no entry.
    const bare = buildEditableRows([{ path: 'bare.mkv', originalName: 'bare.mkv', cleanName: '', targetPath: 'bare.mkv', kind: 'movie' }])
    bare[0].finalName = '   '
    expect(buildOverrides(bare, { 'bare.mkv': 'bare.mkv' })).toEqual({})
  })
})

describe('selectedPaths', () => {
  it('returns selected, non-errored source paths', () => {
    const rows: ReclassifyRow[] = buildEditableRows([movie, tv, { ...movie, path: 'err.mkv', error: 'x' }])
    rows[1].selected = false
    expect(selectedPaths(rows)).toEqual(['raw.mkv'])
  })
})

describe('categoryOptions', () => {
  it('unions dest folders + row categories, deduped case-insensitively', () => {
    const rows = buildEditableRows([movie, tv])
    expect(categoryOptions(['Movies', 'movies', 'Anime'], rows))
      .toEqual(['Anime', 'Movies', 'Series'])
  })

  it('handles empty inputs', () => {
    expect(categoryOptions([], [])).toEqual([])
  })
})
