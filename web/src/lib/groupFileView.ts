import type { DownloadEntry } from '../api/downloads'

// Filter + sorting for the files WITHIN a multi-file torrent (the expanded
// DownloadGroupCard). Only makes sense for groups with 2+ files —
// a single-file torrent has nothing to filter/sort. Pure logic,
// testable, outside the DownloadsPage god-file.

export type GroupFileStatusFilter = 'all' | 'active' | 'completed'
export type GroupFileSortKey = 'name' | 'size' | 'progress'
export type GroupFileSortDir = 'asc' | 'desc'

const naturalCmp = (a: string, b: string) =>
  a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' })

// 'active' = everything that hasn't completed yet (downloading/queued/paused/failed/
// moving); 'completed' = finished ones only. Covers the "list only downloading"
// vs "only completed" request.
function matchesStatus(d: DownloadEntry, f: GroupFileStatusFilter): boolean {
  if (f === 'all') return true
  if (f === 'completed') return d.status === 'completed'
  return d.status !== 'completed'
}

// Per-file progress in [0,1]: completed = 1; otherwise bytes/size (or the
// backend progress when the size is unknown).
function fileProgress(d: DownloadEntry): number {
  if (d.status === 'completed') return 1
  if (d.fileSize > 0) return Math.min(1, d.bytesDownloaded / d.fileSize)
  return d.progress || 0
}

// viewGroupFiles filters by status and sorts by name/size/progress. Stable
// on ties (preserves the natural arrival order). Returns a NEW array.
export function viewGroupFiles(
  files: readonly DownloadEntry[],
  statusFilter: GroupFileStatusFilter,
  sortKey: GroupFileSortKey,
  sortDir: GroupFileSortDir,
): DownloadEntry[] {
  const sign = sortDir === 'asc' ? 1 : -1
  return files
    .filter((d) => matchesStatus(d, statusFilter))
    .map((d, i) => ({ d, i }))
    .sort((a, b) => {
      let r = 0
      if (sortKey === 'name') r = naturalCmp(a.d.filePath || a.d.name, b.d.filePath || b.d.name)
      else if (sortKey === 'size') r = a.d.fileSize - b.d.fileSize
      else r = fileProgress(a.d) - fileProgress(b.d)
      if (r === 0) return a.i - b.i // stable
      return sign * r
    })
    .map((x) => x.d)
}

// groupStatusCounts feeds the bar badges (All N · Downloading N · Completed N).
export function groupStatusCounts(
  files: readonly DownloadEntry[],
): { all: number; active: number; completed: number } {
  let completed = 0
  for (const d of files) if (d.status === 'completed') completed++
  return { all: files.length, active: files.length - completed, completed }
}
