import { Dispatch, MutableRefObject, RefObject, SetStateAction, useEffect, useMemo, useState } from 'react'
import { interpretPlayerKey, nextSpeed } from '../../lib/playerShortcuts'
import { SPEED_OPTIONS } from './playerFormat'
import { findIntroSkip, shouldShowSkipIntro } from '../../lib/skipIntro'
import type { MediaChapter } from '../../api/stream-types'
import {
  StreamProbe,
  TorrentInfo,
  TranscodeCapabilities,
  SidecarSubtitle,
  streamProbe,
  streamSidecars,
} from '../../api/client'
import { clientLog } from '../../lib/diag'
import { fileKind } from '../../lib/playable'
import { load, save } from '../../lib/storage'

// Per-file subtitle choice persisted in localStorage (mirrors the type in
// PlayerModal). Kept local to avoid a circular import.
type SubChoiceLite = {
  readonly external: string | null
  readonly embedded: number | null
  readonly sidecar: number | null
  readonly offset: number
}

// Hooks extracted verbatim from PlayerModal to shrink that 2000+ line component
// and make these self-contained side effects independently readable/testable.
// Behavior is unchanged — same effect bodies, same dependency arrays.

type KeyboardShortcutsOpts = {
  // HTMLMediaElement (not HTMLVideoElement) to accept both the <video> and the
  // gapless engine's <audio> — only touches play/pause/seek/volume (common members).
  readonly videoRef: RefObject<HTMLMediaElement | null>
  readonly minimized: boolean
  readonly requestFullscreen: () => void
  readonly onNext?: () => void
  readonly onPrev?: () => void
  readonly chapters?: readonly MediaChapter[]
  readonly setPlaybackSpeed?: (v: number) => void
  readonly playbackSpeed?: number
}

