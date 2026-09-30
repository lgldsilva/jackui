import { useMemo, useCallback } from 'react'
import { TorrentInfo } from '../../api/client'
import { clientLog } from '../../lib/diag'
import { nextTrack, prevTrack } from '../../lib/trackTransport'
import { useKeyboardShortcuts, useMediaSession, useMediaQueue } from './playerHooks'
import type { MediaChapter } from '../../api/stream-types'
import { filterAndSortFiles, type FileType } from './playerFormat'
import { useTrackOrder } from './useTrackOrder'
import { useAudioDirectUrl } from './useAudioDirectUrl'
import { usePlaylistTracks } from './usePlaylistTracks'
import { audioCoverURL } from './AudioCoverArt'
import type { PlaylistMeta } from './playerTypes'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// In-torrent queue + continuous transport (album tracks / series episodes) plus
// the shuffle/repeat-aware next/prev spill into the playlist, the simplified
// audio engine wiring, keyboard shortcuts and the Media Session (lock-screen)
// controls. Per-file resets on a track switch run via `resetForFile`.
export function usePlayerTransport(deps: {
  info: TorrentInfo | null
  selectedFile: number
  shuffle: boolean
  repeat: 'none' | 'one' | 'all'
  audioMode: boolean
  minimized: boolean
  mediaToken: string
  playlist: PlaylistMeta | null | undefined
  sidebarOpen: boolean
  onPlaylistAdvance?: () => void
  onPlaylistPrevious?: () => void
  onProgress?: (sec: number) => void
  videoRef: React.RefObject<HTMLVideoElement | null>
  audioRef: React.MutableRefObject<HTMLAudioElement | null>
  handleRequestFullscreen: () => void
  fileFilter: string
  fileTypeFilter: FileType
  fileSortBySize: boolean
  fileSizeDesc: boolean
  resetForFile: (idx: number) => void
  setCurrentTime: Setter<number>
  setDuration: Setter<number>
  chapters?: readonly MediaChapter[]
  playbackSpeed?: number
  setPlaybackSpeed?: (v: number) => void
}) {
  const {
    info, selectedFile, shuffle, repeat, audioMode, minimized, mediaToken, playlist, sidebarOpen,
    onPlaylistAdvance, onPlaylistPrevious, onProgress, videoRef, audioRef, handleRequestFullscreen,
    fileFilter, fileTypeFilter, fileSortBySize, fileSizeDesc, resetForFile, setCurrentTime, setDuration,
    chapters, playbackSpeed, setPlaybackSpeed,
  } = deps

  // Display order of the file list — the SAME order the sidebar renders
  // (episodes sorted, extras last, user's size-sort respected). The queue
  // below follows it so next/prev never disagree with the visible list.
  const displayFiles = useMemo(
    () => filterAndSortFiles(info?.files ?? [], {
      filter: fileFilter, typeFilter: fileTypeFilter,
      sortBySize: fileSortBySize, sizeDesc: fileSizeDesc,
    }),
    [info, fileFilter, fileTypeFilter, fileSortBySize, fileSizeDesc],
  )

  // Unified in-torrent queue (album tracks / series episodes) of the same kind
  // as the current file. Generalises the old video-only navigation so audio
  // albums get ⏮⏭ too. Hook keeps the logic out of this god-file (gate).
  const mediaQueue = useMediaQueue(info, selectedFile, displayFiles)
  // Play order of the tracks of the SAME torrent, honoring shuffle (bag) and
  // serving as the base for repeat. The picker/sidebar keeps using mediaQueue (display
  // order); the transport (prev/next/onEnded) follows trackOrder.
  const trackOrder = useTrackOrder(mediaQueue.indices, selectedFile, shuffle, info?.infoHash)

  const playFile = (idx: number) => {
    if (idx < 0) return
    resetForFile(idx)
  }

  // Continuous transport: stay within the current torrent's queue, then spill
  // over into the user's playlist (next/prev torrent) at the boundary — one
  // logical timeline (Spotify/VLC style). Reused by the buttons, MediaSession
  // (lock-screen/headphones) and onEnded auto-advance. nextTrack/prevTrack
  // decide track vs. spill vs. wrap (repeat-all without playlist) — shuffle and
  // repeat now apply WITHIN the album, not only across torrents.
  const handleNext = () => {
    const step = nextTrack(trackOrder.order, selectedFile, repeat, !!onPlaylistAdvance)
    if (step.kind === 'track') { playFile(step.fileIndex); return }
    if (step.kind === 'wrap-rebuild') {
      const first = trackOrder.rebuildAndFirst()
      if (first != null) playFile(first)
      return
    }
    onPlaylistAdvance?.()
  }
  const handlePrev = () => {
    const step = prevTrack(trackOrder.order, selectedFile, repeat, !!onPlaylistPrevious)
    if (step.kind === 'track') { playFile(step.fileIndex); return }
    onPlaylistPrevious?.()
  }
  const hasNext = trackOrder.hasNext || !!onPlaylistAdvance || repeat === 'all'
  const hasPrev = trackOrder.hasPrev || !!onPlaylistPrevious || repeat === 'all'

  const handleVideoEnded = () => {
    // ACTIVE element: for audio it's SimpleAudioPlayer's <audio> (mirrored into
    // audioRef via elementRef), for video the <video>. Used to read videoRef only →
    // for audio it was null and repeat-one never replayed the track.
    const v = audioMode ? audioRef.current : videoRef.current
    // iOS/WebKit fires a SPURIOUS 'ended' when the direct-play <video> STALLS at
    // the start (stuck at readyState 2, playhead ~0) instead of actually ending.
    // Treating that as end would auto-advance to the next item (in order/shuffle) and
    // swap the src mid-start, aborting the pending play() — that was the
    // "switched track by itself + no sound" on the iPhone. It's only a real end when the
    // playhead got near the duration; with unknown duration (0/NaN) it advances
    // normally (there's no way to tell).
    // Real end ⇒ the playhead got near the duration. Two spurious patterns:
    //  (a) known duration and the playhead far from the end;
    //  (b) duration 0/NaN (freshly swapped element, not yet settled) with the
    //      playhead still at the beginning — the cross-item stall (mp3↔m4a) that PREVIOUSLY
    //      escaped the guard and made the list "skip" tracks on its own (churn). Without
    //      this, when unlocking auto-advance, the 2nd track crackled and advanced in a loop.
    const knownFarFromEnd = !!v && Number.isFinite(v.duration) && v.duration > 0 && v.currentTime < v.duration - 2
    const unknownDurAtStart = !!v && !(Number.isFinite(v.duration) && v.duration > 0) && v.currentTime < 1
    if (knownFarFromEnd || unknownDurAtStart) {
      clientLog('warn', 'player', 'spurious ended ignored', { currentTime: v?.currentTime, duration: v?.duration, readyState: v?.readyState })
      return
    }
    clientLog('info', 'player', 'video ended → advance', { repeat, nextIdx: mediaQueue.nextIdx, hasPlaylistAdvance: !!onPlaylistAdvance, audioMode })
    if (repeat === 'one') {
      if (v) { v.currentTime = 0; v.play().catch(() => {}) }
      return
    }
    // Continuous advance: next track/episode, else next playlist item.
    handleNext()
  }

  // ─── Simplified audio ─────────────────────────────────────────────────────
  // "Bare" audio player: <audio controls> with DIRECT src, no Web Audio,
  // no gapless/crossfade, no HLS.js, no <track>. The only difference between
  // a local source (rclone/disk) and torrent is the URL.
  const inPlaylist = !!playlist && playlist.items.length > 1
  const audioDirectSrc = useAudioDirectUrl(info, selectedFile, mediaToken)
  const activeMediaRef = audioMode ? audioRef : videoRef

  // Aggregated playlist sidebar (track list across items). The skeleton
  // persists when the sidebar closes (doesn't re-resolve ~47 tracks on reopen); the
  // resolution burst is gated by `sidebarOpen`. The old `resolveEnabled`/blessed was
  // removed: with preload='none' on iOS there's no byte-stream to choke.
  const aggregate = usePlaylistTracks(playlist?.items ?? [], playlist?.currentIndex ?? -1, info, inPlaylist && sidebarOpen)

  // Mirrors the <audio>'s currentTime/duration/onProgress into the player state.
  const handleAudioTimeUpdate = useCallback((currentTime: number, duration: number) => {
    setCurrentTime(currentTime)
    setDuration(duration)
    onProgress?.(currentTime)
  }, [onProgress])

  // Keyboard shortcuts control the active element (<audio> or <video>).
  useKeyboardShortcuts({
    videoRef: activeMediaRef,
    minimized,
    requestFullscreen: handleRequestFullscreen,
    onNext: handleNext,
    onPrev: handlePrev,
    chapters,
    setPlaybackSpeed,
    playbackSpeed,
  })

  // Media Session API — exposes metadata + lock-screen/AirPods controls.
  // Artwork for the lock screen (Now Playing) — ABSOLUTE URL because iOS fetches the
  // image outside the page's context. Covers local and torrent (audioCoverURL).
  const mediaArtworkURL = info ? `${globalThis.location?.origin ?? ''}${audioCoverURL(info, selectedFile, mediaToken)}` : ''
  useMediaSession({ videoRef: activeMediaRef, info, selectedFile, playlistName: playlist?.name, onNext: handleNext, onPrev: handlePrev, artworkURL: mediaArtworkURL })

  return {
    displayFiles, mediaQueue, trackOrder,
    playFile, handleNext, handlePrev, hasNext, hasPrev, handleVideoEnded,
    inPlaylist, audioDirectSrc, activeMediaRef, aggregate, handleAudioTimeUpdate,
  }
}
