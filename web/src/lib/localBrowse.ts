import type { LocalMount } from '../api/client'

/** Where a download's file lives in the local browser: a mount + the relative
 *  folder to open. Returned by {@link localBrowseTarget}. */
export type LocalBrowseTarget = { mount: string; path: string }

/**
 * Maps a download's absolute `file_path` to a local-browser target: the mount it
 * lives under + the folder to open. Strips the per-user subdir prefix the same
 * way the player does (UserSubpath mounts isolate files under `/{username}/` and
 * the backend re-scopes by username when resolving). Returns null when the path
 * isn't under any browsable mount (e.g. a cache-only completion).
 *
 * `targetIsDir` is true for whole-torrent downloads (file_path IS the folder, so
 * open it directly) and false for a single file (open the folder containing it).
 */
export function localBrowseTarget(
  filePath: string,
  mounts: readonly LocalMount[],
  username?: string,
  targetIsDir = false,
): LocalBrowseTarget | null {
  if (!filePath) return null
  const m = mounts.find(mt => filePath === mt.path || filePath.startsWith(mt.path + '/'))
  if (!m) return null

  let rel = filePath.slice(m.path.length).replace(/^\/+/, '')
  if (m.userSubpath && username && (rel === username || rel.startsWith(username + '/'))) {
    rel = rel.slice(username.length).replace(/^\/+/, '')
  }

  if (targetIsDir) return { mount: m.name, path: rel }
  const slash = rel.lastIndexOf('/')
  return { mount: m.name, path: slash >= 0 ? rel.slice(0, slash) : '' }
}

/** Builds the LocalPage deep-link (`/local?mount=&path=`) for a download's file,
 *  or null when it isn't under a browsable mount. Thin wrapper over
 *  {@link localBrowseTarget}. */
export function localBrowseHref(
  filePath: string,
  mounts: readonly LocalMount[],
  username?: string,
  targetIsDir = false,
): string | null {
  const t = localBrowseTarget(filePath, mounts, username, targetIsDir)
  if (!t) return null
  return `/local?mount=${encodeURIComponent(t.mount)}&path=${encodeURIComponent(t.path)}`
}