// useKeyboardShortcuts wires space/arrows/M/F plus YouTube-style J/K/L, N/P,
// digits, I (skip intro) and W (PiP). Skipped while minimized, while typing,
// and when the media element itself has focus (native handler already acts).
export function useKeyboardShortcuts({
  videoRef, minimized, requestFullscreen, onNext, onPrev, chapters, setPlaybackSpeed, playbackSpeed = 1,
}: KeyboardShortcutsOpts) {
  useEffect(() => {
    if (minimized) return
    const handleKeyDown = (e: KeyboardEvent) => {
      const v = videoRef.current
      if (!v) return
      const tgt = e.target as HTMLElement | null
      if (tgt && (tgt.tagName === 'INPUT' || tgt.tagName === 'TEXTAREA' || tgt.tagName === 'SELECT' || tgt === v)) return
      const action = interpretPlayerKey(e.key)
      if (!action) return
      const dur = Number.isFinite(v.duration) ? v.duration : Infinity
      e.preventDefault()
      applyShortcutAction(v, action, { dur, requestFullscreen, onNext, onPrev, chapters, setPlaybackSpeed, playbackSpeed })
    }
    globalThis.addEventListener('keydown', handleKeyDown)
    return () => globalThis.removeEventListener('keydown', handleKeyDown)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [minimized, onNext, onPrev, chapters, playbackSpeed])
}

function applyShortcutAction(
  v: HTMLMediaElement,
  action: NonNullable<ReturnType<typeof interpretPlayerKey>>,
  ctx: {
    dur: number
    requestFullscreen: () => void
    onNext?: () => void
    onPrev?: () => void
    chapters?: readonly MediaChapter[]
    setPlaybackSpeed?: (n: number) => void
    playbackSpeed: number
  },
): void {
  switch (action.kind) {
    case 'toggle':
      if (v.paused) v.play().catch(() => {})
      else v.pause()
      return
    case 'seek':
      v.currentTime = Math.min(ctx.dur, Math.max(0, v.currentTime + action.delta))
      return
    case 'seekToFraction':
      if (Number.isFinite(ctx.dur) && ctx.dur !== Infinity) v.currentTime = ctx.dur * action.fraction
      return
    case 'volume':
      v.volume = Math.min(1, Math.max(0, v.volume + action.delta))
      return
    case 'mute':
      v.muted = !v.muted
      return
    case 'fullscreen':
      ctx.requestFullscreen()
      return
    case 'next':
      ctx.onNext?.()
      return
    case 'prev':
      ctx.onPrev?.()
      return
    case 'skipIntro': {
      const intro = findIntroSkip(ctx.chapters)
      if (intro && shouldShowSkipIntro(intro, v.currentTime)) v.currentTime = intro.endSec
      return
    }
    case 'pip': {
      const el = v as HTMLVideoElement & { requestPictureInPicture?: () => Promise<void>; webkitSetPresentationMode?: (m: string) => void }
      if (typeof el.requestPictureInPicture === 'function') el.requestPictureInPicture().catch(() => {})
      else el.webkitSetPresentationMode?.('picture-in-picture')
      return
    }
    case 'speed':
      ctx.setPlaybackSpeed?.(nextSpeed(ctx.playbackSpeed, action.delta, SPEED_OPTIONS))
  }
}

// WebKit-only AirPlay surface (Safari/iOS) — not present in lib.dom types.
interface WebKitAirPlayVideo {
  webkitShowPlaybackTargetPicker?: () => void
  webkitCurrentPlaybackTargetIsWireless?: boolean
}
type WebKitAvailabilityEvent = Event & { availability?: 'available' | 'not-available' }

export type AirPlayState = {
  /** A device is reachable on the network → worth showing the button. */
  readonly available: boolean
  /** Playback is currently routed to an AirPlay target. */
  readonly active: boolean
  /** Opens the native AirPlay route picker. */
  readonly show: () => void
}

// useAirPlay surfaces AirPlay state for the <video> via the WebKit API (Safari/
// iOS). The standard Remote Playback API doesn't cover AirPlay reliably in
// Safari, so we use webkit hooks: `webkitplaybacktargetavailabilitychanged`
// fires an initial state on registration plus every change, and
// `webkitShowPlaybackTargetPicker()` opens the native picker. Listeners are
// removed on cleanup — Apple warns that monitoring availability drains battery.
// `srcKey` (the stream URL) is a dep so the listeners re-attach when the <video>
// is remounted on a source/fallback change.
export function useAirPlay(videoRef: RefObject<HTMLVideoElement | null>, srcKey: string): AirPlayState {
  const [available, setAvailable] = useState(false)
  const [active, setActive] = useState(false)

  useEffect(() => {
    // NOSONAR: the assertion carries the webkit* methods (WebKitAirPlayVideo is a
    // standalone interface, not merged into HTMLVideoElement) — tsc needs it; S4325 is a false positive.
    const el = videoRef.current as (HTMLVideoElement & WebKitAirPlayVideo) | null // NOSONAR
    if (!el || typeof el.webkitShowPlaybackTargetPicker !== 'function') return
    const onAvail = (e: Event) => setAvailable((e as WebKitAvailabilityEvent).availability === 'available')
    const onWireless = () => setActive(!!el.webkitCurrentPlaybackTargetIsWireless)
    el.addEventListener('webkitplaybacktargetavailabilitychanged', onAvail)
    el.addEventListener('webkitcurrentplaybacktargetiswirelesschanged', onWireless)
    return () => {
      el.removeEventListener('webkitplaybacktargetavailabilitychanged', onAvail)
      el.removeEventListener('webkitcurrentplaybacktargetiswirelesschanged', onWireless)
    }
  }, [videoRef, srcKey])

  const show = () => {
    const el = videoRef.current as (HTMLVideoElement & WebKitAirPlayVideo) | null // NOSONAR: same as :92 (webkit* methods, S4325 false positive)
    el?.webkitShowPlaybackTargetPicker?.()
  }

  return { available, active, show }
}

export type MediaQueue = {
  /** File indices of playable files of the SAME kind as the current one, in file order. */
  readonly indices: number[]
  /** Position of selectedFile inside `indices` (-1 if not in queue). */
  readonly cursor: number
  /** File index of the previous track/episode, or -1 at the start. */
  readonly prevIdx: number
  /** File index of the next track/episode, or -1 at the end. */
  readonly nextIdx: number
}

// buildMediaQueue is the pure core of useMediaQueue: the playable files of the
// SAME kind as the current one (audio↔audio in an album, video↔video in a
// series) IN THE ORDER GIVEN, with prev/next around selectedFile. The caller
// passes the files in DISPLAY order (filterAndSortFiles) so the next/prev
// buttons walk the list exactly as the user sees it — torrents rarely store
// episodes in file order, and the old file-order queue jumped around.
export function buildMediaQueue(files: readonly TorrentInfo['files'][number][], selectedFile: number): MediaQueue {
  const cur = files.find(f => f.index === selectedFile)
  const curKind = cur ? fileKind(cur.path, cur.isVideo) : 'other'
  const indices = curKind === 'other'
    ? []
    : files.filter(f => fileKind(f.path, f.isVideo) === curKind).map(f => f.index)
  const cursor = indices.indexOf(selectedFile)
  const prevIdx = cursor > 0 ? indices[cursor - 1] : -1
  const nextIdx = cursor >= 0 && cursor < indices.length - 1 ? indices[cursor + 1] : -1
  return { indices, cursor, prevIdx, nextIdx }
}

// useMediaQueue builds the in-torrent track/episode queue for the file
// currently playing. `orderedFiles` (when given) is the sidebar's display
// order — queue and list must agree; without it, falls back to file order.
export function useMediaQueue(info: TorrentInfo | null, selectedFile: number, orderedFiles?: readonly TorrentInfo['files'][number][]): MediaQueue {
  return useMemo(
    () => buildMediaQueue(orderedFiles ?? info?.files ?? [], selectedFile),
    [info, selectedFile, orderedFiles],
  )
}

type MediaSessionOpts = {
  // HTMLMediaElement: accepts the <video> or the gapless engine's active <audio>.
  readonly videoRef: RefObject<HTMLMediaElement | null>
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly playlistName?: string
  readonly onNext?: () => void
  readonly onPrev?: () => void
  // ABSOLUTE artwork URL (iOS fetches the image at OS level). Empty = no artwork.
  readonly artworkURL?: string
}

// useMediaSession exposes "what's playing" + media keys / lock-screen controls
// to the OS. Without it, iOS shows "JackUI" with no metadata and AirPods/
// bluetooth controls don't fire next/previous on the playlist.
export function useMediaSession({ videoRef, info, selectedFile, playlistName, onNext, onPrev, artworkURL }: MediaSessionOpts) {
  // Metadata (title + artwork) only changes with the TRACK/artwork — kept in an effect
  // with STABLE deps only. The action handlers (onNext/onPrev, recreated every
  // render since they aren't memoized) used to be in the same effect, so metadata
  // was re-emitted on every onTimeUpdate (~4x/s) and the OS re-downloaded the artwork each time
  // (seen in the logs: thousands of GET /api/local/audio/cover in a single session).
  useEffect(() => {
    if (!info || selectedFile < 0) return
    if (!('mediaSession' in navigator)) return
    const file = info.files[selectedFile]
    const title = file?.path?.split('/').pop() || info.name
    // artwork: 96 (compact player) + 512 (expanded) pointing to the same artwork —
    // the route serves a single image; iOS picks (see MDN/dbushell). type is a hint only.
    const artwork = artworkURL
      ? [
          { src: artworkURL, sizes: '96x96', type: 'image/jpeg' },
          { src: artworkURL, sizes: '512x512', type: 'image/jpeg' },
        ]
      : []
    navigator.mediaSession.metadata = new MediaMetadata({
      title,
      album: playlistName || info.name,
      artist: 'JackUI',
      artwork,
    })
  }, [info?.infoHash, selectedFile, playlistName, artworkURL])

  // Action handlers (media keys / lock-screen). Re-registering is cheap (no network),
  // so this effect may freely re-run when the track callbacks change.
  useEffect(() => {
    if (!('mediaSession' in navigator)) return
    const v = () => videoRef.current
    navigator.mediaSession.setActionHandler('play', () => { v()?.play().catch(() => {}) })
    navigator.mediaSession.setActionHandler('pause', () => { v()?.pause() })
    navigator.mediaSession.setActionHandler('previoustrack', () => onPrev?.())
    navigator.mediaSession.setActionHandler('nexttrack', () => onNext?.())
    navigator.mediaSession.setActionHandler('seekto', (d) => {
      const el = v()
      if (el && d.seekTime != null) el.currentTime = d.seekTime
    })
    return () => {
      try {
        navigator.mediaSession.setActionHandler('play', null)
        navigator.mediaSession.setActionHandler('pause', null)
        navigator.mediaSession.setActionHandler('previoustrack', null)
        navigator.mediaSession.setActionHandler('nexttrack', null)
        navigator.mediaSession.setActionHandler('seekto', null)
      } catch {}
    }
  }, [videoRef, onNext, onPrev])
}

type SubtitleOffsetOpts = {
  readonly videoRef: RefObject<HTMLVideoElement>
  readonly subActive: string | null
  readonly embeddedSub: number | null
  readonly sidecarIdx: number | null
  readonly localEmbeddedVttURL: string
  readonly subOffset: number
  readonly origCuesRef: MutableRefObject<{ start: number; end: number }[]>
}

// useSubtitleOffset forces the text track to show and applies the user's sync
// offset to every cue, snapshotting the original timings once per loaded sub
// so repeated offset changes stay relative to the source.
//
// Activates for ANY subtitle that becomes a <track>: external (subActive), embedded
// (embeddedSub) or sidecar (sidecarIdx). It used to run for external only, so
// embedded/sidecar never got `track.mode = 'showing'` — on Safari with native HLS
// the <track>'s `default` attribute is NOT enough when the src arrives later
// (blob of the on-demand extracted embedded subtitle), and the track loaded invisible.
// `localEmbeddedVttURL` joins the deps because the local blob is async: when
// it finally arrives, the effect re-runs and activates the track already with src.
export function useSubtitleOffset({ videoRef, subActive, embeddedSub, sidecarIdx, localEmbeddedVttURL, subOffset, origCuesRef }: SubtitleOffsetOpts) {
  useEffect(() => {
    const v = videoRef.current
    const hasTrackSub = subActive !== null || embeddedSub !== null || sidecarIdx !== null
    if (!v || !hasTrackSub) return

    const applyOffset = () => {
      const track = v.textTracks?.[0]
      if (!track) return
      // Activate FIRST, always: without this Safari doesn't display a <track> whose src
      // arrived dynamically, even with `default`.
      track.mode = 'showing'
      if (!track.cues?.length) return
      // Save originals once per loaded sub
      if (origCuesRef.current.length !== track.cues.length) {
        origCuesRef.current = Array.from(track.cues).map((c: any) => ({
          start: c.startTime,
          end: c.endTime,
        }))
      }
      Array.from(track.cues).forEach((cue: any, i) => {
        const orig = origCuesRef.current[i]
        if (!orig) return
        cue.startTime = Math.max(0, orig.start + subOffset)
        cue.endTime = Math.max(0, orig.end + subOffset)
      })
    }

    // Try now, and again when the track finishes loading
    applyOffset()
    const tracks = v.textTracks
    const onLoad = () => applyOffset()
    for (const track of tracks) {
      track.addEventListener('cuechange', onLoad)
    }
    return () => {
      for (const track of tracks) {
        track.removeEventListener('cuechange', onLoad)
      }
    }
  }, [subActive, embeddedSub, sidecarIdx, localEmbeddedVttURL, subOffset])

  // Reset original cue timings when the active subtitle changes (any source).
  useEffect(() => {
    origCuesRef.current = []
  }, [subActive, embeddedSub, sidecarIdx])
}

type TrackProbeOpts = {
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly serverReady: boolean
  readonly subActive: string | null
  readonly embeddedSub: number | null
  readonly setProbe: Dispatch<SetStateAction<StreamProbe | null>>
  readonly setEmbeddedSub: Dispatch<SetStateAction<number | null>>
  readonly setAutoSource: Dispatch<SetStateAction<'hash' | 'title' | 'embedded' | null>>
  readonly setSidecars: Dispatch<SetStateAction<SidecarSubtitle[]>>
  readonly setSidecarIdx: Dispatch<SetStateAction<number | null>>
}

// useTrackProbe runs ffprobe (embedded tracks) + sidecar discovery once the
// torrent is live, auto-picking a pt subtitle unless the user already saved a
// choice for this file. Extracted verbatim from PlayerModal — same gating,
// same stale-closure-safe storage read, same deps.
export function useTrackProbe(opts: TrackProbeOpts) {
  const { info, selectedFile, serverReady, subActive, embeddedSub,
    setProbe, setEmbeddedSub, setAutoSource, setSidecars, setSidecarIdx } = opts
  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0 || !serverReady) return
    // If the user previously chose a subtitle for THIS file, skip auto-load —
    // the restore effect applies that choice and it must win over pt auto-pick.
    // Read storage directly (not state) to avoid stale-closure races with the
    // async probe callback.
    const savedChoice = load<SubChoiceLite | null>(`sub.${info.infoHash}.${selectedFile}`, null)
    const hasSavedChoice = !!savedChoice && (savedChoice.external !== null || savedChoice.embedded !== null || savedChoice.sidecar !== null)

    streamProbe(info.infoHash, selectedFile)
      .then(p => {
        const safe = { audio: p.audio ?? [], subtitles: p.subtitles ?? [] }
        setProbe(safe)
        const ptSub = safe.subtitles.find(t => !t.image && /^(pt|por)/i.test(t.language || ''))
        if (ptSub && !hasSavedChoice && !subActive) {
          setEmbeddedSub(ptSub.index)
          setAutoSource('embedded')
        }
      })
      .catch(err => console.warn('probe failed:', err?.response?.data?.error || err.message))

    // Sidecar subtitle files (separate .srt in the torrent) — cheap, parallel with probe
    streamSidecars(info.infoHash, selectedFile)
      .then(list => {
        setSidecars(list ?? [])
        // Auto-pick pt sidecar if no embedded already chosen and no saved choice
        if (!hasSavedChoice && !subActive && embeddedSub === null && list && list.length > 0) {
          const pt = list.find(s => /^(pt|por)/i.test(s.language || ''))
          if (pt) {
            setSidecarIdx(pt.index)
            setAutoSource('embedded')
          }
        }
      })
      .catch(() => setSidecars([]))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash, selectedFile, serverReady])
}

