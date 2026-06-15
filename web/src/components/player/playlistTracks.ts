import { TorrentInfo } from '../../api/client'
import { fileKind } from '../../lib/playable'
import { filterAndSortFiles } from './playerFormat'

// A playlist is an AGGREGATOR: each item is a torrent (which may be a multi-file
// pack — a discography, a season) or a single local file (rclone/HDD). When a
// playlist plays we want to surface EVERY playable file across ALL items, not
// just the current torrent's. These pure helpers turn resolved torrent metadata
// into the flat, grouped track model the sidebar renders and the hook fills in.

// The minimum a playlist item must carry for us to resolve + play its files.
// Mirrors the relevant subset of api PlaylistItem (kept local so the player
// pieces don't depend on the full playlist API shape).
export type PlaylistItemLite = {
  readonly title: string
  readonly infoHash: string
  readonly magnet: string
  readonly fileIndex: number
}

export type PlaylistGroupStatus = 'pending' | 'loading' | 'ready' | 'error'

export type PlaylistTrack = {
  readonly fileIndex: number
  readonly name: string
  readonly path: string
  readonly size: number
  readonly kind: 'audio' | 'video'
}

export type PlaylistGroup = {
  // Index into the playlist's ORIGINAL items array (not the shuffle order) —
  // the stable identity used by jump-to-track and the resolver.
  readonly itemIndex: number
  readonly title: string
  readonly infoHash: string
  readonly isLocal: boolean
  readonly status: PlaylistGroupStatus
  readonly tracks: readonly PlaylistTrack[]
}

// basename returns the last path segment (a torrent file path is usually
// "Album/01 - Track.flac"; we show just the file name in the list).
export function basename(p: string): string {
  const parts = p.split('/')
  return parts[parts.length - 1] || p
}

// extractTracks turns a resolved TorrentInfo into the playable-only track list,
// in the SAME display order the player navigates (filterAndSortFiles: extras
// last, episode/track order, then file index) so the visible list and the
// ⏮⏭ queue never disagree. Non-playable files (.nfo/.jpg/...) are dropped.
export function extractTracks(info: TorrentInfo | null): PlaylistTrack[] {
  if (!info?.files?.length) return []
  const ordered = filterAndSortFiles(info.files, {
    filter: '', typeFilter: 'all', sortBySize: false, sizeDesc: false,
  })
  const tracks: PlaylistTrack[] = []
  for (const f of ordered) {
    const kind = fileKind(f.path, f.isVideo)
    if (kind === 'other') continue
    tracks.push({ fileIndex: f.index, name: basename(f.path), path: f.path, size: f.size, kind })
  }
  return tracks
}

// orderPending returns the item indices still worth resolving, in the order the
// background resolver should tackle them: the currently-playing item first
// (so its group is ready instantly), then ascending playlist order. Already
// ready/loading groups and the ones in flight are excluded.
export function orderPending(
  groups: readonly PlaylistGroup[],
  currentItemIndex: number,
  inFlight: ReadonlySet<number>,
): number[] {
  const pending = groups
    .filter(g => g.status === 'pending' && !inFlight.has(g.itemIndex))
    .map(g => g.itemIndex)
  pending.sort((a, b) => {
    if (a === currentItemIndex) return -1
    if (b === currentItemIndex) return 1
    return a - b
  })
  return pending
}

// totalReadyTracks is the count of playable files discovered so far across all
// resolved groups — drives the sidebar header ("N faixas").
export function totalReadyTracks(groups: readonly PlaylistGroup[]): number {
  return groups.reduce((sum, g) => sum + g.tracks.length, 0)
}
