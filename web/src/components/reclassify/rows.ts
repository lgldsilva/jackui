// Pure logic for the reclassify batch table — kept out of the React component so
// it can be unit-tested in isolation (no DOM, no API). Builds the editable rows
// from the IA preview and folds the user's edits back into the `overrides` map
// the backend's LocalPromote expects (source path → edited target RELATIVE to
// the destination base). The backend re-sanitizes every override (path
// traversal, unsafe chars, category reuse) — this is the UI-side mirror so the
// preview the user sees matches what lands on disk.

import type { PromotePreviewEntry } from '../../api/client'

// ReclassifyRow is one editable line in the batch table.
export type ReclassifyRow = {
  // path is the ORIGINAL un-scoped source path the UI sent (and the key the
  // backend echoes back in previews/results). Stable row identity.
  readonly path: string
  readonly originalName: string
  // selected gates whether "Apply selected" processes this row.
  selected: boolean
  // category is the top-level destination folder (e.g. "Movies"). Editable.
  category: string
  // middle are the path segments between category and the leaf filename
  // (e.g. ["The Show", "Season 01"] for a TV episode). Preserved, not edited
  // directly — they ride along so a TV layout stays intact when only the
  // category/name change.
  readonly middle: readonly string[]
  // finalName is the leaf filename (the last path segment). Editable.
  finalName: string
  readonly kind: 'movie' | 'tv'
  readonly reusedFolder?: string
  // error is set when the IA preview itself failed for this item — the row is
  // shown but can't be applied.
  readonly error?: string
}

// splitTargetPath breaks an IA target path "Movies/Inception (2010)/Inception.mkv"
// into { category, middle, finalName }. A single-segment path is treated as a
// bare filename (no category). Defensive against empty/edge inputs.
export function splitTargetPath(target: string): { category: string; middle: string[]; finalName: string } {
  const segs = (target || '').split('/').filter(Boolean)
  if (segs.length === 0) return { category: '', middle: [], finalName: '' }
  if (segs.length === 1) return { category: '', middle: [], finalName: segs[0] }
  return {
    category: segs[0],
    middle: segs.slice(1, -1),
    finalName: segs[segs.length - 1],
  }
}

// buildEditableRows turns the IA preview list into editable rows. Errored
// previews still produce a row (so the user sees what failed) but start
// unselected. Successful previews start selected.
export function buildEditableRows(previews: readonly PromotePreviewEntry[]): ReclassifyRow[] {
  return previews.map(p => {
    const { category, middle, finalName } = splitTargetPath(p.targetPath || '')
    return {
      path: p.path ?? p.originalName,
      originalName: p.originalName,
      selected: !p.error,
      category,
      middle,
      finalName: finalName || p.originalName,
      kind: p.kind,
      reusedFolder: p.reusedFolder,
      error: p.error,
    }
  })
}

// rowTargetPath rebuilds the destination path (relative to base) from a row's
// editable fields, preserving the middle segments. Trims blanks so a cleared
// category just drops that segment (the file lands at the base root).
export function rowTargetPath(row: Pick<ReclassifyRow, 'category' | 'middle' | 'finalName'>): string {
  const parts = [row.category, ...row.middle, row.finalName]
    .map(s => s.trim())
    .filter(Boolean)
  return parts.join('/')
}

// rowIsEdited reports whether the row's current target differs from the IA's
// original suggestion — only edited rows need an override entry (unedited rows
// let the backend recompute the IA path, keeping the request small and letting
// a late TMDB enrichment still apply).
export function rowIsEdited(row: ReclassifyRow, originalTarget: string): boolean {
  return rowTargetPath(row) !== originalTarget
}

// buildOverrides folds the SELECTED, EDITED rows into the overrides map the
// backend consumes. `originalByPath` is the IA's original target per row path
// (from the preview), so an unchanged row is omitted. Errored rows are skipped.
export function buildOverrides(
  rows: readonly ReclassifyRow[],
  originalByPath: Readonly<Record<string, string>>,
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const row of rows) {
    if (!row.selected || row.error) continue
    const target = rowTargetPath(row)
    if (!target) continue
    if (target !== (originalByPath[row.path] ?? '')) {
      out[row.path] = target
    }
  }
  return out
}

// selectedPaths returns the source paths of the rows to apply (selected,
// non-errored) — the `paths` array sent to LocalPromote.
export function selectedPaths(rows: readonly ReclassifyRow[]): string[] {
  return rows.filter(r => r.selected && !r.error).map(r => r.path)
}

// categoryOptions derives the category <select> options: the union of existing
// destination folders and any category the IA already chose for a row, sorted
// and de-duped (case-insensitively, first spelling wins). Lets the user pick a
// known folder instead of retyping it (and matching its exact casing).
export function categoryOptions(
  destFolders: readonly string[],
  rows: readonly ReclassifyRow[],
): string[] {
  const seen = new Map<string, string>()
  const add = (f: string) => {
    const key = f.trim().toLowerCase()
    if (key && !seen.has(key)) seen.set(key, f.trim())
  }
  destFolders.forEach(add)
  rows.forEach(r => add(r.category))
  return [...seen.values()].sort((a, b) => a.localeCompare(b))
}