type SubtitleChoicePersistOpts = {
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly subRestored: boolean
  readonly subActive: string | null
  readonly embeddedSub: number | null
  readonly sidecarIdx: number | null
  readonly subOffset: number
  readonly setSubActive: Dispatch<SetStateAction<string | null>>
  readonly setEmbeddedSub: Dispatch<SetStateAction<number | null>>
  readonly setSidecarIdx: Dispatch<SetStateAction<number | null>>
  readonly setSubOffset: Dispatch<SetStateAction<number>>
  readonly setAutoSource: Dispatch<SetStateAction<'hash' | 'title' | 'embedded' | null>>
  readonly setSubRestored: Dispatch<SetStateAction<boolean>>
}

// useSubtitleChoicePersist restores the saved subtitle choice for the current
// file (before the pt auto-load can fire), then persists changes back to
// localStorage. Extracted verbatim from PlayerModal — same deps & gating.
export function useSubtitleChoicePersist(opts: SubtitleChoicePersistOpts) {
  const { info, selectedFile, subRestored, subActive, embeddedSub, sidecarIdx, subOffset,
    setSubActive, setEmbeddedSub, setSidecarIdx, setSubOffset, setAutoSource, setSubRestored } = opts

  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0 || subRestored) return
    const saved = load<SubChoiceLite | null>(`sub.${info.infoHash}.${selectedFile}`, null)
    if (saved) {
      setSubActive(saved.external)
      setEmbeddedSub(saved.embedded)
      setSidecarIdx(saved.sidecar)
      setSubOffset(saved.offset || 0)
      if (saved.external !== null || saved.embedded !== null || saved.sidecar !== null) {
        setAutoSource(null)
      }
    }
    setSubRestored(true)
  }, [info?.infoHash, selectedFile, subRestored])

  useEffect(() => {
    if (!subRestored || !info?.infoHash || selectedFile < 0) return
    save<SubChoiceLite>(`sub.${info.infoHash}.${selectedFile}`, {
      external: subActive,
      embedded: embeddedSub,
      sidecar: sidecarIdx,
      offset: subOffset,
    })
  }, [subActive, embeddedSub, sidecarIdx, subOffset, subRestored, info?.infoHash, selectedFile])
}

