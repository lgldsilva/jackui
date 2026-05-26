import { useState, useEffect, useRef } from 'react'
import { X, Play, Loader2, AlertCircle, FileVideo, Download, ExternalLink, Users, Activity, Subtitles, Check, Maximize2, Minimize2, Minus, Plus, RotateCcw, SkipBack, SkipForward, Rewind, FastForward, Cpu, Volume2, Flame, Heart, ChevronLeft, ChevronRight, ListMusic, Shuffle, Repeat } from 'lucide-react'
import {
  SearchResult,
  TorrentInfo,
  Subtitle,
  StreamProbe,
  TranscodeOpts,
  SidecarSubtitle,
  TranscodeCapabilities,
  streamAdd,
  streamMetadata,
  pickTorrentSource,
  streamInfo,
  streamDrop,
  streamFileURL,
  streamHLSMasterURL,
  streamArtworkURL,
  isSafariBrowser,
  streamProbe,
  streamSubtrackURL,
  streamTranscodeURL,
  streamSidecars,
  streamSidecarURL,
  streamPlaylistM3UURL,
  streamPrefetch,
  streamThumbnailURL,
  subtitlesEnabled,
  subtitlesSearch,
  subtitlesAuto,
  subtitleDownloadURL,
  transcodeCapabilities,
  favoriteAdd,
  favoriteRemove,
  favoritesList,
  libraryGet,
  libraryUpdateResume,
} from '../api/client'
import { formatRate } from '../lib/format'
import { clientLog } from '../lib/diag'
import FilePreviewModal, { detectPreviewKind } from './FilePreviewModal'

interface PlaylistMeta {
  name: string
  items: { title: string }[]
  currentIndex: number
}

interface PlayerModalProps {
  result: SearchResult | null
  onClose: () => void
  /** Optional override of the auto-selected file (primaryFile).
   *  Used when caller already knows which file (e.g., picked from contents view, episode N of a series). */
  initialFileIndex?: number
  /** Optional seek-to position (seconds) applied once the video can play.
   *  Takes precedence over the per-user DB resume position when both exist —
   *  the URL-encoded value is more explicit (a shared timestamp) than the
   *  silent DB value. Falls through to DB resume when undefined. */
  initialSeek?: number
  /** When set, the player is part of a playlist sequence. The header shows
   *  "X de Y · Playlist Name" and onEnded falls through to onPlaylistAdvance
   *  once the local file-sequence inside the torrent is exhausted. */
  playlist?: PlaylistMeta | null
  onPlaylistAdvance?: () => void
  onPlaylistPrevious?: () => void
  repeat?: 'none' | 'one' | 'all'
  shuffle?: boolean
  onCycleRepeat?: () => void
  onToggleShuffle?: () => void
  /** Called once when the current item passes ~50% — warms up next playlist item. */
  onPrefetchNextPlaylist?: () => void
  /** Called once when the current item passes ~85% — warms up the item after the next. */
  onPrefetchNextNextPlaylist?: () => void
  /** Open in minimized (compact floating card) mode. Used for audio, which
   *  replaces the old bottom AudioBar — the player opens as a small dock. */
  startMinimized?: boolean
  /** Audio content: in minimized mode we show cover art over the (black)
   *  video element since there's nothing to display. */
  audioMode?: boolean
}

