// downloadsReconcile — pure logic for reconciling the periodic downloads poll
// with optimistic deletes, so a delete never silently "doesn't take".
//
// THE BUG (intermittent "clicked Remove, nothing happened"):
// DownloadsPage polls `load()` every 2s and replaces the list with whatever the
// backend returns. A poll request that was ALREADY IN FLIGHT when the user hit
// Remove can land AFTER the delete completed — its (stale) response still
// contains the just-deleted row, so the row reappears. Combined with the old
// backend swallowing cross-user/idempotent delete failures, the row looked
// un-deletable.
//
// THE FIX: track IDs the user optimistically removed. Every incoming poll list
// is filtered through that set. An ID stays "pending delete" until a poll
// CONFIRMS it's gone (the backend no longer returns it) — at which point we
// stop tracking it (so a future re-add of the same ID isn't hidden forever).
// This is a pure, framework-free module so it can be unit-tested in isolation.

export interface HasID {
  id: number
}

// PendingDeletes is the optimistic-delete tracker. It is intentionally a plain
// mutable object (not React state) so the polling closure always sees the
// latest set without re-subscribing — the page keeps it in a ref.
export interface PendingDeletes {
  ids: Set<number>
}

export function newPendingDeletes(): PendingDeletes {
  return { ids: new Set<number>() }
}

// markDeleted records that the user removed these IDs optimistically. Returns
// the same tracker for chaining/testing.
export function markDeleted(pd: PendingDeletes, ids: number[]): PendingDeletes {
  for (const id of ids) pd.ids.add(id)
  return pd
}

// reconcile filters a freshly-polled list against the pending-delete set AND
// prunes the set: any pending ID the backend no longer returns is confirmed
// gone and dropped from tracking (so its slot can be reused later). Any pending
// ID still present in the incoming list is hidden from the returned list (the
// delete is in flight / a stale poll raced it) but KEPT pending.
//
// Returns the list the UI should render. Does not mutate the input list.
export function reconcile<T extends HasID>(pd: PendingDeletes, incoming: T[]): T[] {
  if (pd.ids.size === 0) return incoming
  const present = new Set<number>()
  const out: T[] = []
  for (const item of incoming) {
    if (pd.ids.has(item.id)) {
      present.add(item.id) // still returned by backend → keep hiding, keep pending
      continue
    }
    out.push(item)
  }
  // Prune confirmed-gone IDs: pending but NOT in the latest backend list.
  for (const id of [...pd.ids]) {
    if (!present.has(id)) pd.ids.delete(id)
  }
  return out
}

// clearDeleted forgets a pending delete explicitly (e.g. the DELETE request
// failed, so we must let the row come back into view rather than hide it
// forever). Safe to call with IDs that aren't tracked.
export function clearDeleted(pd: PendingDeletes, ids: number[]): void {
  for (const id of ids) pd.ids.delete(id)
}