type HevcBackstopOpts = {
  readonly videoRef: RefObject<HTMLVideoElement>
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly audioMode: boolean
  readonly transcodeAudio: number | null
  readonly forceH264: boolean
  readonly burnSubTrack: number | null
  readonly transcodeFallbackAttempted: boolean
  readonly videoError: boolean
  readonly bufferedEnd: number
  // From the ffprobe (#16): true=codec needs transcode, false=browser-safe,
  // undefined=probe hasn't arrived yet. Locks the backstop when it's already known that the
  // codec is browser-safe — then a stall is network/moov, not codec rejection.
  readonly needsTranscode?: boolean
  readonly caps: TranscodeCapabilities | null
  readonly videoDiagnostic: () => Record<string, unknown> | { reason: string }
  readonly setTranscodeFallbackAttempted: Dispatch<SetStateAction<boolean>>
  readonly setForceH264: Dispatch<SetStateAction<boolean>>
}

// backstopStuck: after 20s, readyState < 2 (nothing playable) + currentTime < 0.1
// (hasn't moved a frame) + buffered ~0. Each condition alone is benign during
// normal buffering; the three together for 20s smell like a problem.
export function backstopStuck(readyState: number, currentTime: number, bufferedEnd: number): boolean {
  return readyState < 2 && currentTime < 0.1 && bufferedEnd < 0.5
}

