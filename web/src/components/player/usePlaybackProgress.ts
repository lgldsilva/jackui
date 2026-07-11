import { useEffect, useRef } from 'react'
import { TorrentInfo, resolveArt } from '../../api/client'
import { tryPrefetchNext, updateBufferedRanges, tryAutoFavorite, trySaveResume, trySyncUrlPlayhead, autoDownloadNextFile } from './playerEffects'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// The per-timeupdate work (playhead/duration/buffered mirror, watch-time
// auto-favorite, silent resume save, URL playhead sync, next-item prefetch)
// plus the playback-speed, auto-download-next, art-resolve and scroll-to-file
// effects. Refs shared with the reset paths are passed in.
export function usePlaybackProgress(deps: {
  videoRef: React.RefObject<HTMLVideoElement | null>
  info: TorrentInfo | null
  selectedFile: number
  incognito: boolean
  serverReady: boolean
  sidebarOpen: boolean
  playbackSpeed: number
  mediaQueueNextIdx: number
  isFavorite: boolean
  autoFavThreshold: number
  libraryEntryID: number | null
  selectedFileRef: React.RefObject<HTMLButtonElement | null>
  watchedRef: React.MutableRefObject<number>
  lastTickRef: React.MutableRefObject<number>
  lastResumeSaveRef: React.MutableRefObject<number>
  prefetchedNextEpRef: React.MutableRefObject<boolean>
  prefetchedPlaylistN1Ref: React.MutableRefObject<boolean>
  prefetchedPlaylistN2Ref: React.MutableRefObject<boolean>
  autoDownloadDoneRef: React.MutableRefObject<Set<number>>
  enqueueNextDownloadRef: React.MutableRefObject<(fileIndex: number) => void>
  onProgress?: (sec: number) => void
  onPrefetchNextPlaylist?: () => void
  onPrefetchNextNextPlaylist?: () => void
  setCurrentTime: Setter<number>
  setDuration: Setter<number>
  setBufferedEnd: Setter<number>
  setBufferedRanges: Setter<Array<[number, number]>>
  setIsFavorite: Setter<boolean>
}) {
  const {
    videoRef, info, selectedFile, incognito, serverReady, sidebarOpen, playbackSpeed, mediaQueueNextIdx,
    isFavorite, autoFavThreshold, libraryEntryID, selectedFileRef,
    watchedRef, lastTickRef, lastResumeSaveRef, prefetchedNextEpRef, prefetchedPlaylistN1Ref,
    prefetchedPlaylistN2Ref, autoDownloadDoneRef, enqueueNextDownloadRef,
    onProgress, onPrefetchNextPlaylist, onPrefetchNextNextPlaylist,
    setCurrentTime, setDuration, setBufferedEnd, setBufferedRanges, setIsFavorite,
  } = deps

  const lastUrlSyncRef = useRef(0)

  // Track playback state + accumulate watch time for auto-favorite
  const handleTimeUpdate = () => {
    const v = videoRef.current
    if (!v) return
    const now = v.currentTime
    const delta = now - lastTickRef.current
    if (delta > 0 && delta < 2) watchedRef.current += delta
    lastTickRef.current = now
    setCurrentTime(now)
    setDuration(v.duration || 0)
    onProgress?.(now)
    updateBufferedRanges(v, now, setBufferedRanges, setBufferedEnd)
    tryAutoFavorite(watchedRef.current, isFavorite, autoFavThreshold, info, setIsFavorite)
    trySaveResume(now, incognito, libraryEntryID, lastResumeSaveRef, v.duration || 0)
    trySyncUrlPlayhead(now, lastUrlSyncRef)
    tryPrefetchNext({ v, now, nextVideoIdx: mediaQueueNextIdx, info, prefetchedNextEpRef, onPrefetchNextPlaylist, prefetchedPlaylistN1Ref, onPrefetchNextNextPlaylist, prefetchedPlaylistN2Ref })
  }

  // Apply playback speed + pitch preservation whenever the user changes it or
  // a new <video>/<audio> element mounts (i.e., when selectedFile changes).
  // preservesPitch is the modern spec; webkitPreservesPitch is the Safari/iOS
  // legacy attribute that's still required on some devices.
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    v.playbackRate = playbackSpeed
    v.preservesPitch = true
    // Safari/iOS legacy attribute — not in lib.dom but still needed on older WebKit.
    ;(v as unknown as { webkitPreservesPitch?: boolean }).webkitPreservesPitch = true
    localStorage.setItem('jackui.playbackSpeed', String(playbackSpeed))
  }, [playbackSpeed, selectedFile, info?.infoHash])

  // Auto-download the next file in the in-torrent queue when the current file
  // finishes streaming. Runs every time the poll updates `info` so it triggers
  // within 2s of completion. Pure logic lives in playerEffects.ts.
  useEffect(() => {
    autoDownloadNextFile({
      info,
      selectedFile,
      nextIdx: mediaQueueNextIdx,
      doneRef: autoDownloadDoneRef,
      incognito,
      onEnqueue: (idx) => enqueueNextDownloadRef.current(idx),
    })
  }, [info, selectedFile, mediaQueueNextIdx, incognito])

  // Resolve + persist a per-torrent thumbnail once playback is live. Gated by
  // serverReady so the torrent is active (embedded image / frame capture need a
  // live Reader). Idempotent server-side: skips re-processing if good art was
  // already persisted. Best-effort — never touches playback.
  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0 || !serverReady) return
    resolveArt(info.infoHash, selectedFile)
  }, [info?.infoHash, selectedFile, serverReady])

  // Scroll the file picker to the selected file (after it renders). Runs on
  // open and whenever the selection changes — a tiny delay lets the list mount.
  useEffect(() => {
    if (!sidebarOpen || selectedFile < 0) return
    const t = setTimeout(() => {
      selectedFileRef.current?.scrollIntoView({ block: 'center', behavior: 'auto' })
    }, 60)
    return () => clearTimeout(t)
  }, [sidebarOpen, selectedFile, info?.infoHash])

  return { handleTimeUpdate }
}
