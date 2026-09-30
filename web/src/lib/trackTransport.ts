// trackTransport: PURE core of track navigation WITHIN a torrent
// (multi-file album / multi-episode show). Decides the next
// step given the tracks' play order (already shuffled or not), the
// current fileIndex, the repeat mode and whether there is a multi-torrent playlist
// context to "spill" into when hitting the edge.
//
// repeat-one is NOT handled here: replaying the same track happens in onEnded
// (replay on the same <audio> element). The prev/next buttons skip the track
// normally even in repeat-one — that's the expected UX behavior.
export type RepeatMode = 'none' | 'one' | 'all'

export type TrackStep =
  // Play this track from the SAME torrent.
  | { readonly kind: 'track'; readonly fileIndex: number }
  // Album edge → delegate to the PLAYLIST level (next/previous torrent).
  | { readonly kind: 'spill' }
  // repeat-all WITHOUT playlist → re-shuffle and play the 1st of the new pass.
  | { readonly kind: 'wrap-rebuild' }

// stepAtEnd resolves the edge (end on next, start on prev): spill's
// priority for the playlist avoids "double wrap" — in a multi-torrent context the end
// of the album spills into the next torrent and the repeat-all wrap happens
// only at the playlist level (goTo). Without a playlist, track wrap-rebuild is the only option.
function stepAtEnd(repeat: RepeatMode, hasPlaylistNeighbor: boolean): TrackStep {
  if (hasPlaylistNeighbor) return { kind: 'spill' }
  if (repeat === 'all') return { kind: 'wrap-rebuild' }
  return { kind: 'spill' }
}

export function nextTrack(
  order: readonly number[],
  currentFileIndex: number,
  repeat: RepeatMode,
  hasPlaylistNext: boolean,
): TrackStep {
  if (order.length === 0) return { kind: 'spill' }
  const cursor = order.indexOf(currentFileIndex)
  // Current track out of the order (filter changed, etc.) → start from the 1st.
  if (cursor < 0) return { kind: 'track', fileIndex: order[0] }
  if (cursor < order.length - 1) return { kind: 'track', fileIndex: order[cursor + 1] }
  return stepAtEnd(repeat, hasPlaylistNext)
}

export function prevTrack(
  order: readonly number[],
  currentFileIndex: number,
  repeat: RepeatMode,
  hasPlaylistPrev: boolean,
): TrackStep {
  if (order.length === 0) return { kind: 'spill' }
  const cursor = order.indexOf(currentFileIndex)
  if (cursor < 0) return { kind: 'track', fileIndex: order[0] }
  if (cursor > 0) return { kind: 'track', fileIndex: order[cursor - 1] }
  // Album start: spill to the previous torrent; otherwise repeat-all wraps to the last.
  if (hasPlaylistPrev) return { kind: 'spill' }
  if (repeat === 'all') return { kind: 'track', fileIndex: order[order.length - 1] }
  return { kind: 'spill' }
}