// startGapNudgeTarget: Safari (native HLS, EVENT/live path of transcoded local
// files) sometimes buffers the first segment starting a hair
// AFTER 0 (sub-tick PTS residue the MPEG-TS muxer leaves even with
// -muxdelay 0; observed: buffered.start = 0.000002). currentTime sits
// EXACTLY at 0 — right BEFORE buffered.start(0) — so Safari never
// reaches readyState 3, `canplay` doesn't fire, autoplay doesn't run and the video
// stalls at t=0 with seconds already buffered (symptom: "jumped to live but didn't
// play"). Detects exactly that shape (stuck at ~0, with the 1st range
// starting in (currentTime, gapMax]) and returns the nudge target; the caller
// advances currentTime into the buffer. gapMax (1.5s) covers everything from the
// µs residue up to the muxer's historical 1.4s initial_offset.
export function startGapNudgeTarget(currentTime: number, bufferedStart: number | null): number | null {
  if (currentTime > 0.25) return null        // already moved / past the initial hole
  if (bufferedStart === null) return null
  const gap = bufferedStart - currentTime
  if (gap <= 0 || gap > 1.5) return null      // no hole, or too large to be the PTS residue
  return bufferedStart + 0.05                 // lands 50ms inside the buffered range
}

// backstopShouldFire decides whether the backstop should FORCE transcode (h264) on a stall.
// Rule (#16): if the probe already confirmed a browser-safe codec (needsTranscode===false),
// the stall is network/moov — transcoding H264→H264 from the same cold source doesn't help →
// do NOT fire. If the codec needs transcode (true) or is unknown
// (undefined, probe hasn't arrived yet), fire — as long as there's a GPU encoder.
export function backstopShouldFire(stuck: boolean, needsTranscode: boolean | undefined, hasEncoder: boolean): boolean {
  if (!stuck) return false
  if (needsTranscode === false) return false
  return hasEncoder
}

