import type { LocalEntry } from '../api/client'

// STATUS filter on the local file list: show everything, only what's
// downloading (.part entries / folders containing a .part → e.incomplete), or only
// completed ones. Applies to files AND folders, but only when ≠ 'all' — by default
// folder navigation stays free (unfiltered).
export type LocalStatusFilter = 'all' | 'downloading' | 'done'

export function matchesEntryStatus(e: LocalEntry, f: LocalStatusFilter): boolean {
  if (f === 'all') return true
  return f === 'downloading' ? !!e.incomplete : !e.incomplete
}
