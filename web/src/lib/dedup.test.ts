import { describe, it, expect } from 'vitest'
import { linkableItems, planAfterLink } from './dedup'
import type { DedupMatch } from '../api/client'

const m = (over: Partial<DedupMatch>): DedupMatch => ({
  fileIndex: 0,
  name: 'f',
  size: 100,
  isVideo: true,
  source: 'library',
  confidence: 'certain',
  ...over,
})

describe('linkableItems', () => {
  it('keeps only mount-backed matches with mount+relPath', () => {
    const items = linkableItems([
      m({ fileIndex: 1, source: 'library', mount: 'Lib', relPath: 'a.mkv' }),
      m({ fileIndex: 2, source: 'cloud', mount: 'GDrive', relPath: 'b.mkv' }),
      m({ fileIndex: 3, source: 'download' }), // already queued — not linkable
      m({ fileIndex: 4, source: 'library', mount: '', relPath: '' }), // incomplete — skip
    ])
    expect(items).toEqual([
      { fileIndex: 1, mount: 'Lib', relPath: 'a.mkv' },
      { fileIndex: 2, mount: 'GDrive', relPath: 'b.mkv' },
    ])
  })

  it('returns empty when nothing is linkable', () => {
    expect(linkableItems([m({ source: 'download' })])).toEqual([])
  })
})

describe('planAfterLink', () => {
  it('no file list, fully matched → download nothing', () => {
    const plan = planAfterLink(false, [], [m({ fileIndex: 0 })], 1)
    expect(plan).toEqual({ kind: 'none' })
  })

  it('no file list, partial match → fall back to whole-torrent', () => {
    const plan = planAfterLink(false, [], [m({ fileIndex: 0 })], 3)
    expect(plan).toEqual({ kind: 'whole' })
  })

  it('file list, all wanted are matched → download nothing', () => {
    const plan = planAfterLink(true, [0, 1], [m({ fileIndex: 0 }), m({ fileIndex: 1 })], 2)
    expect(plan).toEqual({ kind: 'none' })
  })

  it('file list, partial → download only the unmatched wanted files', () => {
    const plan = planAfterLink(true, [0, 1, 2], [m({ fileIndex: 1 })], 3)
    expect(plan).toEqual({ kind: 'files', indices: [0, 2] })
  })

  it('ignores matches outside the wanted selection', () => {
    // The user only wants index 0; a match on index 5 must not change the plan.
    const plan = planAfterLink(true, [0], [m({ fileIndex: 5 })], 6)
    expect(plan).toEqual({ kind: 'files', indices: [0] })
  })
})
