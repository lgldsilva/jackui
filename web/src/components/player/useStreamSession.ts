import { useEffect, useRef } from 'react'
import {
  SearchResult,
  TorrentInfo,
  TranscodeCapabilities,
  streamAdd,
  streamMetadata,
  pickTorrentSource,
  streamInfo,
  streamViewerOpen,
  streamViewerClose,
  subtitlesEnabled,
  fetchMediaToken,
  transcodeCapabilities,
} from '../../api/client'
import { clientLog } from '../../lib/diag'
import { chooseInitialFile } from './playerEffects'
import type { TFn } from './playerTypes'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// Owns the streaming session lifecycle: media-token fetch, the authoritative
// streamAdd (+ metadata-cache preview) on open, the Cinema↔Music re-send, the
// 2s progress poll, and the viewer lease. Cross-cutting state resets for a new
// result live in `resetForNewResult` (called with the warm-hold flag) so this
// hook doesn't need to thread every setter the reset touches.
export function useStreamSession(deps: {
  result: SearchResult | null
  audioMode: boolean
  initialFileIndex?: number
  t: TFn
  info: TorrentInfo | null
  selectedFile: number
  caps: TranscodeCapabilities | null
  blessed: boolean
  setLoading: Setter<boolean>
  setError: Setter<string>
  setInfo: Setter<TorrentInfo | null>
  setSelectedFile: Setter<number>
  setServerReady: Setter<boolean>
  setMediaToken: Setter<string>
  setSubEnabled: Setter<boolean>
  setCaps: Setter<TranscodeCapabilities | null>
  setBlessed: Setter<boolean>
  resetForNewResult: (warmHold: boolean) => void
}) {
  const {
    result, audioMode, initialFileIndex, t, info, selectedFile, caps, blessed,
    setLoading, setError, setInfo, setSelectedFile, setServerReady, setMediaToken,
    setSubEnabled, setCaps, setBlessed, resetForNewResult,
  } = deps

  // streamAddDoneRef: has the (authoritative) streamAdd already resolved? Prevents the
  // metadata-cache preview from overwriting the authoritative result in the race.
  const streamAddDoneRef = useRef(false)
  // everReadyRef: flips true as soon as the player has shown content (info +
  // file) at least once in this instance. Enables the "warm hold" on music track
  // switch (see the [result] effect) and suppresses the start overlay.
  const everReadyRef = useRef(false)
  const prevAudioModeRef = useRef(audioMode)
  const pollRef = useRef<ReturnType<typeof globalThis.setInterval> | null>(null)

  // Asks for a media token (long-TTL JWT, scope="media") when the player opens.
  // Required BEFORE mounting <video src> so the URL doesn't change afterwards
  // (which would make the browser treat it as new media and reset to 0).
  // A background refresh of the regular access token doesn't affect this one — it
  // only expires after the whole playback session (6h default).
  useEffect(() => {
    if (!result) return
    let cancelled = false
    fetchMediaToken()
      .then(t => { if (!cancelled) setMediaToken(t) })
      .catch(() => {}) // fallback: streamURL stays empty, UI shows "loading"
    return () => { cancelled = true }
  }, [result?.infoHash])

  // Add the torrent when modal opens
  useEffect(() => {
    if (!result || !pickTorrentSource(result)) return

    // Guard against a slow streamAdd from the PREVIOUS result resolving after
    // we've switched to a new one — without it the old torrent's file list +
    // thumbnails clobber the new video. Flipped by the cleanup below.
    let cancelled = false

    // warmHold: on a MUSIC track switch with the player already populated, do NOT
    // unmount the UI (cover/seekbar/transport) nor cut the current audio — hold the
    // old `info`/`selectedFile`/`serverReady` (streamURL derives from `info`, so
    // the <video> stays on the current track) until the new one's streamMetadata/streamAdd
    // resolves; then the switch is atomic. Without this the screen "flashed" (the
    // "Connecting to swarm" overlay) on every track. AUDIO-only scope (video keeps the full
    // reset, no regression); cold start (1st track) also resets normally.
    const warmHold = everReadyRef.current && audioMode
    streamAddDoneRef.current = false

    resetForNewResult(warmHold)

    // Try the cached metadata first — if the server has seen this hash before,
    // the file list + name appear instantly. streamAdd still kicks off in
    // parallel to actually load the torrent client (required for playback).
    if (result.infoHash) {
      streamMetadata(result.infoHash).then(cached => {
        // streamAddDoneRef (not `info`): under warm hold the old `info` is still
        // set, so the cache preview MUST be able to overwrite it; it just can't
        // stomp on the authoritative streamAdd if that one already resolved.
        if (cancelled || !cached || streamAddDoneRef.current) return
        setInfo(cached)
        setSelectedFile(chooseInitialFile(cached, initialFileIndex))
      }).catch(() => {}) // cache probe only — streamAdd below is authoritative
    }

    streamAdd(pickTorrentSource(result), audioMode ? 'audio' : 'video')
      .then(t => {
        if (cancelled) return
        streamAddDoneRef.current = true
        setInfo(t)
        setSelectedFile(chooseInitialFile(t, initialFileIndex))
        // Streamer now has the torrent active — unblock <video src>.
        setServerReady(true)
      })
      .catch(err => { if (!cancelled) setError(err?.response?.data?.error || err.message || t('player.modal.streamStartFailed')) })
      .finally(() => { if (!cancelled) setLoading(false) })

    // Check whether subtitles backend is configured
    subtitlesEnabled().then(v => { if (!cancelled) setSubEnabled(v) }).catch(() => { if (!cancelled) setSubEnabled(false) })
    // Cache transcode capabilities once per modal — used by HEVC auto-fallback decision.
    if (!caps) {
      transcodeCapabilities().then(c => { if (!cancelled) setCaps(c) }).catch(() => { if (!cancelled) setCaps(null) })
    }

    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result?.infoHash])

  // Cinema ↔ Music on the same torrent: re-sends kind to the backend without restarting
  // the whole player (the effect above omits audioMode on purpose).
  useEffect(() => {
    if (!result?.infoHash || !everReadyRef.current) {
      prevAudioModeRef.current = audioMode
      return
    }
    if (prevAudioModeRef.current === audioMode) return
    prevAudioModeRef.current = audioMode
    let cancelled = false
    streamAdd(pickTorrentSource(result), audioMode ? 'audio' : 'video')
      .then(t => {
        if (cancelled) return
        streamAddDoneRef.current = true
        setInfo(t)
        setSelectedFile(cur => (cur >= 0 ? cur : chooseInitialFile(t, initialFileIndex)))
      })
      .catch(err => {
        if (!cancelled) setError(err?.response?.data?.error || err.message || t('player.modal.streamStartFailed'))
      })
    return () => { cancelled = true }
  }, [audioMode, result?.infoHash, initialFileIndex, result, t])

  // Marks that the player already rendered a track in this instance → enables the warm
  // hold (track switch without unmounting the UI) on subsequent switches.
  useEffect(() => {
    if (info && selectedFile >= 0) everReadyRef.current = true
  }, [info, selectedFile])

  // Poll progress every 2s while modal is open
  useEffect(() => {
    if (!info?.infoHash) return
    const tick = () => {
      // Skip while the tab is hidden (background audio is the common case): each
      // streamInfo rebuilds the torrent's buildInfo (dozens of BytesCompleted
      // on a multi-file pack). Resumes updating on its own when the tab regains focus.
      if (document.hidden) return
      streamInfo(info.infoHash).then(setInfo).catch(() => {})
    }
    pollRef.current = globalThis.setInterval(tick, 2000)
    return () => {
      if (pollRef.current) globalThis.clearInterval(pollRef.current)
    }
  }, [info?.infoHash])

  // Viewer lease: hold a lease on the active torrent while it's open and release
  // it when the hash changes (playlist/autoplay reuses this instance) or on
  // unmount. The backend keeps the torrent alive while ≥1 viewer holds a lease
  // (so a co-watcher closing one tab doesn't kill the others) and drops a
  // stream-only torrent shortly after the LAST viewer leaves — instead of
  // seeding idly until the reaper. local- hashes aren't streamer torrents.
  useEffect(() => {
    const hash = info?.infoHash
    if (!hash || hash.startsWith('local-')) return
    streamViewerOpen(hash).catch(() => {})
    return () => { streamViewerClose(hash).catch(() => {}) }
  }, [info?.infoHash])

  // <video> onPlaying: marks `blessed` (1st gesture-started playback on
  // iOS) ONCE and logs it. From then on auto-advance may play programmatically —
  // Apple's per-element grant survives the src-swap ("Auto-play restrictions are
  // granted on a per-element basis" + "change the source... instead of creating
  // multiple media elements"). Idempotent: onPlaying fires on every resume.
  const handlePlaybackStarted = () => {
    if (blessed) return
    clientLog('info', 'player', 'blessed: 1st playback started (auto-advance unlocked)', {})
    setBlessed(true)
  }

  return { handlePlaybackStarted }
}
