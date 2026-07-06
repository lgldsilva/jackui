import type { StreamFile } from '../api/client'

/** Default minimum size for non-video files in the file picker (10 MiB). */
export const DEFAULT_MIN_BYTES = 10 * 1024 * 1024

/**
 * Heuristic default selection for download file pickers: videos always on;
 * other files only when large enough; falls back to the largest file.
 */
export function defaultSelectedFiles(files: StreamFile[]): Set<number> {
  const sel = new Set<number>()
  for (const f of files) {
    if (f.isVideo || f.size >= DEFAULT_MIN_BYTES) sel.add(f.index)
  }
  if (sel.size === 0 && files.length > 0) {
    let biggest = files[0]
    for (const f of files) if (f.size > biggest.size) biggest = f
    sel.add(biggest.index)
  }
  return sel
}
