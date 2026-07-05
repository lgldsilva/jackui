import type { DedupMatch, DedupLinkItem } from '../api/client'

// linkableItems are the matches the backend can turn into completed links: files
// living on a browsable mount (library/cloud), which carry mount + relPath. A
// 'download'-source match is already a queued/completed row — there is nothing
// to link, only to exclude from a re-download.
export function linkableItems(matches: DedupMatch[]): DedupLinkItem[] {
  return matches
    .filter((m) => m.source !== 'download' && !!m.mount && !!m.relPath)
    .map((m) => ({ fileIndex: m.fileIndex, mount: m.mount as string, relPath: m.relPath as string }))
}

// DownloadPlan is what still needs fetching after the matches are linked:
//   none  — every wanted file is already present; enqueue nothing.
//   whole — no per-file list available and only a partial match; fall back to
//           the original whole-torrent enqueue (can't exclude individual files).
//   files — enqueue exactly these file indices.
export type DownloadPlan =
  | { kind: 'none' }
  | { kind: 'whole' }
  | { kind: 'files'; indices: number[] }

// planAfterLink decides what to download once the already-present files are
// linked. wanted are the file indices the user chose (the picker selection);
// hasFileList is false for single-file/whole enqueues with no picker.
export function planAfterLink(
  hasFileList: boolean,
  wanted: number[],
  matches: DedupMatch[],
  totalFiles: number,
): DownloadPlan {
  const matched = new Set(matches.map((m) => m.fileIndex))
  if (!hasFileList) {
    return matches.length >= totalFiles ? { kind: 'none' } : { kind: 'whole' }
  }
  const remaining = wanted.filter((i) => !matched.has(i))
  return remaining.length === 0 ? { kind: 'none' } : { kind: 'files', indices: remaining }
}
