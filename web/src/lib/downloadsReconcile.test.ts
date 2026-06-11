import { describe, expect, it } from 'vitest'
import {
  newPendingDeletes,
  markDeleted,
  clearDeleted,
  reconcile,
} from './downloadsReconcile'

type Row = { id: number; name?: string }

const rows = (...ids: number[]): Row[] => ids.map(id => ({ id }))

describe('reconcile', () => {
  it('passes the list through untouched when nothing is pending', () => {
    const pd = newPendingDeletes()
    const list = rows(1, 2, 3)
    expect(reconcile(pd, list)).toEqual(list)
  })

  it('hides an optimistically-deleted row that a stale poll still returns', () => {
    // THE BUG: a poll in flight before the delete completed returns the row.
    const pd = markDeleted(newPendingDeletes(), [2])
    const out = reconcile(pd, rows(1, 2, 3))
    expect(out.map(r => r.id)).toEqual([1, 3])
    // Still pending — the backend hasn't confirmed it's gone yet.
    expect(pd.ids.has(2)).toBe(true)
  })

  it('stops tracking a delete once the backend confirms it is gone', () => {
    const pd = markDeleted(newPendingDeletes(), [2])
    // Backend no longer returns id 2 → confirmed gone → pruned.
    const out = reconcile(pd, rows(1, 3))
    expect(out.map(r => r.id)).toEqual([1, 3])
    expect(pd.ids.has(2)).toBe(false)
  })

  it('does NOT hide a future row that reuses a confirmed-gone id', () => {
    const pd = markDeleted(newPendingDeletes(), [2])
    // First poll confirms 2 is gone → pruned.
    reconcile(pd, rows(1, 3))
    expect(pd.ids.has(2)).toBe(false)
    // A later create reuses id 2 — it must NOT be hidden.
    const out = reconcile(pd, rows(1, 2, 3))
    expect(out.map(r => r.id)).toEqual([1, 2, 3])
  })

  it('keeps hiding across multiple stale polls until confirmed', () => {
    const pd = markDeleted(newPendingDeletes(), [5])
    // Two consecutive stale polls still carry the row.
    expect(reconcile(pd, rows(5, 6)).map(r => r.id)).toEqual([6])
    expect(reconcile(pd, rows(5, 6)).map(r => r.id)).toEqual([6])
    expect(pd.ids.has(5)).toBe(true)
    // Finally the backend drops it.
    expect(reconcile(pd, rows(6)).map(r => r.id)).toEqual([6])
    expect(pd.ids.has(5)).toBe(false)
  })

  it('handles a batch of optimistic deletes', () => {
    const pd = markDeleted(newPendingDeletes(), [1, 2, 3])
    const out = reconcile(pd, rows(1, 2, 3, 4))
    expect(out.map(r => r.id)).toEqual([4])
    expect([...pd.ids].sort()).toEqual([1, 2, 3])
    // Backend drops 1 and 3 but 2 lingers (slow drop) — only 2 stays pending.
    const out2 = reconcile(pd, rows(2, 4))
    expect(out2.map(r => r.id)).toEqual([4])
    expect([...pd.ids]).toEqual([2])
  })

  it('does not mutate the incoming list', () => {
    const pd = markDeleted(newPendingDeletes(), [2])
    const list = rows(1, 2, 3)
    reconcile(pd, list)
    expect(list.map(r => r.id)).toEqual([1, 2, 3])
  })
})

describe('clearDeleted', () => {
  it('un-hides a row when its delete failed (so it reappears on next poll)', () => {
    const pd = markDeleted(newPendingDeletes(), [7, 8])
    clearDeleted(pd, [7])
    // 7 is no longer hidden; 8 still is.
    const out = reconcile(pd, rows(7, 8, 9))
    expect(out.map(r => r.id)).toEqual([7, 9])
    expect(pd.ids.has(8)).toBe(true)
  })

  it('is a no-op for ids that were never pending', () => {
    const pd = markDeleted(newPendingDeletes(), [1])
    clearDeleted(pd, [99])
    expect([...pd.ids]).toEqual([1])
  })
})

describe('markDeleted', () => {
  it('accumulates ids and returns the tracker', () => {
    const pd = newPendingDeletes()
    expect(markDeleted(pd, [1, 2])).toBe(pd)
    markDeleted(pd, [2, 3])
    expect([...pd.ids].sort()).toEqual([1, 2, 3])
  })
})