// hlsFatalAction decides, without touching the Hls object, which recovery to apply to an
// hls.js FATAL error: NETWORK_ERROR → reload (startLoad), MEDIA_ERROR →
// recoverMediaError, anything else → destroy. `types` is the Hls.ErrorTypes enum
// (passed in to avoid coupling this pure/testable module to the hls.js import).
export function hlsFatalAction(
  type: string,
  types: { NETWORK_ERROR: string; MEDIA_ERROR: string },
): 'startLoad' | 'recoverMedia' | 'destroy' {
  if (type === types.NETWORK_ERROR) return 'startLoad'
  if (type === types.MEDIA_ERROR) return 'recoverMedia'
  return 'destroy'
}

// useHevcBackstop is the Safari silent-HEVC-failure backstop: after 20s with no
// playable frame it fires the same fallback <video onError> would. Extracted
// verbatim from PlayerModal — same 20s window, same gating, same deps.
export function useHevcBackstop(opts: HevcBackstopOpts) {
  const { videoRef, info, selectedFile, audioMode, transcodeAudio, forceH264, burnSubTrack,
    transcodeFallbackAttempted, videoError, bufferedEnd, needsTranscode, caps, videoDiagnostic,
    setTranscodeFallbackAttempted, setForceH264 } = opts
  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0) return
    const transcodingActive = transcodeAudio !== null || forceH264 || burnSubTrack !== null
    // Audio files don't need H264 transcoding — skip backstop entirely.
    if (audioMode || transcodingActive || transcodeFallbackAttempted || videoError) return
    const timer = globalThis.setTimeout(() => {
      const v = videoRef.current
      if (!v) return
      const stuck = backstopStuck(v.readyState, v.currentTime, bufferedEnd)
      const hasEncoder = !!(caps && (caps.hasNvidia || caps.hasVaapi || caps.hasQsv))
      clientLog('info', 'player', '20s backstop tick', { stuck, readyState: v.readyState, currentTime: v.currentTime, bufferedEnd, needsTranscode, src: v.currentSrc })
      if (stuck) {
        // Did the probe (#16) already confirm a browser-safe codec (H264/AAC/MP4)? Then
        // this stall (readyState 0, buffered ~0) is a network/moov problem — e.g.:
        // the MP4 moov hasn't downloaded yet — NOT Safari's silent HEVC
        // failure. Transcoding H264→H264 reading the SAME cold source doesn't speed
        // anything up and only adds latency. Don't fire the fallback.
        if (needsTranscode === false) {
          clientLog('info', 'player', 'backstop skipped — codec browser-safe (probe); stall is network/moov, not codec', { needsTranscode, readyState: v.readyState, bufferedEnd })
          return
        }
        if (backstopShouldFire(stuck, needsTranscode, hasEncoder)) {
          clientLog('warn', 'player', 'backstop firing fallback — Safari silent HEVC path likely', videoDiagnostic())
          setTranscodeFallbackAttempted(true)
          setForceH264(true)
        } else {
          clientLog('warn', 'player', 'backstop wanted to fallback but no GPU encoder available', { caps })
        }
      }
    }, 20000)
    return () => globalThis.clearTimeout(timer)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash, selectedFile, transcodeAudio, forceH264, burnSubTrack, transcodeFallbackAttempted, videoError, needsTranscode, caps])
}
