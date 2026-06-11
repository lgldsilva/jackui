// Pure logic for the Local page's BATCH promote — kept out of the React
// component so it can be unit-tested without the DOM or the API. The component
// walks each selected directory (localWalk, media_only) and passes the loose
// files plus the walked files here to be merged into the final, deduped list
// the promote modal consumes.

import type { LocalEntry } from '../api/client'

// mergePromoteFiles folds the loosely-selected files together with the files
// discovered inside selected folders, dropping directories and deduplicating by
// path (a file can appear both as a loose selection AND inside a selected
// parent folder — promote it once). Loose files win on collision so the
// already-listed entry's metadata is preferred. Order is preserved: loose files
// first (in selection order), then newly discovered files.
export function mergePromoteFiles(
  looseFiles: readonly LocalEntry[],
  walkedFiles: readonly LocalEntry[],
): LocalEntry[] {
  const byPath = new Map<string, LocalEntry>()
  for (const f of [...looseFiles, ...walkedFiles]) {
    if (f.isDir) continue
    if (!byPath.has(f.path)) byPath.set(f.path, f)
  }
  return [...byPath.values()]
}
