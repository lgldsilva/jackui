import { useTranslation } from 'react-i18next'
import { SearchResult } from '../../api/client'

// The translate fn type (react-i18next's TFunction), shared with the module-level
// render helpers below (they aren't components, so they receive `t` as a param).
export type TFn = ReturnType<typeof useTranslation>['t']

export type PlaylistMeta = {
  readonly name: string
  // Each item is a torrent (a pack with many files) or a single local file.
  // The aggregated track sidebar needs the source (infoHash/magnet) to resolve
  // every item's file list, not just the playing one.
  readonly items: readonly { title: string; infoHash: string; magnet: string; fileIndex: number }[]
  readonly currentIndex: number
}

export type PlayerModalProps = {
  readonly result: SearchResult | null
  readonly onClose: () => void
  readonly initialFileIndex?: number
  readonly initialSeek?: number
  readonly playlist?: PlaylistMeta | null
  readonly onPlaylistAdvance?: () => void
  readonly onPlaylistPrevious?: () => void
  readonly onPlaylistJump?: (itemIndex: number, fileIndex?: number) => void
  readonly repeat?: 'none' | 'one' | 'all'
  readonly shuffle?: boolean
  readonly onCycleRepeat?: () => void
  readonly onToggleShuffle?: () => void
  readonly onPrefetchNextPlaylist?: () => void
  readonly onPrefetchNextNextPlaylist?: () => void
  readonly startMinimized?: boolean
  readonly audioMode?: boolean
  /** Render the player filling the whole browser viewport (not the centered
   *  modal) — used when the tab booted at a /?play= deep-link. Shows a Home
   *  button instead of minimize/close. */
  readonly fullViewport?: boolean
  /** Navigate back to Home (used by the full-viewport Home button). */
  readonly onHome?: () => void
  /** Reports the playhead (seconds) on every timeupdate. Lets the provider
   *  preserve position when it re-keys the modal on a Cinema/Música switch. */
  readonly onProgress?: (sec: number) => void
}

export interface PlaylistBarControls {
  onPrev: (() => void) | undefined
  onToggleShuffle: (() => void) | undefined
  shuffle: boolean
  onCycleRepeat: (() => void) | undefined
  repeat: 'none' | 'one' | 'all'
  onNext: (() => void) | undefined
}