function formatSize(bytes: number): string {
  if (bytes === 0 || !bytes) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

export default function PlayerModal({
  result,
  onClose,
  initialFileIndex,
  initialSeek,
  playlist = null,
  onPlaylistAdvance,
  onPlaylistPrevious,
  repeat = 'none',
  shuffle = false,
  onCycleRepeat,
  onToggleShuffle,
  onPrefetchNextPlaylist,
  onPrefetchNextNextPlaylist,
  startMinimized = false,
  audioMode = false,
}: PlayerModalProps) {
  const [info, setInfo] = useState<TorrentInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [selectedFile, setSelectedFile] = useState<number>(-1)
  const [videoError, setVideoError] = useState(false)
  // Minimized (picture-in-picture-style) mode. The <video> element stays
  // mounted in the same DOM position — only the surrounding container shrinks
  // to a floating card and the heavy panels hide. This unifies the modal and
  // the old AudioBar into one player: audio opens minimized, video opens full,
  // and either can toggle. Since PlayerProvider lives above the router, the
  // player keeps playing across page navigation in both modes.
  const [minimized, setMinimized] = useState(startMinimized)
  // Subtitles
  const [subEnabled, setSubEnabled] = useState(false)
  const [subOpen, setSubOpen] = useState(false)
  const [subResults, setSubResults] = useState<Subtitle[]>([])
  const [subLoading, setSubLoading] = useState(false)
  const [subError, setSubError] = useState('')
  const [subActive, setSubActive] = useState<string | null>(null)
  const [subOffset, setSubOffset] = useState(0) // seconds; +/-0.1s steps
  const [autoSource, setAutoSource] = useState<'hash' | 'title' | 'embedded' | null>(null)
  // Embedded tracks discovered via ffprobe
  const [probe, setProbe] = useState<StreamProbe | null>(null)
  const [embeddedSub, setEmbeddedSub] = useState<number | null>(null) // selected embedded sub track index

  // Sidecar subtitle files (separate .srt/.vtt inside the torrent)
  const [sidecars, setSidecars] = useState<SidecarSubtitle[]>([])
  const [sidecarIdx, setSidecarIdx] = useState<number | null>(null) // selected sidecar file index

  // Library entry for this torrent — used for resume seek + saving position
  const [libraryEntryID, setLibraryEntryID] = useState<number | null>(null)
  const [resumePosition, setResumePosition] = useState<number | null>(null)
  const lastResumeSaveRef = useRef(0)
  const lastUrlSyncRef = useRef(0)  // throttle for writing ?t= into the URL
  const bufferRetryRef = useRef(0)  // bounded auto-retries while swarm still delivers

  // Sidebar (file list) open/closed state. On lg+ screens the file picker
  // renders as a right column instead of a stacked panel below the video.
  // Persisted to localStorage so the user's choice survives between modals.
  const [sidebarOpen, setSidebarOpen] = useState<boolean>(() => {
    const stored = localStorage.getItem('jackui.playerSidebar')
    return stored === null ? true : stored === '1'
  })
  useEffect(() => {
    localStorage.setItem('jackui.playerSidebar', sidebarOpen ? '1' : '0')
  }, [sidebarOpen])

  // serverReady — flips true the moment streamAdd resolves and the streamer has
  // actually loaded the torrent. The metadata cache lets us populate `info`
  // (file list, primaryFile) instantly from disk, but the <video src> can't
  // start fetching pieces until the streamer has the torrent in its `active`
  // map — otherwise /api/stream/HASH/IDX returns 404 and the browser fires a
  // misleading "format not supported" error before swarm bootstrap completes.
  const [serverReady, setServerReady] = useState(false)
  // Frozen snapshot of the diagnostic at the moment onVideoError fired. Used by
  // the error UI which re-renders AFTER the <video> element unmounted, so by
  // then videoRef.current is null and a live diagnostic would come back empty.
  const [lastErrorDiag, setLastErrorDiag] = useState<Record<string, unknown> | null>(null)
  // Inline preview for non-playable files (txt/srt/nfo/pdf/jpg/etc).
  // Storing the file index lets us look up path + size from `info.files`
  // on render without duplicating state when the user reopens the player.
  const [previewFileIdx, setPreviewFileIdx] = useState<number | null>(null)
  // Transcoding options — any non-null value triggers `/api/stream/transcode` instead of raw stream
  const [transcodeAudio, setTranscodeAudio] = useState<number | null>(null)
  const [forceH264, setForceH264] = useState(false)
  const [burnSubTrack, setBurnSubTrack] = useState<number | null>(null)
  // HEVC auto-fallback: on first <video> error, if a GPU encoder is available, retry via transcode.
  // The "Attempted" flag prevents an infinite loop if the transcoded stream also errors.
  const [transcodeFallbackAttempted, setTranscodeFallbackAttempted] = useState(false)
  const [caps, setCaps] = useState<TranscodeCapabilities | null>(null)
  // Variable playback speed for audiobooks / lectures. We persist this in
  // localStorage so it survives modal close and across sessions. We rely on
  // the browser's built-in pitch-preservation (preservesPitch / webkitPreservesPitch)
  // so 1.5x/2x doesn't sound chipmunked.
  const [playbackSpeed, setPlaybackSpeed] = useState<number>(() => {
    const stored = parseFloat(localStorage.getItem('jackui.playbackSpeed') || '1')
    return isFinite(stored) && stored > 0 ? stored : 1
  })
  const SPEED_OPTIONS = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3] as const
  // File list filter — for series packs with 30+ episodes the list pushes the
  // settings off-screen. The filter keeps the list short so settings stay reachable.
  const [fileFilter, setFileFilter] = useState('')
  // Hover preview state for the seek bar. `seekHover` is the time (s) the user
  // is pointing at; nullable so we don't render the bubble while idle.
  const [seekHover, setSeekHover] = useState<{ time: number; x: number } | null>(null)

  // Favorites — auto-mark after 5min of actual playback (currentTime accumulates)
  const [isFavorite, setIsFavorite] = useState(false)
  const watchedRef = useRef(0)            // accumulated playback time (seconds)
  const lastTickRef = useRef<number>(0)   // last currentTime sample (for delta)
  const AUTO_FAV_THRESHOLD = 5 * 60       // 5 minutes
  // Playback state
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [bufferedEnd, setBufferedEnd] = useState(0)
  const videoRef = useRef<HTMLVideoElement>(null)
  const pollRef = useRef<number | null>(null)
  // Prefetch fire-once flags. Reset whenever the underlying selected file
  // changes so re-watching a playlist item or switching files starts fresh.
  const prefetchedNextEpRef = useRef(false)
  const prefetchedPlaylistN1Ref = useRef(false)
  const prefetchedPlaylistN2Ref = useRef(false)
  // Store the original (un-offset) cue timings the first time we see them
  const origCuesRef = useRef<{ start: number; end: number }[]>([])

  // Add the torrent when modal opens
  useEffect(() => {
    if (!result || !pickTorrentSource(result)) return

    setLoading(true)
    setError('')
    setInfo(null)
    setSelectedFile(-1)
    setVideoError(false)
    setSubActive(null)
    setSubResults([])
    setSubError('')
    setSubOpen(false)
    setSubOffset(0)
    setAutoSource(null)
    setProbe(null)
    setEmbeddedSub(null)
    setSidecars([])
    setSidecarIdx(null)
    setLibraryEntryID(null)
    setResumePosition(null)
    lastResumeSaveRef.current = 0
    setServerReady(false)
    setTranscodeAudio(null)
    setForceH264(false)
    setBurnSubTrack(null)
    setTranscodeFallbackAttempted(false)
    prefetchedNextEpRef.current = false
    prefetchedPlaylistN1Ref.current = false
    prefetchedPlaylistN2Ref.current = false
    bufferRetryRef.current = 0
    setFileFilter('')
    origCuesRef.current = []
    setCurrentTime(0)
    setDuration(0)
    setBufferedEnd(0)

    // Try the cached metadata first — if the server has seen this hash before,
    // the file list + name appear instantly. streamAdd still kicks off in
    // parallel to actually load the torrent client (required for playback).
    if (result.infoHash) {
      streamMetadata(result.infoHash).then(cached => {
        if (cached && !info) {
          setInfo(cached)
          const chosen =
            initialFileIndex !== undefined && initialFileIndex >= 0 && initialFileIndex < cached.files.length
              ? initialFileIndex
              : (cached.primaryFile >= 0 ? cached.primaryFile : 0)
          setSelectedFile(chosen)
        }
      })
    }

    streamAdd(pickTorrentSource(result))
      .then(t => {
        setInfo(t)
        // Honor explicit override; fall back to backend-suggested primary; else first file
        const chosen =
          initialFileIndex !== undefined && initialFileIndex >= 0 && initialFileIndex < t.files.length
            ? initialFileIndex
            : (t.primaryFile >= 0 ? t.primaryFile : 0)
        setSelectedFile(chosen)
        // Streamer now has the torrent active — unblock <video src>.
        setServerReady(true)
      })
      .catch(err => setError(err?.response?.data?.error || err.message || 'Falha ao iniciar stream'))
      .finally(() => setLoading(false))

    // Check whether subtitles backend is configured
    subtitlesEnabled().then(setSubEnabled).catch(() => setSubEnabled(false))
    // Cache transcode capabilities once per modal — used by HEVC auto-fallback decision.
    if (!caps) {
      transcodeCapabilities().then(setCaps).catch(() => setCaps(null))
    }
  }, [result])

  // Diagnostic snapshot helper. Returns a plain object with the MediaError code,
  // network state, ready state, current src and user-agent details — everything
  // needed to debug "format not supported" reports without back-and-forth.
  // Codes per HTML spec:
  //   MediaError.code: 1=ABORTED, 2=NETWORK, 3=DECODE, 4=SRC_NOT_SUPPORTED
  //   networkState:    0=EMPTY, 1=IDLE,  2=LOADING, 3=NO_SOURCE
  //   readyState:      0=NOTHING, 1=METADATA, 2=CURRENT, 3=FUTURE, 4=ENOUGH
  const videoDiagnostic = () => {
    const v = videoRef.current
    if (!v) return { reason: 'no video element' }
    return {
      errorCode: v.error?.code,
      errorMsg: v.error?.message,
      networkState: v.networkState,
      readyState: v.readyState,
      currentSrc: v.currentSrc,
      duration: v.duration,
      currentTime: v.currentTime,
      buffered: v.buffered.length > 0 ? `${v.buffered.start(0)}-${v.buffered.end(0)}` : 'empty',
      forceH264,
      transcodeFallbackAttempted,
      isTranscoded,
      caps: caps ? { nvidia: caps.hasNvidia, vaapi: caps.hasVaapi, qsv: caps.hasQsv, preferred: caps.preferred } : null,
      ua: navigator.userAgent,
    }
  }

  // HEVC / unsupported codec auto-fallback. The native <video onError> fires for HEVC, AV1,
  // VP9-in-MKV, etc. — anything Safari/Chrome can't decode. If a GPU encoder is available we
  // retry through /api/stream/transcode (force h264) silently. The "Attempted" flag prevents
  // looping when the transcoded stream itself errors (then the real error UI shows).
  //
  // Diagnostic logs are intentionally verbose — Safari HEVC silent failures are
  // notoriously hard to reproduce locally and we want enough context to debug
  // from a single user report (paste the console output).
  const onVideoError = () => {
    const diag = videoDiagnostic()
    clientLog('warn', 'player', 'video onError fired', diag)
    // Freeze a copy so the error UI (which re-renders after <video> unmounts)
    // still has populated values instead of "—" placeholders.
    setLastErrorDiag(diag as Record<string, unknown>)
    if (transcodeFallbackAttempted || forceH264 || !caps) {
      // We're already transcoding and it still errored. Before giving up:
      // if the swarm is STILL delivering bytes, the HLS endpoint just needs
      // more pieces (the encoder starved before the first segment). Reload
      // the playlist instead of failing — by the next attempt more of the
      // file is buffered. Bounded to 6 retries so a truly dead stream still
      // surfaces an error rather than looping forever.
      const downloadingNow = (info?.downRate ?? 0) > 30 * 1024 // > 30 KB/s
      if (downloadingNow && bufferRetryRef.current < 6) {
        bufferRetryRef.current++
        clientLog('info', 'player', 'buffer retry — swarm still delivering, reloading playlist',
          { retry: bufferRetryRef.current, downRate: info?.downRate, ...diag })
        setVideoError(false)
        window.setTimeout(() => { videoRef.current?.load() }, 6000)
        return
      }
      clientLog('warn', 'player', 'surfacing error UI — no more fallbacks available',
        { reason: transcodeFallbackAttempted ? 'already-attempted' : forceH264 ? 'h264-already-forced' : 'no-caps', retries: bufferRetryRef.current, ...diag })
      setVideoError(true)
      return
    }
    const hasGPU = caps.hasNvidia || caps.hasVaapi || caps.hasQsv
    if (!hasGPU) {
      clientLog('warn', 'player', 'no GPU encoder — surfacing manual UI', { caps })
      setVideoError(true)
      return
    }
    clientLog('info', 'player', 'auto-fallback engaging via onError', { willRetryVia: caps.preferred, ...diag })
    setTranscodeFallbackAttempted(true)
    setForceH264(true)
    setVideoError(false)
  }

  // Safari HEVC silent-failure backstop. Safari on macOS does NOT fire
  // <video onError> when it can't decode HEVC — it just stays at readyState=0
  // with no diagnostic. After 20 s, if we still haven't reached
  // HAVE_CURRENT_DATA AND playback hasn't moved, trigger the same fallback
  // that onError would. 20s (not 10s) because HEVC 10-bit transcode legitimately
  // takes longer to emit the first segment — a tighter window fired the
  // fallback while ffmpeg was still producing, causing a reload storm.
  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0) return
    const transcodingActive = transcodeAudio !== null || forceH264 || burnSubTrack !== null
    if (transcodingActive || transcodeFallbackAttempted || videoError) return
    const timer = window.setTimeout(() => {
      const v = videoRef.current
      if (!v) return
      // readyState < 2 = nothing playable yet; currentTime < 0.1 = we haven't
      // moved a frame. Either condition alone could be benign during normal
      // buffering, but BOTH together for 20s smells like a codec rejection.
      const stuck = v.readyState < 2 && v.currentTime < 0.1 && bufferedEnd < 0.5
      clientLog('info', 'player', '20s backstop tick', { stuck, readyState: v.readyState, currentTime: v.currentTime, bufferedEnd, src: v.currentSrc })
      if (stuck) {
        if (caps && (caps.hasNvidia || caps.hasVaapi || caps.hasQsv)) {
          clientLog('warn', 'player', 'backstop firing fallback — Safari silent HEVC path likely', videoDiagnostic())
          setTranscodeFallbackAttempted(true)
          setForceH264(true)
        } else {
          clientLog('warn', 'player', 'backstop wanted to fallback but no GPU encoder available', { caps })
        }
      }
    }, 20000)
    return () => window.clearTimeout(timer)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash, selectedFile, transcodeAudio, forceH264, burnSubTrack, transcodeFallbackAttempted, videoError, caps])

  // Poll progress every 2s while modal is open
  useEffect(() => {
    if (!info?.infoHash) return
    const tick = () => {
      streamInfo(info.infoHash).then(setInfo).catch(() => {})
    }
    pollRef.current = window.setInterval(tick, 2000)
    return () => {
      if (pollRef.current) window.clearInterval(pollRef.current)
    }
  }, [info?.infoHash])

  // Mirror the values the unmount cleanup needs into a ref, refreshed every
  // render. This lets the cleanup run ONLY on real unmount (deps: []) while
  // still seeing current values — without it, depending on [libraryEntryID]
  // re-ran the cleanup the moment the library entry loaded mid-playback,
  // calling streamDrop() and KILLING the torrent we were actively streaming
  // (ffmpeg then died with "torrent closed" → "Sem seeds").
  const cleanupRef = useRef<{ infoHash: string; libraryEntryID: number | null }>({ infoHash: '', libraryEntryID: null })
  useEffect(() => {
    cleanupRef.current = { infoHash: info?.infoHash ?? '', libraryEntryID }
  })

  // Drop the torrent + persist final resume position — ONLY when the modal
  // truly unmounts (user closes/navigates), never on intra-playback state changes.
  useEffect(() => {
    return () => {
      const { infoHash, libraryEntryID: libID } = cleanupRef.current
      const v = videoRef.current
      if (libID !== null && v && v.currentTime > 1) {
        libraryUpdateResume(libID, v.currentTime, v.duration || 0).catch(() => {})
      }
      if (infoHash) {
        streamDrop(infoHash).catch(() => {})
      }
    }
  }, [])

  // Detect season/episode from title for better subtitle matches
  const parseSeasonEpisode = (title: string): { season?: number; episode?: number; cleanQuery: string } => {
    const match = title.match(/[Ss](\d{1,2})[Ee](\d{1,3})/)
    if (!match) return { cleanQuery: title }
    return {
      season: parseInt(match[1]),
      episode: parseInt(match[2]),
      cleanQuery: title.substring(0, match.index).trim().replace(/[._]/g, ' '),
    }
  }

  const openSubtitlePanel = async () => {
    setSubOpen(true)
    if (subResults.length > 0 || !result || !info) return
    setSubLoading(true)
    setSubError('')
    try {
      // Prefer hash-based auto search (frame-exact) — single API call, results ranked by relevance
      const resp = await subtitlesAuto(info.infoHash, selectedFile, 'pt-BR,pt')
      setSubResults(resp.results || [])
      if (resp.osHash && !resp.hashErr) setAutoSource('hash')
      else setAutoSource('title')
    } catch (e: any) {
      // Fall back to plain title search if auto endpoint fails
      try {
        const baseTitle = info.name || result.title
        const { season, episode, cleanQuery } = parseSeasonEpisode(baseTitle)
        const data = await subtitlesSearch(cleanQuery || baseTitle, { season, episode, langs: 'pt-BR,pt' })
        setSubResults(data || [])
        setAutoSource('title')
      } catch (e2: any) {
        setSubError(e2?.response?.data?.error || e2.message || 'Erro ao buscar legendas')
      }
    } finally {
      setSubLoading(false)
    }
  }

  const pickSubtitle = (s: Subtitle) => {
    setSubActive(s.id)
    setSubOpen(false)
  }

  const requestFullscreen = () => {
    const v = videoRef.current as any
    if (!v) return
    // iOS Safari uses webkitEnterFullscreen on the <video> element
    if (typeof v.webkitEnterFullscreen === 'function') {
      v.webkitEnterFullscreen()
    } else if (v.requestFullscreen) {
      v.requestFullscreen()
    } else if (v.webkitRequestFullscreen) {
      v.webkitRequestFullscreen()
    }
  }

  // Seek by delta seconds (skip forward/back)
  const seekBy = (delta: number) => {
    const v = videoRef.current
    if (!v || !v.duration) return
    v.currentTime = Math.max(0, Math.min(v.duration, v.currentTime + delta))
  }

  // Update offset and reapply to all cues
  const adjustSubOffset = (delta: number) => {
    setSubOffset((prev) => Math.round((prev + delta) * 10) / 10)
  }

  const resetSubOffset = () => setSubOffset(0)

  // Apply subtitle offset whenever active sub or offset changes.
  // Strategy: snapshot the original cue timings on first sight, then re-offset relative to that.
  useEffect(() => {
    const v = videoRef.current
    if (!v || !subActive) return

    const applyOffset = () => {
      const track = v.textTracks[0]
      if (!track || !track.cues || track.cues.length === 0) return
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
      track.mode = 'showing'
    }

    // Try now, and again when the track finishes loading
    applyOffset()
    const tracks = v.textTracks
    const onLoad = () => applyOffset()
    for (let i = 0; i < tracks.length; i++) {
      tracks[i].addEventListener('cuechange', onLoad)
    }
    return () => {
      for (let i = 0; i < tracks.length; i++) {
        tracks[i].removeEventListener('cuechange', onLoad)
      }
    }
  }, [subActive, subOffset])

  // Reset original cue timings when subtitle changes
  useEffect(() => {
    origCuesRef.current = []
  }, [subActive])

  // After torrent metadata loads, fetch the library entry to know if we have a saved resume position
  useEffect(() => {
    if (!info?.infoHash) return
    libraryGet(0).catch(() => {}) // warmup (no-op)
    // We don't know the library row ID upfront; the upsert happens in StreamAdd response chain.
    // Instead, fetch the user's library and find the entry by info_hash.
    import('../api/client').then(({ libraryList }) => {
      libraryList({ limit: 100 }).then(list => {
        const entry = list.find(e => e.infoHash === info.infoHash)
        if (entry) {
          setLibraryEntryID(entry.id)
          if (entry.resumeSeconds > 30 && entry.durationSeconds > 0 && entry.resumeSeconds < entry.durationSeconds - 30) {
            setResumePosition(entry.resumeSeconds)
          }
        }
      }).catch(() => {})
    })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash])

  // One-shot guard for the URL-supplied seek. Without it we'd re-apply the
  // initial seek every time `canplay` fires (which happens on each format
  // negotiation, transcode fallback, etc.), making it impossible to scrub away.
  const appliedInitialSeekRef = useRef(false)
  // Same idea for the library-driven auto-resume — fire once per file selection
  // and then keep `resumePosition` populated so the "Continuar" button can use
  // it after the user goes back to the start.
  const appliedAutoResumeRef = useRef(false)
  useEffect(() => {
    // Reset whenever a new file is selected so a future URL-driven re-play
    // (e.g., navigating to ?play=X&t=...) re-applies the seek instead of
    // remembering "already done" from the previous file.
    appliedInitialSeekRef.current = false
    appliedAutoResumeRef.current = false
  }, [selectedFile, info?.infoHash])

  // Seek once the video can play. Priority:
  //   1. URL-supplied initialSeek (explicit, e.g. shared link with `t=120`)
  //   2. per-user library resumeSeconds (background-saved, silent)
  const onVideoCanPlay = () => {
    const v = videoRef.current
    if (!v) return
    if (initialSeek !== undefined && initialSeek > 0 && !appliedInitialSeekRef.current) {
      if (v.currentTime < 1) {
        v.currentTime = initialSeek
      }
      appliedInitialSeekRef.current = true
      // Clear DB resume to avoid the second branch firing on the same canplay
      setResumePosition(null)
      return
    }
    if (resumePosition === null) return
    if (v.currentTime < 1 && resumePosition > 30 && !appliedAutoResumeRef.current) {
      v.currentTime = resumePosition
      appliedAutoResumeRef.current = true
      // Intentionally NOT clearing resumePosition — keep it around so the
      // "Continuar de onde parou" button can jump back if the user goes to
      // the start or scrubs elsewhere.
    }
  }

  // Probe container for embedded audio + subtitle tracks (uses ffprobe on first ~16MB).
  // Gated by serverReady so we don't fire while the torrent is still warming up —
  // ffprobe needs a live Reader from the streamer's active map.
  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0 || !serverReady) return
    streamProbe(info.infoHash, selectedFile)
      .then(p => {
        const safe = { audio: p.audio ?? [], subtitles: p.subtitles ?? [] }
        setProbe(safe)
        const ptSub = safe.subtitles.find(t => !t.image && /^pt|por/i.test(t.language || ''))
        if (ptSub && !subActive) {
          setEmbeddedSub(ptSub.index)
          setAutoSource('embedded')
        }
      })
      .catch(err => console.warn('probe failed:', err?.response?.data?.error || err.message))

    // Sidecar subtitle files (separate .srt in the torrent) — cheap, parallel with probe
    streamSidecars(info.infoHash, selectedFile)
      .then(list => {
        setSidecars(list ?? [])
        // Auto-pick pt sidecar if no embedded already chosen
        if (!subActive && embeddedSub === null && list && list.length > 0) {
          const pt = list.find(s => /^pt|por/i.test(s.language || ''))
          if (pt) {
            setSidecarIdx(pt.index)
            setAutoSource('embedded')
          }
        }
      })
      .catch(() => setSidecars([]))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash, selectedFile, serverReady])

  // Note: auto-search of OpenSubtitles intentionally NOT triggered here — it would burn quota.
  // Embedded subtitles auto-load (free), external ones require explicit click via "Legendas" button.
  // The hash-based search runs only on first open of the panel.

  // Track playback state + accumulate watch time for auto-favorite
  const onTimeUpdate = () => {
    const v = videoRef.current
    if (!v) return
    const now = v.currentTime
    // Delta-based accumulation — handles seeks correctly (don't count jumps)
    const delta = now - lastTickRef.current
    if (delta > 0 && delta < 2) { // small forward step = normal playback
      watchedRef.current += delta
    }
    lastTickRef.current = now

    setCurrentTime(now)
    setDuration(v.duration || 0)
    if (v.buffered.length > 0) {
      for (let i = 0; i < v.buffered.length; i++) {
        if (v.currentTime >= v.buffered.start(i) && v.currentTime <= v.buffered.end(i)) {
          setBufferedEnd(v.buffered.end(i))
          break
        }
      }
      if (v.buffered.length > 0 && v.currentTime > v.buffered.end(v.buffered.length - 1)) {
        setBufferedEnd(v.buffered.end(v.buffered.length - 1))
      }
    }

    // Auto-favorite once threshold passed
    if (!isFavorite && watchedRef.current >= AUTO_FAV_THRESHOLD && info) {
      setIsFavorite(true)
      favoriteAdd(info.name, info.infoHash, info?.infoHash ? `magnet:?xt=urn:btih:${info.infoHash}` : '', 'auto-5min').catch(() => setIsFavorite(false))
    }

    // Persist resume position every 15s, plus best-effort on close (cleanup effect)
    if (libraryEntryID !== null && now > 1) {
      const elapsed = now - lastResumeSaveRef.current
      if (elapsed > 15 || elapsed < -1 /* seek backwards forces save too */) {
        lastResumeSaveRef.current = now
        libraryUpdateResume(libraryEntryID, now, v.duration || 0).catch(() => {})
      }
    }

    // Mirror the playhead into the URL (?t=) every 5s so copying the browser
    // URL — or hitting reload — resumes at this exact point, even across
    // devices/users (the library-based resume is per-user and lags 15s).
    // replaceState avoids polluting browser history; preserves ?play & ?f.
    if (now > 3) {
      const since = now - lastUrlSyncRef.current
      if (since > 5 || since < -1 /* seek also refreshes the URL */) {
        lastUrlSyncRef.current = now
        const params = new URLSearchParams(window.location.search)
        params.set('t', String(Math.floor(now)))
        window.history.replaceState(null, '', `${window.location.pathname}?${params.toString()}`)
      }
    }

    // Aggressive prefetch — eliminate the metadata + first-pieces wait at item boundaries.
    // Heuristic: 50% triggers next-episode (same torrent) and next-playlist-item warm-up,
    // 85% adds the N+2 playlist item for fast-sequence playlists.
    if (v.duration && v.duration > 0) {
      const ratio = now / v.duration
      if (ratio > 0.5) {
        // Next episode in the same torrent (priority head pieces for series packs)
        if (!prefetchedNextEpRef.current && nextVideoIdx >= 0 && info) {
          prefetchedNextEpRef.current = true
          streamPrefetch(info.infoHash, nextVideoIdx)
        }
        // Next item in the playlist (different torrent — preloads metadata + first pieces)
        if (!prefetchedPlaylistN1Ref.current && onPrefetchNextPlaylist) {
          prefetchedPlaylistN1Ref.current = true
          onPrefetchNextPlaylist()
        }
      }
      if (ratio > 0.85 && !prefetchedPlaylistN2Ref.current && onPrefetchNextNextPlaylist) {
        prefetchedPlaylistN2Ref.current = true
        onPrefetchNextNextPlaylist()
      }
    }
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

  // Media Session API — exposes "what's playing" + media keys / lock-screen
  // controls to the OS. Without this, iOS shows "JackUI" with no metadata and
  // AirPods/bluetooth controls don't fire next/previous on the playlist.
  useEffect(() => {
    if (!info || selectedFile < 0) return
    if (!('mediaSession' in navigator)) return
    const file = info.files[selectedFile]
    const title = file?.path?.split('/').pop() || info.name
    navigator.mediaSession.metadata = new MediaMetadata({
      title,
      album: playlist?.name || info.name,
      artist: 'JackUI',
    })
    const v = () => videoRef.current
    navigator.mediaSession.setActionHandler('play', () => { v()?.play().catch(() => {}) })
    navigator.mediaSession.setActionHandler('pause', () => { v()?.pause() })
    navigator.mediaSession.setActionHandler('previoustrack', () => onPlaylistPrevious?.())
    navigator.mediaSession.setActionHandler('nexttrack', () => onPlaylistAdvance?.())
    navigator.mediaSession.setActionHandler('seekto', (d) => {
      const el = v()
      if (el && d.seekTime != null) el.currentTime = d.seekTime
    })
    return () => {
      // Best-effort cleanup — clearing handlers stops stale bindings firing
      // after the modal closes (the OS could still hold the previous metadata).
      try {
        navigator.mediaSession.setActionHandler('play', null)
        navigator.mediaSession.setActionHandler('pause', null)
        navigator.mediaSession.setActionHandler('previoustrack', null)
        navigator.mediaSession.setActionHandler('nexttrack', null)
        navigator.mediaSession.setActionHandler('seekto', null)
      } catch {}
    }
  }, [info?.infoHash, selectedFile, playlist?.name, onPlaylistAdvance, onPlaylistPrevious])

  // Load initial favorite state when torrent info arrives. Match by infoHash
  // first (precise — same content always returns same hash) and fall back to
  // name only when needed. The old version matched ONLY by name, which broke
  // when `info.name` (anacrolix torrent.Name()) differed from the favorite's
  // stored name (which was the search result title at favorite-time) — common
  // for torrents whose name has trailing periods, encoded characters, etc.
  useEffect(() => {
    if (!info) return
    favoritesList()
      .then(list => setIsFavorite(list.some(f =>
        (info.infoHash && f.infoHash && f.infoHash.toLowerCase() === info.infoHash.toLowerCase())
        || f.name === info.name
      )))
      .catch(() => {})
  }, [info?.name, info?.infoHash])

  const toggleFavorite = async () => {
    if (!info) return
    const next = !isFavorite
    setIsFavorite(next)
    try {
      if (next) {
        // We have the real source URL via `result.magnetUri || result.link` (pickTorrentSource).
        // If the result came from a search, magnetUri is set; if from /library, the magnet was already saved.
        // Fallback to inferred magnet (no trackers) ONLY if nothing else available.
        const magnet = (result ? pickTorrentSource(result) : '')
          || (info.infoHash ? `magnet:?xt=urn:btih:${info.infoHash}` : '')
        await favoriteAdd(info.name, info.infoHash, magnet, 'manual')
      } else {
        await favoriteRemove(info.name)
      }
    } catch {
      setIsFavorite(!next) // revert on error
    }
  }

  const formatTime = (s: number): string => {
    if (!isFinite(s) || s < 0) return '0:00'
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    const sec = Math.floor(s % 60)
    if (h > 0) return `${h}:${m.toString().padStart(2, '0')}:${sec.toString().padStart(2, '0')}`
    return `${m}:${sec.toString().padStart(2, '0')}`
  }

  if (!result) return null

  const videoFiles = info?.files.filter(f => f.isVideo) || []
  const currentFile = selectedFile >= 0 ? info?.files[selectedFile] : null

  // Series-in-torrent navigation: detect prev/next video file (by index order, restricted to video files)
  const videoFileIndices = (info?.files || []).filter(f => f.isVideo).map(f => f.index)
  const videoCursor = videoFileIndices.indexOf(selectedFile)
  const prevVideoIdx = videoCursor > 0 ? videoFileIndices[videoCursor - 1] : -1
  const nextVideoIdx = videoCursor >= 0 && videoCursor < videoFileIndices.length - 1 ? videoFileIndices[videoCursor + 1] : -1

  // Parse S/E pattern from filename for nicer episode labels
  const parseEpisode = (path: string): string | null => {
    const m = path.match(/[Ss](\d{1,2})[ ._-]?[Ee](\d{1,3})/)
    if (m) return `S${m[1].padStart(2, '0')}E${m[2].padStart(2, '0')}`
    return null
  }
  const currentEp = currentFile ? parseEpisode(currentFile.path) : null

  const playFile = (idx: number) => {
    if (idx < 0) return
    setSelectedFile(idx)
    setVideoError(false)
    setLastErrorDiag(null)
    setSidecarIdx(null)
    setEmbeddedSub(null)
    setSubActive(null)
    setProbe(null)
    setSidecars([])
    watchedRef.current = 0
    lastTickRef.current = 0
    setCurrentTime(0)
    setBufferedEnd(0)
  }

  // URL builder: raw direct play unless any transcoding option is active.
  // Safari + HEVC/x265/AV1 short-circuits to transcode (which is HLS for Safari)
  // BEFORE the first <video> attempt — otherwise the user sees the direct-play
  // attempt fail, the auto-fallback overlay flash, then a retry loop ("tente
  // novamente até funcionar"). Detection by filename is best-effort; if it
  // misses, the auto-fallback flow on onError still rescues it like before.
  const selectedFilename = info?.files?.[selectedFile]?.path ?? ''
  // Route to HLS up-front on Safari for anything it likely can't direct-play:
  //   - HEVC/x265/AV1 by name (codec markers)
  //   - 2160p/4K/UHD: even "MP4" containers at 4K are usually HEVC or H264 at
  //     a level Safari's <video> rejects; trying direct-play first just burns
  //     ~18s before the fallback. The whole point is to NOT attempt the path
  //     we know fails. Misses still get rescued by onError/backstop fallback.
  const safariNeedsTranscode = isSafariBrowser() &&
    /\b(x265|h\.?265|hevc|av1|2160p?|4k|uhd)\b/i.test(selectedFilename)
  const isTranscoded = transcodeAudio !== null || forceH264 || burnSubTrack !== null || safariNeedsTranscode
  const transcodeOpts: TranscodeOpts = {}
  if (transcodeAudio !== null) transcodeOpts.audio = transcodeAudio
  if (forceH264) transcodeOpts.video = 'h264'
  if (burnSubTrack !== null) {
    transcodeOpts.burn = burnSubTrack
    transcodeOpts.video = 'h264' // burn-in requires video re-encode
  }
  // When changing audio track, also re-encode audio to AAC for browser compatibility
  if (transcodeAudio !== null) transcodeOpts.acodec = 'aac'

  // Only emit a real <video src> after the streamer has the torrent active.
  // Premature srcs (during the metadata-cache hit phase) would 404 and cause
  // the browser to surface "format not supported" prematurely.
  //
  // Route selection in transcoded mode:
  //   - Safari/iOS → /api/stream/hls/.../index.m3u8 (HLS, natively supported)
  //   - Other browsers → /api/stream/transcode/.../?video=h264 (progressive MP4)
  //
  // Why: Safari's MSE pipeline rejects progressive fragmented MP4 over
  // chunked transfer with MediaError.SRC_NOT_SUPPORTED regardless of encoder
  // tuning. Apple's documented streaming format is HLS — `<video src=*.m3u8>`
  // is the only thing Safari treats as a first-class video source.
  // Chromium/Edge don't have native HLS support so we keep progressive MP4
  // for them (works fine with our current ffmpeg config).
  const streamURL = info && selectedFile >= 0 && serverReady
    ? (isTranscoded
        ? (isSafariBrowser()
            ? streamHLSMasterURL(info.infoHash, selectedFile)
            : streamTranscodeURL(info.infoHash, selectedFile, transcodeOpts))
        : streamFileURL(info.infoHash, selectedFile))
    : ''
  // Subtitle source priority: sidecar file (instant, perfect sync) > embedded track (extracted via ffmpeg) > OpenSubtitles external
  const subtitleVttURL =
    info && sidecarIdx !== null
      ? streamSidecarURL(info.infoHash, sidecarIdx)
      : info && embeddedSub !== null
        ? streamSubtrackURL(info.infoHash, selectedFile, embeddedSub)
        : subActive
          ? subtitleDownloadURL(subActive)
          : ''

  // "Open in VLC" link — universal M3U download.
  // The browser downloads /api/stream/playlist/HASH/IDX.m3u with the right content-type,
  // and the OS opens it in the registered M3U handler (VLC on every platform).
  // The previous vlc:// scheme broke on desktop VLC and iOS Safari produced "invalid address".
  const vlcURL = info && selectedFile >= 0
    ? streamPlaylistM3UURL(info.infoHash, selectedFile, forceH264 ? 'h264' : undefined)
    : ''

  return (
    <div
      className={minimized
        ? 'fixed bottom-3 right-3 z-50 w-[360px] max-w-[calc(100vw-1.5rem)]'
        : 'fixed inset-0 bg-black/80 backdrop-blur-sm flex items-center justify-center z-50 sm:p-4'}
      onClick={minimized ? undefined : (e) => e.target === e.currentTarget && onClose()}
    >
      {/* Responsive width: phones/tablets keep ~896px (max-w-4xl) for a tight focused
          modal. Laptops bump to ~1280px so the file list + side panels stop fighting
          for vertical space. Ultra-wide desktops (≥1536px) use 90vw — fills usable
          area without going edge-to-edge.

          Mobile-fullscreen: `h-[100dvh]` on phones makes the modal occupy the full
          dynamic viewport (handles iOS URL-bar collapse). Border/rounding stripped
          on phones so the modal becomes edge-to-edge. Returns to bounded card on sm+. */}
      <div className={minimized
        ? 'bg-gray-800 rounded-xl border border-gray-700 shadow-2xl w-full flex flex-col overflow-hidden'
        : 'bg-gray-800 rounded-none sm:rounded-2xl border-0 sm:border border-gray-700 w-full max-w-4xl lg:max-w-6xl 2xl:max-w-[min(90vw,1600px)] shadow-2xl h-[100dvh] sm:h-auto sm:max-h-[90vh] flex flex-col'}>
        {/* Header — safe-top on mobile so the title + close button clear the iOS
            notch in PWA standalone mode. Bounded to mobile (sm:pt-0 via inline
            class) because on sm+ the modal sits inside the page with margins
            and the inset is always 0 anyway. */}
        <div className="flex items-center justify-between p-4 pt-[max(env(safe-area-inset-top),1rem)] sm:pt-4 border-b border-gray-700 flex-shrink-0">
          <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2 min-w-0">
            <Play className="w-4 h-4 text-green-500 flex-shrink-0" />
            <span className="truncate">{info?.name || result.title}</span>
            {isTranscoded && (
              <span className="text-[10px] bg-purple-500/20 text-purple-300 border border-purple-500/30 px-1.5 py-0.5 rounded flex items-center gap-1 flex-shrink-0">
                <Cpu className="w-2.5 h-2.5" />GPU
              </span>
            )}
          </h2>
          <div className="flex items-center gap-2 flex-shrink-0 ml-2">
            {info && (
              <button
                onClick={toggleFavorite}
                title={isFavorite ? 'Remover dos favoritos (volta a ser elegível pra eviction)' : 'Marcar como favorito — preservado mesmo após "Limpar cache"'}
                className={`transition-colors ${isFavorite ? 'text-pink-400 hover:text-pink-300' : 'text-gray-500 hover:text-pink-400'}`}
              >
                <Heart className={`w-5 h-5 ${isFavorite ? 'fill-current' : ''}`} />
              </button>
            )}
            <button
              onClick={() => setMinimized(m => !m)}
              title={minimized ? 'Expandir player' : 'Minimizar (continua tocando ao navegar)'}
              className="text-gray-400 hover:text-gray-200 transition-colors"
            >
              {minimized ? <Maximize2 className="w-4 h-4" /> : <Minimize2 className="w-5 h-5" />}
            </button>
            <button onClick={onClose} className="text-gray-400 hover:text-gray-200 transition-colors">
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        {/* Playlist context bar */}
        {playlist && (
          <div className="flex items-center justify-between gap-2 px-4 py-2 bg-blue-500/10 border-b border-blue-500/30 text-xs text-blue-200 flex-shrink-0">
            <div className="flex items-center gap-2 min-w-0">
              <ListMusic className="w-3.5 h-3.5 flex-shrink-0" />
              <span className="font-medium truncate">{playlist.name}</span>
              <span className="text-blue-400/80 flex-shrink-0">
                · {playlist.currentIndex + 1} de {playlist.items.length}
              </span>
            </div>
            <div className="flex items-center gap-1 flex-shrink-0">
              <button
                onClick={onPlaylistPrevious}
                className="p-1 rounded hover:bg-blue-500/20 text-blue-200 hover:text-white"
                title="Item anterior da playlist"
              >
                <ChevronLeft className="w-4 h-4" />
              </button>
              <button
                onClick={onToggleShuffle}
                className={`p-1 rounded hover:bg-blue-500/20 ${shuffle ? 'text-green-300' : 'text-blue-200/60'} hover:text-white`}
                title={shuffle ? 'Shuffle: ON' : 'Shuffle: OFF'}
              >
                <Shuffle className="w-3.5 h-3.5" />
              </button>
              <button
                onClick={onCycleRepeat}
                className={`p-1 rounded hover:bg-blue-500/20 ${repeat !== 'none' ? 'text-green-300' : 'text-blue-200/60'} hover:text-white relative`}
                title={`Repeat: ${repeat}`}
              >
                <Repeat className="w-3.5 h-3.5" />
                {repeat === 'one' && (
                  <span className="absolute -bottom-0.5 -right-0.5 text-[8px] font-bold text-green-300">1</span>
                )}
              </button>
              <button
                onClick={onPlaylistAdvance}
                className="p-1 rounded hover:bg-blue-500/20 text-blue-200 hover:text-white"
                title="Próximo item da playlist"
              >
                <ChevronRight className="w-4 h-4" />
              </button>
            </div>
          </div>
        )}

        {/* Content. min-h-0 + flex-1 lets the inner active-stream block manage
            its own scroll regions (main column + sidebar) without the parent
            forcing an outer scrollbar. */}
        <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
          {/* Big loading: only when we have NOTHING — no metadata cache hit AND
              streamAdd hasn't returned yet. If the cache primed `info`, we skip
              this and show the populated UI immediately with a slim inline
              "waiting on swarm" indicator instead (rendered further below). */}
          {loading && !info && (
            <div className="flex flex-col items-center justify-center py-16 text-gray-400">
              <Loader2 className="w-10 h-10 animate-spin mb-4 text-green-500" />
              <p className="font-medium">Conectando ao swarm...</p>
              <p className="text-xs text-gray-500 mt-2">Primeira vez nesse torrent — buscando peers</p>
            </div>
          )}
          {/* Slim inline indicator: cached file list visible, swarm still warming up.
              The big buffering overlay over the video area covers the actual playback
              start; this strip is just to acknowledge that something is happening. */}
          {loading && info && !serverReady && (
            <div className="px-4 py-1.5 text-xs text-blue-300 bg-blue-500/10 border-b border-blue-500/30 flex items-center gap-2 flex-shrink-0">
              <Loader2 className="w-3 h-3 animate-spin" />
              Metadados em cache — conectando ao swarm em segundo plano...
            </div>
          )}

          {/* Error state */}
          {error && (
            <div className="m-5 p-4 bg-red-500/10 border border-red-500/30 rounded-xl">
              <p className="flex items-center gap-2 text-red-400 font-medium">
                <AlertCircle className="w-4 h-4" />
                Erro ao iniciar stream
              </p>
              <p className="text-sm text-red-300 mt-1">{error}</p>
              <p className="text-xs text-gray-500 mt-3">
                Causas comuns: torrent sem seeders, metadados não obtidos a tempo, ou magnet inválido.
              </p>
            </div>
          )}

          {/* Active stream */}
          {info && selectedFile >= 0 && (
            <div className="flex flex-col lg:flex-row flex-1 min-h-0">
              {/* Main column: video + transport + status + panels. On lg+ the
                  file picker moves to a sidebar on the right — frees this
                  column to grow without forcing the page into outer scroll. */}
              <div className="flex flex-col flex-1 min-w-0 lg:overflow-y-auto lg:overflow-x-hidden">
              {/* Video player. Vertical-aware sizing: we cap at ~58vh so the controls,
                  status bar, file picker, and panels below all fit inside the modal's
                  90vh budget on standard 1080p/ultrawide-1080 monitors. The flex
                  centering + `mx-auto` keeps the <video> centered with letterbox
                  bars when the source aspect doesn't match the available area. */}
              <div className="bg-black relative w-full mx-auto flex items-center justify-center max-h-[70vh] sm:max-h-[58vh]" style={{ aspectRatio: '16 / 9' }}>
                {/* Audio cover art — the <video> element below plays the audio
                    but shows a black frame, so for audio content we overlay the
                    embedded cover (or a music glyph fallback). Pointer-events
                    off so the native video controls underneath stay clickable. */}
                {audioMode && info && (
                  <div className="absolute inset-0 flex items-center justify-center bg-gradient-to-br from-gray-800 to-gray-900 pointer-events-none">
                    {/* Glyph sits behind; the cover <img> covers it when it loads,
                        and is hidden on error so the glyph shows through. */}
                    <Volume2 className="absolute w-12 h-12 text-gray-600" />
                    <img
                      src={streamArtworkURL(info.infoHash, selectedFile)}
                      alt=""
                      className="relative max-h-full max-w-full object-contain"
                      onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }}
                    />
                  </div>
                )}
                {/* Buffering overlay — visible while either (a) the streamer
                    hasn't activated the torrent yet (serverReady=false) or
                    (b) pieces haven't reached the playhead yet. */}
                {!videoError && currentTime === 0 && bufferedEnd === 0 && (
                  <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none z-10 bg-black/40">
                    <Loader2 className="w-12 h-12 animate-spin text-green-500 mb-3" />
                    <p className="text-gray-200 font-medium">
                      {!serverReady ? 'Conectando ao swarm...' : 'Baixando primeiras peças do torrent...'}
                    </p>
                    {resumePosition !== null && (
                      <p className="text-xs text-blue-300 mt-2">
                        Continuando de {formatTime(resumePosition)}
                      </p>
                    )}
                    <p className="text-xs text-gray-400 mt-1">
                      {info.peers > 0
                        ? `${info.seeders} seeders / ${info.peers} peers conectados`
                        : 'Aguardando peers...'}
                    </p>
                    {/* Honest progress: show download rate + bytes buffered so a
                        slow swarm reads as "still working" instead of "frozen".
                        ~30 MB is roughly the buffer the transcoder needs before
                        the first segment lands for 4K. */}
                    {info.downRate > 0 && (
                      <p className="text-[11px] text-gray-400 mt-1 tabular-nums">
                        <span className="text-green-400">↓ {formatRate(info.downRate)}</span>
                        {info.files?.[selectedFile] && (
                          <span className="text-gray-500"> · {formatSize(info.files[selectedFile].downloaded)} em buffer</span>
                        )}
                      </p>
                    )}
                    {isTranscoded && (
                      <p className="text-[11px] text-purple-300 mt-2 flex items-center gap-1">
                        <Cpu className="w-3 h-3" />
                        {transcodeFallbackAttempted
                          ? 'Convertendo via GPU — codec original incompatível (HEVC/AV1)'
                          : 'Transcoding ativo — primeiros frames demoram mais'}
                      </p>
                    )}
                  </div>
                )}
                {/* Persistent corner badge while auto-fallback is active (after buffering) */}
                {transcodeFallbackAttempted && !videoError && (
                  <div className="absolute top-2 right-2 bg-purple-600/85 text-white text-[10px] px-2 py-1 rounded-md flex items-center gap-1 backdrop-blur-sm pointer-events-none z-20">
                    <Cpu className="w-3 h-3" />
                    Convertendo via GPU
                  </div>
                )}
                {!videoError ? (
                  <video
                    ref={videoRef}
                    src={streamURL}
                    /* Native HTML5 controls. Custom overlays (central play
                       button, hover-fullscreen corner, tap-to-toggle on the
                       video area) conflicted with iOS Safari's touch gestures
                       and the custom fullscreen affordance was invisible on
                       touch (relied on :hover). Native controls give us
                       touch-correct behaviour, AirPlay, PiP, and the iOS lock
                       screen integration for free — at the cost of the
                       hover-thumbnail preview (desktop-only feature, useless
                       on touch anyway). */
                    controls
                    autoPlay
                    playsInline
                    /* iOS-legacy attribute for inline playback before fullscreen */
                    {...{ 'webkit-playsinline': 'true' } as any}
                    className="max-h-full max-w-full"
                    onError={onVideoError}
                    onLoadStart={() => clientLog('info', 'player', 'loadstart', { src: streamURL })}
                    onStalled={() => clientLog('warn', 'player', 'stalled', videoDiagnostic())}
                    onWaiting={() => clientLog('info', 'player', 'waiting (buffering)', { readyState: videoRef.current?.readyState })}
                    onTimeUpdate={onTimeUpdate}
                    onLoadedMetadata={(e) => {
                      const v = e.currentTarget
                      clientLog('info', 'player', 'loadedmetadata', { duration: v.duration, videoWidth: v.videoWidth, videoHeight: v.videoHeight, currentSrc: v.currentSrc })
                      onTimeUpdate()
                    }}
                    onProgress={onTimeUpdate}
                    onEnded={() => {
                      // 1. repeat-one: replay the same file
                      if (repeat === 'one') {
                        const v = videoRef.current
                        if (v) { v.currentTime = 0; v.play().catch(() => {}) }
                        return
                      }
                      // 2. Next file in the same torrent (next episode of a series pack)
                      if (nextVideoIdx >= 0) {
                        playFile(nextVideoIdx)
                        return
                      }
                      // 3. Next item in the playlist (different torrent)
                      if (onPlaylistAdvance) {
                        onPlaylistAdvance()
                      }
                    }}
                    onCanPlay={onVideoCanPlay}
                  >
                    {subtitleVttURL && (
                      <track
                        kind="subtitles"
                        src={subtitleVttURL}
                        srcLang="pt"
                        label="Português (BR)"
                        default
                      />
                    )}
                  </video>
                ) : null}
                {/* Native HTML5 controls render the play/pause button + the
                    fullscreen affordance inside the video element. No custom
                    overlays needed. */}
                {videoError && (() => {
                  // Honest error classification. The <video> element can't read
                  // the 503 body, but we already poll streamInfo (peers, rate,
                  // per-file progress) — use that to distinguish a dead/slow
                  // swarm (the bytes never arrive) from a real codec problem.
                  // Showing "codec não suportado" for a slow download is what
                  // confused the user; this tells them what's actually wrong.
                  const cf = info?.files?.[selectedFile]
                  const peers = info?.peers ?? 0
                  const fileDownloaded = cf?.downloaded ?? 0
                  const starving = fileDownloaded < 30 * 1024 * 1024 // < 30 MB
                  let title: string
                  let detail: string
                  let kind: 'swarm' | 'codec'
                  if (peers === 0) {
                    kind = 'swarm'
                    title = 'Sem seeds disponíveis'
                    detail = 'Ninguém está compartilhando este torrent agora. Não há de onde baixar os dados para reproduzir.'
                  } else if (starving) {
                    kind = 'swarm'
                    title = 'Download muito lento para streaming'
                    detail = `Baixando a ${formatRate(info?.downRate ?? 0)} de ${peers} peer${peers !== 1 ? 's' : ''} — lento demais para assistir em tempo real (4K precisa de ~3,7 MB/s). Baixe o arquivo completo antes de assistir.`
                  } else {
                    kind = 'codec'
                    title = 'Formato não suportado pelo browser'
                    detail = 'Codec ou container não compatível (provavelmente HEVC/x265 ou MKV). Use o link "Abrir no VLC" abaixo para reproduzir local.'
                  }
                  return (
                  <div className="absolute inset-0 flex flex-col items-center justify-center text-gray-300 p-6 text-center">
                    <AlertCircle className={`w-12 h-12 mb-3 ${kind === 'swarm' ? 'text-orange-400' : 'text-yellow-400'}`} />
                    <p className="font-medium">{title}</p>
                    <p className="text-sm text-gray-500 mt-2 max-w-md">{detail}</p>
                    {/* Diagnostic chip — shows the actual MediaError code so we
                        can tell HEVC-decode-rejection (3) from no-src-supported
                        (4) at a glance, without asking the user to open devtools. */}
                    {(() => {
                      // Prefer the frozen snapshot from onVideoError — by the
                      // time this UI renders, the <video> already unmounted so
                      // a live videoDiagnostic() comes back with null fields.
                      const diag = (lastErrorDiag ?? videoDiagnostic()) as Record<string, any>
                      const codeNames: Record<number, string> = { 1: 'ABORTED', 2: 'NETWORK', 3: 'DECODE', 4: 'SRC_NOT_SUPPORTED' }
                      const codeName = diag.errorCode ? codeNames[diag.errorCode] || `code ${diag.errorCode}` : '—'
                      return (
                        <div className="mt-3 text-[10px] text-gray-500 font-mono space-y-0.5">
                          <div>MediaError: <span className="text-yellow-400">{codeName}</span> {diag.errorMsg ? `· ${diag.errorMsg}` : ''}</div>
                          <div>ready={diag.readyState ?? '—'} net={diag.networkState ?? '—'} {diag.isTranscoded ? '· transcode ON' : '· direct play'}{diag.transcodeFallbackAttempted ? ' · fallback tried' : ''}</div>
                          <div className="text-gray-600">Full log: filtre por "[player]" no console</div>
                        </div>
                      )
                    })()}
                    <button
                      onClick={() => setVideoError(false)}
                      className="mt-4 text-xs text-green-400 hover:underline"
                    >
                      Tentar de novo
                    </button>
                  </div>
                  )
                })()}
              </div>

              {/* Everything below the video (transport, status, subtitle panel)
                  is hidden in minimized mode — the native <video> controls cover
                  play/pause/seek in the compact card. The <video> element itself
                  stays mounted above, so all the HEVC/HLS/buffer logic is intact. */}
              {!minimized && (<>

              {/* Skip controls + time display + series navigation.
                  Mobile: min-h-[44px] satisfies the iOS 44pt touch target HIG —
                  the previous py-1.5 dropped to ~28px which is below the
                  thumb-friendly threshold. Desktop keeps the compact version. */}
              <div className="px-3 sm:px-4 py-2 bg-gray-900 border-b border-gray-700 flex items-center gap-2 sm:gap-1.5 flex-wrap">
                {videoFileIndices.length > 1 && (
                  <>
                    <button
                      onClick={() => playFile(prevVideoIdx)}
                      disabled={prevVideoIdx < 0}
                      title="Episódio anterior"
                      className="flex items-center gap-1 text-sm sm:text-xs bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 border border-blue-500/30 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors disabled:opacity-30"
                    >
                      <ChevronLeft className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
                      Ep ant.
                    </button>
                    <button
                      onClick={() => playFile(nextVideoIdx)}
                      disabled={nextVideoIdx < 0}
                      title="Próximo episódio"
                      className="flex items-center gap-1 text-sm sm:text-xs bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 border border-blue-500/30 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors disabled:opacity-30"
                    >
                      Próx.
                      <ChevronRight className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
                    </button>
                    {currentEp && (
                      <span className="text-xs text-blue-300 px-2 py-1 bg-blue-500/10 rounded border border-blue-500/20 font-mono">
                        {currentEp}
                      </span>
                    )}
                    <span className="text-xs text-gray-500">
                      {videoCursor + 1}/{videoFileIndices.length}
                    </span>
                    <span className="w-px h-5 bg-gray-700 mx-1" />
                  </>
                )}
                {/* Voltar ao início — sempre visível, útil pra re-assistir do zero */}
                <button
                  onClick={() => { const v = videoRef.current; if (v) v.currentTime = 0 }}
                  title="Voltar ao início (0:00)"
                  className="flex items-center gap-1 text-sm sm:text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors"
                >
                  <SkipBack className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
                  <span className="font-mono text-[10px] sm:text-[9px]">|◀</span>
                </button>
                {/* Continuar de onde parou — só aparece se houver resume guardado
                    do library E o usuário não está perto desse ponto (>5s de gap).
                    Permite voltar pro ponto salvo após o usuário ir pro início ou
                    scrubar pra outro lugar. */}
                {resumePosition !== null && Math.abs(currentTime - resumePosition) > 5 && (
                  <button
                    onClick={() => { const v = videoRef.current; if (v) v.currentTime = resumePosition }}
                    title={`Continuar de ${formatTime(resumePosition)}`}
                    className="flex items-center gap-1 text-sm sm:text-xs bg-green-500/20 hover:bg-green-500/30 text-green-300 border border-green-500/30 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors"
                  >
                    <RotateCcw className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
                    <span className="hidden sm:inline">Continuar</span>
                    <span className="font-mono text-[10px] sm:hidden">{formatTime(resumePosition)}</span>
                  </button>
                )}
                <button
                  onClick={() => seekBy(-30)}
                  title="-30s"
                  className="flex items-center gap-1 text-sm sm:text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors"
                >
                  <Rewind className="w-4 h-4 sm:w-3.5 sm:h-3.5" />30s
                </button>
                <button
                  onClick={() => seekBy(-10)}
                  title="-10s"
                  className="flex items-center gap-1 text-sm sm:text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors"
                >
                  <SkipBack className="w-4 h-4 sm:w-3.5 sm:h-3.5" />10s
                </button>
                <button
                  onClick={() => seekBy(10)}
                  title="+10s"
                  className="flex items-center gap-1 text-sm sm:text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors"
                >
                  10s<SkipForward className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
                </button>
                <button
                  onClick={() => seekBy(30)}
                  title="+30s"
                  className="flex items-center gap-1 text-sm sm:text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors"
                >
                  30s<FastForward className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
                </button>
                <span className="text-xs text-gray-400 ml-2 font-mono tabular-nums">
                  {formatTime(currentTime)} <span className="text-gray-600">/</span> {formatTime(duration)}
                </span>

                {/* Subtitle offset controls — only visible when sub active */}
                {subActive && (
                  <div className="flex items-center gap-1 ml-auto bg-gray-800 border border-gray-700 rounded-lg px-2 py-0.5">
                    <span className="text-[10px] text-gray-500 uppercase tracking-wide mr-1">Legenda</span>
                    <button
                      onClick={() => adjustSubOffset(-0.1)}
                      title="Atrasar legenda em 0.1s"
                      className="text-gray-400 hover:text-blue-400 p-1 transition-colors"
                    >
                      <Minus className="w-3 h-3" />
                    </button>
                    <span className="text-xs text-gray-200 font-mono tabular-nums min-w-[40px] text-center">
                      {subOffset >= 0 ? '+' : ''}{subOffset.toFixed(1)}s
                    </span>
                    <button
                      onClick={() => adjustSubOffset(0.1)}
                      title="Adiantar legenda em 0.1s"
                      className="text-gray-400 hover:text-blue-400 p-1 transition-colors"
                    >
                      <Plus className="w-3 h-3" />
                    </button>
                    {subOffset !== 0 && (
                      <button
                        onClick={resetSubOffset}
                        title="Resetar offset"
                        className="text-gray-500 hover:text-gray-200 p-1 transition-colors"
                      >
                        <RotateCcw className="w-3 h-3" />
                      </button>
                    )}
                  </div>
                )}
              </div>

              {/* Status bar with buffer + torrent progress. `relative` lets the
                  hover preview bubble (absolute) anchor inside this container. */}
              <div className="relative px-3 sm:px-4 py-3 bg-gray-900/50 border-b border-gray-700 flex flex-col gap-2 text-xs">
                <div className="flex items-center gap-3 flex-wrap">
                  <span className="flex items-center gap-1.5 text-gray-300">
                    <Users className="w-3.5 h-3.5 text-green-400" />
                    {info.seeders} <span className="text-gray-500 hidden sm:inline">seeders</span>
                    <span className="text-gray-500">/</span> {info.peers} <span className="text-gray-500 hidden sm:inline">peers</span>
                  </span>
                  <span className="flex items-center gap-1.5 text-gray-300">
                    <Activity className="w-3.5 h-3.5 text-blue-400" />
                    {(info.progress * 100).toFixed(1)}%<span className="text-gray-500 hidden sm:inline ml-1">torrent</span>
                  </span>
                  <span className="flex items-center gap-1.5 text-gray-300 tabular-nums">
                    <span className="text-green-400">↓</span> {formatRate(info.downRate)}
                    <span className="text-yellow-400 ml-1">↑</span> {formatRate(info.upRate)}
                  </span>
                  <label className="flex items-center gap-1 text-gray-400" title="Velocidade de reprodução (pitch preservado — voz não fica robotizada)">
                    <FastForward className="w-3.5 h-3.5 text-gray-500" />
                    <select
                      value={playbackSpeed}
                      onChange={e => setPlaybackSpeed(parseFloat(e.target.value))}
                      className="bg-gray-800 border border-gray-700 rounded px-1 py-0.5 text-xs text-gray-200 tabular-nums focus:outline-none focus:border-green-500"
                    >
                      {SPEED_OPTIONS.map(s => (
                        <option key={s} value={s}>{s}x</option>
                      ))}
                    </select>
                  </label>
                  {currentFile && (
                    <span className="text-gray-400">
                      {formatSize(currentFile.downloaded)} / {formatSize(currentFile.size)}
                    </span>
                  )}
                  {bufferedEnd > 0 && duration > 0 && (
                    <span className="text-gray-400 ml-auto">
                      Buffer: <span className="text-blue-400">{formatTime(bufferedEnd - currentTime)}</span> à frente
                    </span>
                  )}
                </div>
                {/* Layered + interactive progress bar. Click to seek, drag to scrub,
                    hover to preview a thumbnail at that position. The bar is 6px tall
                    on hover so it's easy to click; it shrinks back to 1.5px when idle. */}
                <div
                  className="relative bg-gray-700 rounded-full h-1.5 hover:h-2 transition-[height] cursor-pointer group"
                  onMouseMove={(e) => {
                    if (!duration) return
                    const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect()
                    const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width))
                    setSeekHover({ time: ratio * duration, x: e.clientX - rect.left })
                  }}
                  onMouseLeave={() => setSeekHover(null)}
                  onClick={(e) => {
                    if (!duration || !videoRef.current) return
                    const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect()
                    const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width))
                    videoRef.current.currentTime = ratio * duration
                  }}
                  title={duration ? 'Clique para pular para essa posição' : ''}
                >
                  {/* Torrent download (gray) — only matters for raw stream, transcoded has full random access */}
                  <div
                    className="absolute inset-y-0 left-0 bg-gray-500 rounded-full pointer-events-none"
                    style={{ width: `${(currentFile?.progress || 0) * 100}%` }}
                  />
                  {duration > 0 && (
                    <>
                      <div
                        className="absolute inset-y-0 left-0 bg-blue-500/60 rounded-full pointer-events-none"
                        style={{ width: `${(bufferedEnd / duration) * 100}%` }}
                      />
                      <div
                        className="absolute inset-y-0 left-0 bg-green-500 rounded-full pointer-events-none"
                        style={{ width: `${(currentTime / duration) * 100}%` }}
                      />
                      {/* Scrubber handle — visible on hover only */}
                      <div
                        className="absolute top-1/2 -translate-y-1/2 -translate-x-1/2 w-3 h-3 bg-green-400 rounded-full shadow-lg opacity-0 group-hover:opacity-100 pointer-events-none"
                        style={{ left: `${(currentTime / duration) * 100}%` }}
                      />
                    </>
                  )}
                </div>
                {/* Hover bubble — preview thumbnail + timecode at the hovered position.
                    Positioned absolute over the bar; clamped so it doesn't overflow viewport. */}
                {seekHover && duration > 0 && info && selectedFile >= 0 && !isTranscoded && (
                  <div
                    className="absolute z-50 pointer-events-none flex flex-col items-center gap-1"
                    style={{
                      left: `calc(${(seekHover.time / duration) * 100}% - 70px)`,
                      bottom: '32px',
                    }}
                  >
                    <img
                      src={streamThumbnailURL(info.infoHash, selectedFile, Math.floor(seekHover.time))}
                      alt=""
                      loading="lazy"
                      className="w-[140px] h-[78px] object-cover bg-black border border-gray-600 rounded shadow-xl"
                      onError={(e) => { (e.target as HTMLImageElement).style.display = 'none' }}
                    />
                    <span className="text-[11px] bg-gray-900/95 text-gray-100 px-1.5 py-0.5 rounded font-mono tabular-nums">
                      {formatTime(seekHover.time)}
                    </span>
                  </div>
                )}
              </div>

              {/* (file picker moved to right sidebar — see end of active-stream block) */}

              {/* Embedded tracks (audio + subtitles inside the file) */}
              {probe && (probe.audio.length > 0 || probe.subtitles.length > 0) && (
                <div className="px-3 sm:px-4 py-3 border-b border-gray-700 flex flex-col gap-3">
                  {/* Audio tracks — clicking a non-default triggers transcoded remux */}
                  {probe.audio.length > 1 && (
                    <div>
                      <p className="text-xs text-gray-500 mb-1.5 flex items-center gap-2">
                        <Volume2 className="w-3 h-3" />
                        Faixas de áudio ({probe.audio.length})
                        {transcodeAudio !== null && (
                          <span className="text-[10px] text-purple-300 bg-purple-500/15 border border-purple-500/30 px-1.5 py-0.5 rounded">
                            <Cpu className="w-2.5 h-2.5 inline mr-0.5" />GPU encoding
                          </span>
                        )}
                      </p>
                      <div className="flex flex-wrap gap-1">
                        <button
                          onClick={() => setTranscodeAudio(null)}
                          className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                            transcodeAudio === null
                              ? 'bg-blue-500/20 text-blue-300 border-blue-500/30'
                              : 'bg-gray-800 text-gray-500 border-gray-700 hover:text-gray-300'
                          }`}
                          title="Faixa padrão do arquivo (direct play, com seek completo)"
                        >
                          Padrão
                        </button>
                        {probe.audio.map(a => (
                          <button
                            key={a.index}
                            onClick={() => setTranscodeAudio(a.index)}
                            title={`${a.title || a.codec}${a.channels ? ` (${a.channels}ch)` : ''} — clicar transcoda via FFmpeg, perde seek`}
                            className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                              transcodeAudio === a.index
                                ? 'bg-purple-500/20 text-purple-300 border-purple-500/30'
                                : a.default
                                  ? 'bg-blue-500/10 text-blue-400 border-blue-500/20 hover:bg-blue-500/20'
                                  : 'bg-gray-700/40 text-gray-400 border-gray-700 hover:text-gray-200'
                            }`}
                          >
                            {a.language ? a.language.toUpperCase() : '??'}
                            <span className="text-gray-500 ml-1">{a.codec}{a.channels ? `·${a.channels}ch` : ''}</span>
                            {a.default && <span className="ml-1 text-[9px]">★</span>}
                          </button>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Force H.264 toggle — useful for HEVC files in Chrome */}
                  <div className="flex items-center justify-between gap-2 flex-wrap">
                    <button
                      onClick={() => setForceH264(v => !v)}
                      title="Re-encoda vídeo para H.264 — útil quando o codec original é HEVC e o browser não decodifica"
                      className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg border transition-colors ${
                        forceH264
                          ? 'bg-purple-500/20 text-purple-300 border-purple-500/30'
                          : 'bg-gray-700/50 text-gray-400 border-gray-700 hover:text-gray-200'
                      }`}
                    >
                      <Cpu className="w-3.5 h-3.5" />
                      Forçar H.264
                      {forceH264 && <Check className="w-3 h-3" />}
                    </button>

                    {/* Stream mode indicator */}
                    {isTranscoded && (
                      <span className="text-[11px] text-yellow-400 flex items-center gap-1">
                        <AlertCircle className="w-3 h-3" />
                        Stream transcoded — seek limitado
                      </span>
                    )}
                  </div>

                  {/* Sidecar subtitles (.srt files alongside the video in the torrent) */}
                  {sidecars.length > 0 && (
                    <div>
                      <p className="text-xs text-gray-500 mb-1.5 flex items-center gap-2">
                        <Subtitles className="w-3 h-3" />
                        Legendas no torrent ({sidecars.length}) <span className="text-[10px] text-gray-600 italic">— arquivos .srt/.vtt</span>
                      </p>
                      <div className="flex flex-wrap gap-1">
                        <button
                          onClick={() => setSidecarIdx(null)}
                          className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                            sidecarIdx === null
                              ? 'bg-gray-700 text-gray-200 border-gray-600'
                              : 'bg-gray-800 text-gray-500 border-gray-700 hover:text-gray-300'
                          }`}
                        >
                          Nenhuma
                        </button>
                        {sidecars.map(s => (
                          <button
                            key={s.index}
                            onClick={() => {
                              setSidecarIdx(s.index)
                              setEmbeddedSub(null)
                              setSubActive(null)
                              setAutoSource('embedded')
                            }}
                            title={s.path}
                            className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                              sidecarIdx === s.index
                                ? 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30'
                                : 'bg-gray-700/40 text-gray-400 border-gray-700 hover:text-gray-200'
                            }`}
                          >
                            {(s.language || '??').toUpperCase()}
                            <span className="text-gray-500 ml-1">.{s.format}</span>
                          </button>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Embedded subtitles — pickable (text subs as track, image subs as burn-in) */}
                  {probe.subtitles.length > 0 && (
                    <div>
                      <p className="text-xs text-gray-500 mb-1.5 flex items-center gap-2">
                        <Subtitles className="w-3 h-3" />
                        Legendas embutidas ({probe.subtitles.length})
                        {burnSubTrack !== null && (
                          <span className="text-[10px] text-orange-300 bg-orange-500/15 border border-orange-500/30 px-1.5 py-0.5 rounded">
                            <Flame className="w-2.5 h-2.5 inline mr-0.5" />Burn-in
                          </span>
                        )}
                      </p>
                      <div className="flex flex-wrap gap-1">
                        <button
                          onClick={() => { setEmbeddedSub(null); setBurnSubTrack(null) }}
                          className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                            embeddedSub === null && burnSubTrack === null
                              ? 'bg-gray-700 text-gray-200 border-gray-600'
                              : 'bg-gray-800 text-gray-500 border-gray-700 hover:text-gray-300'
                          }`}
                        >
                          Nenhuma
                        </button>
                        {probe.subtitles.map(s => {
                          const isActive = embeddedSub === s.index || burnSubTrack === s.index
                          return (
                            <button
                              key={s.index}
                              onClick={() => {
                                if (s.image) {
                                  // Image sub → burn-in (forces video re-encode)
                                  setBurnSubTrack(s.index)
                                  setEmbeddedSub(null)
                                } else {
                                  // Text sub → extract as VTT
                                  setEmbeddedSub(s.index)
                                  setBurnSubTrack(null)
                                  setSubActive(null)
                                  setAutoSource('embedded')
                                }
                              }}
                              title={
                                s.image
                                  ? `${s.codec} (imagem) — burn-in via FFmpeg, vai forçar transcode do vídeo`
                                  : s.title || s.codec
                              }
                              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                                isActive
                                  ? s.image
                                    ? 'bg-orange-500/20 text-orange-300 border-orange-500/30'
                                    : 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30'
                                  : 'bg-gray-700/40 text-gray-400 border-gray-700 hover:text-gray-200'
                              }`}
                            >
                              {s.language ? s.language.toUpperCase() : '??'}
                              <span className="text-gray-500 ml-1">{s.codec}</span>
                              {s.forced && <span className="ml-1 text-[9px] text-yellow-400">FORCED</span>}
                              {s.image && <span className="ml-1 text-[9px] text-orange-400">IMG</span>}
                            </button>
                          )
                        })}
                      </div>
                    </div>
                  )}
                </div>
              )}

              {/* Action bar */}
              <div className="px-3 sm:px-4 py-3 flex items-center gap-2 flex-wrap">
                <button
                  onClick={openSubtitlePanel}
                  disabled={!subEnabled}
                  title={
                    !subEnabled ? 'Configure OpenSubtitles API key em Settings'
                    : autoSource === 'embedded' ? 'Legenda embutida no arquivo (sync perfeito)'
                    : autoSource === 'hash' ? 'Legenda casada por hash do arquivo (frame-exato)'
                    : autoSource === 'title' ? 'Legenda encontrada pelo título'
                    : 'Buscar legendas em português'
                  }
                  className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg transition-colors border ${
                    (subActive || embeddedSub !== null)
                      ? autoSource === 'embedded' || autoSource === 'hash'
                        ? 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30'
                        : 'bg-green-500/20 text-green-400 border-green-500/30'
                      : subEnabled
                        ? 'bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 border-blue-500/30'
                        : 'bg-gray-700/50 text-gray-500 border-gray-700 cursor-not-allowed opacity-50'
                  }`}
                >
                  {subLoading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Subtitles className="w-3.5 h-3.5" />}
                  {embeddedSub !== null
                    ? 'Legenda embutida'
                    : subActive
                      ? (autoSource === 'hash' ? 'Legenda ✓ hash' : 'Legenda ativa')
                      : subLoading ? 'Buscando...' : 'Legendas'}
                </button>
                <button
                  onClick={requestFullscreen}
                  title="Tela cheia"
                  className="flex items-center gap-1.5 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 py-1.5 rounded-lg transition-colors sm:hidden"
                >
                  <Maximize2 className="w-3.5 h-3.5" />
                  Fullscreen
                </button>
                <a
                  href={vlcURL}
                  className="flex items-center gap-1.5 text-xs bg-orange-500/20 hover:bg-orange-500/30 text-orange-300 border border-orange-500/30 px-3 py-1.5 rounded-lg transition-colors"
                  title="Abrir o stream no app VLC local — funciona com qualquer codec"
                >
                  <ExternalLink className="w-3.5 h-3.5" />
                  VLC
                </a>
                <a
                  href={streamURL}
                  download
                  className="flex items-center gap-1.5 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 py-1.5 rounded-lg transition-colors"
                >
                  <Download className="w-3.5 h-3.5" />
                  <span className="hidden sm:inline">Baixar direto</span>
                  <span className="sm:hidden">Baixar</span>
                </a>
                <span className="text-xs text-gray-600 ml-auto hidden sm:block">
                  {info.files.length} arquivo{info.files.length !== 1 ? 's' : ''} • {formatSize(info.totalSize)}
                </span>
              </div>

              {/* Subtitle picker panel */}
              {subOpen && (
                <div className="px-3 sm:px-4 pb-4 border-t border-gray-700 pt-3">
                  <div className="flex items-center justify-between mb-2">
                    <h3 className="text-sm font-medium text-gray-200 flex items-center gap-2">
                      <Subtitles className="w-4 h-4 text-blue-400" />
                      Legendas (pt-BR / pt)
                    </h3>
                    <button onClick={() => setSubOpen(false)} className="text-gray-500 hover:text-gray-300">
                      <X className="w-4 h-4" />
                    </button>
                  </div>
                  {subLoading && (
                    <div className="flex items-center gap-2 text-sm text-gray-400 py-2">
                      <Loader2 className="w-4 h-4 animate-spin" />
                      Buscando no OpenSubtitles...
                    </div>
                  )}
                  {subError && (
                    <p className="text-xs text-red-400 py-2">{subError}</p>
                  )}
                  {!subLoading && !subError && subResults.length === 0 && (
                    <p className="text-xs text-gray-500 py-2">Nenhuma legenda encontrada</p>
                  )}
                  {subResults.length > 0 && (
                    <div className="flex flex-col gap-1 max-h-48 overflow-y-auto">
                      {subResults.map(s => (
                        <button
                          key={s.id}
                          onClick={() => pickSubtitle(s)}
                          className={`flex items-center justify-between gap-2 px-3 py-2 rounded-lg text-xs text-left transition-colors ${
                            subActive === s.id
                              ? 'bg-green-500/20 text-green-400 border border-green-500/30'
                              : 'bg-gray-900/50 hover:bg-gray-900 text-gray-300 border border-transparent'
                          }`}
                        >
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2 flex-wrap">
                              <span className="font-mono uppercase text-[10px] bg-gray-700 px-1.5 py-0.5 rounded">
                                {s.language}
                              </span>
                              <span className="truncate">{s.release || '(sem release name)'}</span>
                              {s.trusted && <span className="text-green-400 text-[10px]">✓ trusted</span>}
                              {s.hearingImpaired && <span className="text-yellow-400 text-[10px]">[HI]</span>}
                            </div>
                            <div className="text-[10px] text-gray-500 mt-0.5">
                              {s.uploaderName} • {s.downloads.toLocaleString()} downloads
                            </div>
                          </div>
                          {subActive === s.id && <Check className="w-4 h-4 flex-shrink-0" />}
                        </button>
                      ))}
                    </div>
                  )}
                  {subActive && (
                    <button
                      onClick={() => setSubActive(null)}
                      className="mt-2 text-xs text-gray-500 hover:text-red-400 transition-colors flex items-center gap-1"
                    >
                      <X className="w-3 h-3" />
                      Remover legenda
                    </button>
                  )}
                </div>
              )}
              </>)}{/* end !minimized transport/status/subtitle block */}
              </div>{/* end main column */}

              {/* File picker — right sidebar on lg+, stacked panel below on mobile.
                  Series-aware: detects S/E in filenames and labels them. Filter
                  matches both the path AND the parsed S/E tag so "s04e03" finds
                  the episode without typing the show name. Extras (featurettes,
                  bonus, behind-the-scenes) sort to the bottom with an EXTRA badge. */}
              {!minimized && info.files.length > 1 && sidebarOpen && (() => {
                const filterLower = fileFilter.trim().toLowerCase()
                const matches = (path: string, ep: string | null) =>
                  !filterLower ||
                  path.toLowerCase().includes(filterLower) ||
                  (ep || '').toLowerCase().includes(filterLower)
                const extraRe = /\b(featurettes?|extras?|bonus|behind[\s\-]?the[\s\-]?scenes|deleted[\s\-]?scenes|making[\s\-]?of|samples?|trailers?|interviews?|gag[\s\-]?reel|outtakes?)\b/i
                const isExtra = (path: string) => extraRe.test(path)
                const filteredFiles = info.files
                  .filter(f => matches(f.path, parseEpisode(f.path)))
                  .slice()
                  .sort((a, b) => {
                    const ax = isExtra(a.path), bx = isExtra(b.path)
                    if (ax !== bx) return ax ? 1 : -1
                    const ae = parseEpisode(a.path), be = parseEpisode(b.path)
                    if (ae && be) return ae.localeCompare(be)
                    if (ae) return -1
                    if (be) return 1
                    return a.index - b.index
                  })
                return (
                  <aside className="flex flex-col flex-shrink-0 lg:w-80 xl:w-96 border-t lg:border-t-0 lg:border-l border-gray-700 bg-gray-850/50 min-h-0 lg:overflow-hidden">
                    <div className="flex items-center justify-between gap-2 px-3 py-2 border-b border-gray-700 flex-shrink-0">
                      <p className="text-xs text-gray-400 flex items-center gap-2 min-w-0">
                        <FileVideo className="w-3.5 h-3.5 text-gray-500 flex-shrink-0" />
                        <span className="truncate">
                          {filteredFiles.length}{filterLower ? ` / ${info.files.length}` : ''} arquivo{filteredFiles.length === 1 ? '' : 's'}
                          {videoFiles.length > 0 && <span className="text-blue-400"> · {videoFiles.length} vídeo{videoFiles.length === 1 ? '' : 's'}</span>}
                        </span>
                      </p>
                      <button
                        onClick={() => setSidebarOpen(false)}
                        title="Esconder lista de arquivos"
                        className="text-gray-500 hover:text-gray-200 p-1 rounded hover:bg-gray-700 flex-shrink-0"
                      >
                        <ChevronRight className="w-4 h-4" />
                      </button>
                    </div>
                    {info.files.length > 8 && (
                      <div className="px-3 py-2 border-b border-gray-700 flex-shrink-0">
                        <input
                          type="text"
                          value={fileFilter}
                          onChange={e => setFileFilter(e.target.value)}
                          placeholder="Filtrar (ex: s04e03)"
                          className="w-full bg-gray-900 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 placeholder-gray-500 focus:outline-none focus:border-green-500"
                        />
                      </div>
                    )}
                    <div className="flex flex-col gap-1 px-2 py-2 overflow-y-auto min-h-0 max-h-60 lg:max-h-none">
                      {filteredFiles.length === 0 && (
                        <p className="text-xs text-gray-500 text-center py-3">Nenhum arquivo bate com "{fileFilter}"</p>
                      )}
                      {filteredFiles.map(f => {
                        const ep = parseEpisode(f.path)
                        const extra = isExtra(f.path)
                        // Compact name for sidebar: drop the long shared prefix
                        // (everything before the last "/") so paths fit in 320px.
                        const shortName = f.path.split('/').slice(-2).join('/')
                        // Audio file detection — torrents that pack mp3/flac
                        // along with the video should be playable too.
                        const AUDIO_RE = /\.(mp3|flac|m4a|aac|ogg|wav|opus|alac|wma)$/i
                        const isPlayable = f.isVideo || AUDIO_RE.test(f.path)
                        const previewKind = isPlayable ? 'unknown' : detectPreviewKind(f.path)
                        const canPreview = previewKind !== 'unknown'
                        const previewBadge = canPreview ? previewKind.toUpperCase() : null
                        return (
                          <button
                            key={f.index}
                            onClick={() => {
                              if (isPlayable) playFile(f.index)
                              else if (canPreview) setPreviewFileIdx(f.index)
                              // else: dead row, click does nothing (download via long-press / context menu still available)
                            }}
                            title={f.path}
                            className={`flex flex-col gap-0.5 px-2.5 py-2 rounded-lg text-xs transition-colors text-left ${
                              selectedFile === f.index
                                ? 'bg-green-500/20 text-green-400 border border-green-500/30'
                                : isPlayable
                                  ? extra
                                    ? 'bg-gray-800/40 text-gray-500 hover:bg-gray-700/80 border border-transparent'
                                    : 'bg-gray-700/50 text-gray-300 hover:bg-gray-700 border border-transparent'
                                  : canPreview
                                    ? 'bg-blue-500/5 text-blue-200/80 hover:bg-blue-500/15 border border-blue-500/20'
                                    : 'bg-gray-800/50 text-gray-500 hover:bg-gray-700 border border-transparent'
                            }`}
                          >
                            <span className="flex items-center gap-1.5 min-w-0">
                              {ep && (
                                <span className="text-[10px] font-mono bg-blue-500/15 text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0">
                                  {ep}
                                </span>
                              )}
                              {extra && (
                                <span className="text-[10px] font-mono bg-gray-700/60 text-gray-400 border border-gray-600/40 px-1.5 py-0.5 rounded flex-shrink-0">
                                  EXTRA
                                </span>
                              )}
                              {previewBadge && (
                                <span className="text-[10px] font-mono bg-blue-500/15 text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0" title="Visualizar inline">
                                  {previewBadge}
                                </span>
                              )}
                              {selectedFile === f.index && <Play className="w-3 h-3 flex-shrink-0" />}
                            </span>
                            <span className="flex items-center justify-between gap-2 min-w-0">
                              <span className="truncate">{shortName}</span>
                              <span className="text-gray-500 flex-shrink-0 text-[10px] tabular-nums">{formatSize(f.size)}</span>
                            </span>
                          </button>
                        )
                      })}
                    </div>
                  </aside>
                )
              })()}

              {/* Collapsed-sidebar reopen tab — two variants:
                  • lg+: slim vertical strip on the right edge of the modal.
                  • mobile: horizontal bar below the video. Without this, iOS
                    users who tap "Esconder lista" had no way to bring it back —
                    the list literally vanished. (See issue #50.) */}
              {info.files.length > 1 && !sidebarOpen && (
                <>
                  {/* Mobile (and tablet up to lg): full-width bar */}
                  <button
                    onClick={() => setSidebarOpen(true)}
                    title="Mostrar lista de arquivos"
                    className="lg:hidden flex items-center justify-center gap-2 w-full px-4 py-2 border-t border-gray-700 bg-gray-850 hover:bg-gray-700 text-gray-400 hover:text-gray-200 text-xs flex-shrink-0"
                  >
                    <ChevronLeft className="w-4 h-4 rotate-90" />
                    Mostrar lista de arquivos ({info.files.length})
                  </button>
                  {/* lg+: vertical strip on the right edge */}
                  <button
                    onClick={() => setSidebarOpen(true)}
                    title="Mostrar lista de arquivos"
                    className="hidden lg:flex flex-col items-center justify-center w-8 border-l border-gray-700 bg-gray-850 hover:bg-gray-700 text-gray-400 hover:text-gray-200 flex-shrink-0"
                  >
                    <ChevronLeft className="w-4 h-4" />
                    <span className="text-[10px] [writing-mode:vertical-rl] rotate-180 mt-2">
                      Arquivos ({info.files.length})
                    </span>
                  </button>
                </>
              )}
            </div>
          )}

          {/* No video files in torrent */}
          {info && videoFiles.length === 0 && (
            <div className="m-5 p-4 bg-yellow-500/10 border border-yellow-500/30 rounded-xl">
              <p className="flex items-center gap-2 text-yellow-400 font-medium">
                <AlertCircle className="w-4 h-4" />
                Nenhum arquivo de vídeo encontrado
              </p>
              <p className="text-xs text-gray-500 mt-2">
                Este torrent contém {info.files.length} arquivo(s) mas nenhum é de vídeo reconhecido (.mp4, .mkv, .avi, etc.)
              </p>
            </div>
          )}
        </div>
      </div>
      {/* Inline preview overlay for non-playable companion files (NFO, log,
          subtitles, PDFs shipped inside the torrent). Rendered outside the
          main modal box so its z-index can sit ABOVE the player without
          fighting flex layout. */}
      {previewFileIdx !== null && info && info.files[previewFileIdx] && (
        <FilePreviewModal
          infoHash={info.infoHash}
          fileIdx={previewFileIdx}
          filePath={info.files[previewFileIdx].path}
          fileSize={info.files[previewFileIdx].size}
          onClose={() => setPreviewFileIdx(null)}
        />
      )}
    </div>
  )
}
