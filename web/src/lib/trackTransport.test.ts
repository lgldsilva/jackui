import { describe, it, expect } from 'vitest'
import { nextTrack, prevTrack } from './trackTransport'

const order = [10, 11, 12, 13] // fileIndices in play order

describe('nextTrack', () => {
  it('middle of the album → next track', () => {
    expect(nextTrack(order, 11, 'none', false)).toEqual({ kind: 'track', fileIndex: 12 })
  })

  it('end + has playlist next → spill (no double wrap)', () => {
    expect(nextTrack(order, 13, 'all', true)).toEqual({ kind: 'spill' })
  })

  it('end + repeat-all + no playlist → wrap-rebuild', () => {
    expect(nextTrack(order, 13, 'all', false)).toEqual({ kind: 'wrap-rebuild' })
  })

  it('end + repeat-none + no playlist → spill (no-op in the caller)', () => {
    expect(nextTrack(order, 13, 'none', false)).toEqual({ kind: 'spill' })
  })

  it('cursor -1 (track out of the order) → plays the first', () => {
    expect(nextTrack(order, 999, 'none', false)).toEqual({ kind: 'track', fileIndex: 10 })
  })

  it('repeat-one does NOT short-circuit: button skips the track normally', () => {
    expect(nextTrack(order, 11, 'one', false)).toEqual({ kind: 'track', fileIndex: 12 })
  })

  it('1-track album, end, repeat-all without playlist → wrap-rebuild', () => {
    expect(nextTrack([42], 42, 'all', false)).toEqual({ kind: 'wrap-rebuild' })
  })

  it('empty order → spill', () => {
    expect(nextTrack([], 0, 'all', false)).toEqual({ kind: 'spill' })
  })
})

describe('prevTrack', () => {
  it('middle of the album → previous track', () => {
    expect(prevTrack(order, 12, 'none', false)).toEqual({ kind: 'track', fileIndex: 11 })
  })

  it('start + has playlist previous → spill', () => {
    expect(prevTrack(order, 10, 'all', true)).toEqual({ kind: 'spill' })
  })

  it('start + repeat-all + no playlist → last track', () => {
    expect(prevTrack(order, 10, 'all', false)).toEqual({ kind: 'track', fileIndex: 13 })
  })

  it('start + repeat-none + no playlist → spill', () => {
    expect(prevTrack(order, 10, 'none', false)).toEqual({ kind: 'spill' })
  })

  it('cursor -1 → plays the first', () => {
    expect(prevTrack(order, 999, 'none', false)).toEqual({ kind: 'track', fileIndex: 10 })
  })
})
