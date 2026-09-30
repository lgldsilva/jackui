// Snapshot of the active playlist, persisted in localStorage. The URL only carries the
// current ITEM (?play=hash&f=idx) — on reload/reopen, the player restored
// only that item via playSingle and LOST the list (previous/next) and the position.
// Here we store the whole list + which item was playing, to reopen restoring the
// playlist's full context (working prev/next).
import { load, save, remove } from '../../lib/storage'
import { isIncognito } from '../../lib/incognito'
import type { PlaylistItem } from '../../api/client'

const KEY = 'player.playlistSnapshot'
// Don't resurrect very old playlists: a deep-link whose hash happens to match
// an item from a session weeks ago shouldn't reopen that list.
const TTL_MS = 7 * 24 * 60 * 60 * 1000 // 7 days

export type PlaylistSnapshot = {
  readonly name: string
  readonly items: readonly PlaylistItem[]
  // Index (in the original `items` array, not the shuffled `order`) of the item that
  // was playing — becomes the startIndex on restore.
  readonly currentItemIndex: number
  readonly savedAt: number
}

export function savePlaylistSnapshot(name: string, items: readonly PlaylistItem[], currentItemIndex: number): void {
  if (items.length === 0) return
  // Never persist playlists while incognito — that would leave titles/magnets
  // in localStorage after the session ends (and after logout for the next user).
  if (isIncognito()) return
  const snap: PlaylistSnapshot = { name, items, currentItemIndex, savedAt: Date.now() }
  save(KEY, snap)
}

export function loadPlaylistSnapshot(): PlaylistSnapshot | null {
  const snap = load<PlaylistSnapshot | null>(KEY, null)
  if (!snap || !Array.isArray(snap.items) || snap.items.length === 0) return null
  if (typeof snap.savedAt !== 'number' || Date.now() - snap.savedAt > TTL_MS) return null
  return snap
}

export function clearPlaylistSnapshot(): void {
  remove(KEY)
}

// Finds the playlist item index matching a deep-link info_hash.
// Returns -1 when the hash doesn't belong to the saved playlist (→ falls to single play).
export function snapshotIndexOfHash(snap: PlaylistSnapshot, hash: string): number {
  return snap.items.findIndex(it => it.infoHash === hash)
}
