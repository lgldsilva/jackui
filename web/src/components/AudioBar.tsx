import { useEffect, useRef, useState } from 'react'
import { Play, Pause, SkipBack, SkipForward, X, Music, Loader2, Volume2, VolumeX, FastForward, Shuffle, Repeat, ChevronUp, ChevronDown, ListMusic } from 'lucide-react'
import {
  SearchResult, TorrentInfo,
  streamAdd, streamFileURL, streamArtworkURL, pickTorrentSource,
} from '../api/client'
import { formatDuration } from '../lib/format'

interface AudioBarProps {
  result: SearchResult
  initialFileIndex?: number
  onClose: () => void
  playlist?: { name: string; items: { title: string }[]; currentIndex: number } | null
  onPlaylistAdvance?: () => void
  onPlaylistPrevious?: () => void
  repeat?: 'none' | 'one' | 'all'
  shuffle?: boolean
  onCycleRepeat?: () => void
  onToggleShuffle?: () => void
  onPrefetchNextPlaylist?: () => void
  onPrefetchNextNextPlaylist?: () => void
}

const SPEED_OPTIONS = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3] as const

/**
 * AudioBar — a Spotify-like persistent bottom bar for streaming audio files.
 *
 * Why not the regular PlayerModal: video modal is full-screen and disappears
 * when the user navigates away. For music/podcasts/audiobooks the user wants
 * to keep listening while browsing — the modal UX is wrong for that.
 *
 * The <audio> element lives inside this bar; this bar lives inside
 * PlayerProvider (above <Routes>), so route changes never unmount it.
 * Media Session API exposes track metadata to the OS for lock-screen / AirPods.
 */
export default function AudioBar({
  result,
  initialFileIndex,
  onClose,
  playlist = null,
  onPlaylistAdvance,
  onPlaylistPrevious,
  repeat = 'none',
  shuffle = false,
  onCycleRepeat,
  onToggleShuffle,
  onPrefetchNextPlaylist,
  onPrefetchNextNextPlaylist,
}: AudioBarProps) {
  const [info, setInfo] = useState<TorrentInfo | null>(null)
  const [selectedFile, setSelectedFile] = useState<number>(-1)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [isPlaying, setIsPlaying] = useState(false)
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [volume, setVolume] = useState(() => {
    const v = parseFloat(localStorage.getItem('jackui.volume') || '1')
    return isFinite(v) && v >= 0 && v <= 1 ? v : 1
  })
  const [muted, setMuted] = useState(false)
  const [speed, setSpeed] = useState<number>(() => {
    const v = parseFloat(localStorage.getItem('jackui.playbackSpeed') || '1')
    return isFinite(v) && v > 0 ? v : 1
  })
  const [hasArtwork, setHasArtwork] = useState<boolean | null>(null)
  const [expanded, setExpanded] = useState(false)

  const audioRef = useRef<HTMLAudioElement>(null)
  const prefetchedN1Ref = useRef(false)
  const prefetchedN2Ref = useRef(false)

  // Load the torrent whenever the playlist position / single result changes.
  useEffect(() => {
    const src = pickTorrentSource(result)
    if (!src) return
    setLoading(true)
    setError('')
    setInfo(null)
    setSelectedFile(-1)
    setCurrentTime(0)
    setDuration(0)
    setHasArtwork(null)
    prefetchedN1Ref.current = false
    prefetchedN2Ref.current = false

    streamAdd(src)
      .then(t => {
        setInfo(t)
        const chosen =
          initialFileIndex !== undefined && initialFileIndex >= 0 && initialFileIndex < t.files.length
            ? initialFileIndex
            : pickAudioFile(t) ?? (t.primaryFile >= 0 ? t.primaryFile : 0)
        setSelectedFile(chosen)
      })
      .catch(e => setError(e?.response?.data?.error || e.message || 'Falha ao iniciar stream'))
      .finally(() => setLoading(false))
  }, [result, initialFileIndex])

  // Probe artwork existence — 204 = no cover, 200 = bytes available
  useEffect(() => {
    if (!info || selectedFile < 0) return
    const url = streamArtworkURL(info.infoHash, selectedFile)
    fetch(url, { method: 'HEAD' })
      .then(r => setHasArtwork(r.status === 200))
      .catch(() => setHasArtwork(false))
  }, [info?.infoHash, selectedFile])

  // Apply volume + speed + pitch preservation whenever inputs change
  useEffect(() => {
    const a = audioRef.current
    if (!a) return
    a.volume = volume
    a.muted = muted
    a.playbackRate = speed
    a.preservesPitch = true
    ;(a as unknown as { webkitPreservesPitch?: boolean }).webkitPreservesPitch = true
    localStorage.setItem('jackui.volume', String(volume))
    localStorage.setItem('jackui.playbackSpeed', String(speed))
  }, [volume, muted, speed])

  // Media Session — lock screen + AirPods + bluetooth controls
  useEffect(() => {
    if (!('mediaSession' in navigator)) return
    if (!info || selectedFile < 0) return
    const filePath = info.files[selectedFile]?.path || info.name
    const title = filePath.split('/').pop() || info.name
    const artworkURL = hasArtwork && info ? streamArtworkURL(info.infoHash, selectedFile) : undefined
    navigator.mediaSession.metadata = new MediaMetadata({
      title,
      album: playlist?.name || info.name,
      artist: 'JackUI',
      ...(artworkURL ? { artwork: [{ src: artworkURL, sizes: '300x300', type: 'image/jpeg' }] } : {}),
    })
    navigator.mediaSession.setActionHandler('play', () => audioRef.current?.play().catch(() => {}))
    navigator.mediaSession.setActionHandler('pause', () => audioRef.current?.pause())
    // Hardware media keys + lock-screen prev/next walk torrent tracks first,
    // then fall back to playlist navigation. Same semantics as the bar buttons.
    navigator.mediaSession.setActionHandler('previoustrack', () => {
      if (prevTrackInTorrent >= 0) playFileInTorrent(prevTrackInTorrent)
      else onPlaylistPrevious?.()
    })
    navigator.mediaSession.setActionHandler('nexttrack', () => {
      if (nextTrackInTorrent >= 0) playFileInTorrent(nextTrackInTorrent)
      else onPlaylistAdvance?.()
    })
    navigator.mediaSession.setActionHandler('seekto', d => {
      const a = audioRef.current
      if (a && d.seekTime != null) a.currentTime = d.seekTime
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
  }, [info?.infoHash, selectedFile, hasArtwork, playlist?.name, onPlaylistAdvance, onPlaylistPrevious])

  const togglePlay = () => {
    const a = audioRef.current
    if (!a) return
    if (a.paused) a.play().catch(() => {})
    else a.pause()
  }

  const seekFraction = (frac: number) => {
    const a = audioRef.current
    if (!a || !duration) return
    a.currentTime = Math.max(0, Math.min(duration, frac * duration))
  }

  const streamURL = info && selectedFile >= 0 ? streamFileURL(info.infoHash, selectedFile) : ''
  const fileTitle = info && selectedFile >= 0
    ? (info.files[selectedFile]?.path?.split('/').pop() || info.name)
    : result.title

  // Walking through the audio tracks of the *current torrent* (e.g. all 12 tracks
  // of an album). Cross-torrent jumps (different playlist item) only happen when
  // we hit the boundaries of audioFileIndices.
  const AUDIO_RE = /\.(mp3|flac|m4a|aac|ogg|wav|opus|alac|wma)$/i
  const audioFileIndices = info
    ? info.files
        .map((f, i) => (AUDIO_RE.test(f.path) ? i : -1))
        .filter(i => i >= 0)
    : []
  const positionInTorrent = audioFileIndices.indexOf(selectedFile)
  const trackCountInTorrent = audioFileIndices.length
  const nextTrackInTorrent = positionInTorrent >= 0 && positionInTorrent < trackCountInTorrent - 1
    ? audioFileIndices[positionInTorrent + 1]
    : -1
  const prevTrackInTorrent = positionInTorrent > 0
    ? audioFileIndices[positionInTorrent - 1]
    : -1

  const playFileInTorrent = (idx: number) => {
    setSelectedFile(idx)
    setCurrentTime(0)
    setHasArtwork(null) // re-probe artwork for the new file
  }

  const handleEnded = () => {
    if (repeat === 'one' && audioRef.current) {
      audioRef.current.currentTime = 0
      audioRef.current.play().catch(() => {})
      return
    }
    // Advance through the torrent first; only jump playlists at the boundary.
    if (nextTrackInTorrent >= 0) {
      playFileInTorrent(nextTrackInTorrent)
      return
    }
    onPlaylistAdvance?.()
  }

  // Prev/Next bar buttons walk tracks-in-torrent first, then fall back to
  // the playlist's prev/next item (i.e. a different magnet).
  const handlePrev = () => {
    if (prevTrackInTorrent >= 0) { playFileInTorrent(prevTrackInTorrent); return }
    onPlaylistPrevious?.()
  }
  const handleNext = () => {
    if (nextTrackInTorrent >= 0) { playFileInTorrent(nextTrackInTorrent); return }
    onPlaylistAdvance?.()
  }

  const handleTimeUpdate = () => {
    const a = audioRef.current
    if (!a) return
    setCurrentTime(a.currentTime)
    setDuration(a.duration || 0)
    if (a.duration && a.duration > 0) {
      const ratio = a.currentTime / a.duration
      if (ratio > 0.5) {
        if (!prefetchedN1Ref.current && onPrefetchNextPlaylist) {
          prefetchedN1Ref.current = true
          onPrefetchNextPlaylist()
        }
      }
      if (ratio > 0.85 && !prefetchedN2Ref.current && onPrefetchNextNextPlaylist) {
        prefetchedN2Ref.current = true
        onPrefetchNextNextPlaylist()
      }
    }
  }

  return (
    <>
      {/* Expanded panel — semi-transparent overlay above the bar with queue + now-playing detail.
          Click outside or the close chevron retracts back to the compact bar. The <audio> stays
          mounted in the bar below so playback isn't interrupted by toggling expansion. */}
      {expanded && (
        <div
          className="fixed inset-x-0 bottom-0 z-40 flex flex-col bg-gray-900/98 backdrop-blur-md border-t border-gray-700 animate-[slideUp_180ms_ease-out]"
          style={{ top: 'max(env(safe-area-inset-top), 1rem)' }}
          onClick={(e) => { if (e.target === e.currentTarget) setExpanded(false) }}
        >
          {/* Header with close chevron */}
          <div className="flex items-center justify-between px-4 py-3 border-b border-gray-800 flex-shrink-0">
            <button
              onClick={() => setExpanded(false)}
              className="p-1.5 rounded hover:bg-gray-800 text-gray-400"
              title="Recolher player"
            >
              <ChevronDown className="w-5 h-5" />
            </button>
            <p className="text-xs text-gray-500 tracking-wider uppercase">
              {playlist ? `Tocando da playlist · ${playlist.name}` : 'Tocando agora'}
            </p>
            <div className="w-7" />{/* spacer to center the title */}
          </div>

          {/* Now-playing artwork + title block */}
          <div className="flex flex-col items-center gap-3 p-4 sm:p-6 border-b border-gray-800">
            <div className="w-48 h-48 sm:w-56 sm:h-56 rounded-xl shadow-2xl overflow-hidden bg-gray-800 flex items-center justify-center">
              {info && selectedFile >= 0 && hasArtwork ? (
                <img
                  src={streamArtworkURL(info.infoHash, selectedFile)}
                  alt="cover"
                  className="w-full h-full object-cover"
                />
              ) : (
                <Music className="w-20 h-20 text-gray-600" />
              )}
            </div>
            <div className="text-center min-w-0 max-w-xl">
              <p className="text-lg font-semibold text-gray-100 truncate" title={fileTitle}>{fileTitle}</p>
              <p className="text-sm text-gray-500 truncate">{info?.name || ''}</p>
              {duration > 0 && (
                <p className="text-xs text-gray-600 mt-1 tabular-nums">{formatDuration(currentTime)} / {formatDuration(duration)}</p>
              )}
            </div>
          </div>

          {/* Queues — two distinct lists when applicable:
                1) Tracks inside the CURRENT torrent (e.g. 12 mp3s of an album).
                   Click any track to jump straight to it.
                2) Upcoming items in the OUTER playlist (other torrents queued).
              The Disturbed case has only #1 (single playlist item with N audio files
              inside). For a multi-album playlist both sections appear stacked. */}
          {/* Diagnostic strip — shown when we DIDN'T render a queue. Helps the user
              understand whether the player is still loading metadata, or whether
              this torrent really has only one audio file. */}
          {!(audioFileIndices.length > 1 || (playlist && playlist.items.length > 1)) && (
            <div className="px-4 py-3 text-xs text-gray-500 text-center border-t border-gray-800">
              {!info ? (
                <span><Loader2 className="w-3 h-3 inline animate-spin mr-1" /> Carregando metadata do torrent...</span>
              ) : audioFileIndices.length === 1 ? (
                <span>Este torrent tem apenas 1 arquivo de áudio.</span>
              ) : audioFileIndices.length === 0 ? (
                <span>Nenhuma faixa de áudio detectada em {info.files.length} arquivo(s).</span>
              ) : (
                <span>Lista de faixas indisponível.</span>
              )}
            </div>
          )}
          {(audioFileIndices.length > 1 || (playlist && playlist.items.length > 1)) && (
            <div className="flex-1 overflow-y-auto min-h-0 pb-4">
              {/* In-torrent track list */}
              {audioFileIndices.length > 1 && info && (
                <div>
                  <div className="flex items-center gap-2 px-4 py-2 text-xs text-gray-500 sticky top-0 bg-gray-900/95 backdrop-blur z-[1]">
                    <ListMusic className="w-4 h-4" />
                    <span>
                      Faixas do álbum — {trackCountInTorrent} {trackCountInTorrent === 1 ? 'arquivo' : 'arquivos'}
                    </span>
                  </div>
                  <ul className="px-2">
                    {audioFileIndices.map((fileIdx, ord) => {
                      const isCurrent = fileIdx === selectedFile
                      const file = info.files[fileIdx]
                      const trackName = file?.path?.split('/').pop() || `Faixa ${ord + 1}`
                      return (
                        <li key={fileIdx}>
                          <button
                            onClick={() => playFileInTorrent(fileIdx)}
                            className={`w-full flex items-center gap-3 px-3 py-2 rounded-lg text-left ${
                              isCurrent
                                ? 'bg-green-500/15 text-green-200'
                                : 'text-gray-300 hover:bg-gray-800'
                            }`}
                          >
                            <span className="text-xs font-mono w-6 text-right flex-shrink-0">
                              {isCurrent ? '▶' : ord + 1}
                            </span>
                            <span className="text-sm truncate flex-1" title={trackName}>{trackName}</span>
                            {file && file.size > 0 && (
                              <span className="text-[10px] text-gray-600 flex-shrink-0 tabular-nums">
                                {(file.size / (1024 * 1024)).toFixed(1)} MB
                              </span>
                            )}
                          </button>
                        </li>
                      )
                    })}
                  </ul>
                </div>
              )}

              {/* Outer-playlist queue */}
              {playlist && playlist.items.length > 1 && (
                <div className={audioFileIndices.length > 1 ? 'border-t border-gray-800 mt-2 pt-1' : ''}>
                  <div className="flex items-center gap-2 px-4 py-2 text-xs text-gray-500 sticky top-0 bg-gray-900/95 backdrop-blur z-[1]">
                    <ListMusic className="w-4 h-4" />
                    <span>
                      Playlist — {playlist.currentIndex + 1}/{playlist.items.length}
                    </span>
                  </div>
                  <ul className="px-2">
                    {playlist.items.map((item, idx) => {
                      const isCurrent = idx === playlist.currentIndex
                      const hasPlayed = idx < playlist.currentIndex
                      return (
                        <li
                          key={idx}
                          className={`flex items-center gap-3 px-3 py-2 rounded-lg ${
                            isCurrent ? 'bg-green-500/10 text-green-200' :
                            hasPlayed ? 'text-gray-600' : 'text-gray-300'
                          }`}
                        >
                          <span className="text-xs font-mono w-6 text-right flex-shrink-0">
                            {isCurrent ? '▶' : idx + 1}
                          </span>
                          <span className="text-sm truncate flex-1" title={item.title}>{item.title}</span>
                        </li>
                      )
                    })}
                  </ul>
                </div>
              )}
            </div>
          )}
        </div>
      )}

    <div
      className="fixed bottom-0 inset-x-0 z-40 bg-gray-900/95 backdrop-blur-md border-t border-gray-700 px-3 py-2 safe-bottom"
      style={{ paddingBottom: 'max(env(safe-area-inset-bottom), 0.5rem)' }}
    >
      {/* Hidden <audio> — drives all playback */}
      <audio
        ref={audioRef}
        src={streamURL}
        autoPlay
        onPlay={() => setIsPlaying(true)}
        onPause={() => setIsPlaying(false)}
        onTimeUpdate={handleTimeUpdate}
        onLoadedMetadata={handleTimeUpdate}
        onEnded={handleEnded}
        preload="auto"
      />

      {/* Progress bar (thin, full-width, clickable to seek) */}
      <div
        className="h-1 bg-gray-700 rounded-full overflow-hidden mb-2 cursor-pointer"
        onClick={e => {
          const rect = (e.target as HTMLElement).getBoundingClientRect()
          seekFraction((e.clientX - rect.left) / rect.width)
        }}
      >
        <div
          className="h-full bg-green-500 transition-[width] duration-200"
          style={{ width: duration > 0 ? `${(currentTime / duration) * 100}%` : '0%' }}
        />
      </div>

      <div className="flex items-center gap-3 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto">
        {/* Artwork — clickable to expand the full now-playing view */}
        <button
          onClick={() => setExpanded(true)}
          className="w-12 h-12 rounded-md overflow-hidden flex-shrink-0 bg-gray-800 flex items-center justify-center hover:ring-2 hover:ring-green-500/50 transition-all"
          title="Expandir player"
        >
          {info && selectedFile >= 0 && hasArtwork ? (
            <img
              src={streamArtworkURL(info.infoHash, selectedFile)}
              alt="cover"
              className="w-full h-full object-cover"
            />
          ) : (
            <Music className="w-5 h-5 text-gray-500" />
          )}
        </button>

        {/* Title + playlist position — also clickable to expand */}
        <button
          onClick={() => setExpanded(true)}
          className="flex-1 min-w-0 text-left"
          title="Expandir player"
        >
          <p className="text-sm text-gray-100 truncate" title={fileTitle}>
            {loading ? 'Carregando...' : error ? <span className="text-red-400">{error}</span> : fileTitle}
          </p>
          <p className="text-[11px] text-gray-500 truncate">
            {/* Three optional segments separated by · :
                  1. Playlist name + position (only when there IS a playlist)
                  2. Torrent name (always, except when redundant with playlist)
                  3. Track X/Y inside the torrent (only when torrent has > 1 audio file)
                  4. Elapsed / total duration                                          */}
            {playlist && (
              <span>{playlist.name} · {playlist.currentIndex + 1}/{playlist.items.length} · </span>
            )}
            <span>{info?.name || ''}</span>
            {trackCountInTorrent > 1 && positionInTorrent >= 0 && (
              <span className="ml-1 text-green-400 tabular-nums">
                · faixa {positionInTorrent + 1}/{trackCountInTorrent}
              </span>
            )}
            {duration > 0 && (
              <span className="ml-2 tabular-nums">{formatDuration(currentTime)} / {formatDuration(duration)}</span>
            )}
          </p>
        </button>

        {/* Transport controls */}
        <div className="flex items-center gap-1 flex-shrink-0">
          {playlist && (
            <button
              onClick={onToggleShuffle}
              className={`p-1.5 rounded hover:bg-gray-800 hidden sm:block ${shuffle ? 'text-green-400' : 'text-gray-500'}`}
              title={shuffle ? 'Shuffle: ON' : 'Shuffle: OFF'}
            >
              <Shuffle className="w-4 h-4" />
            </button>
          )}
          {/* Prev/Next show whenever there's something to step through —
              tracks inside the torrent OR items in the playlist. */}
          {(trackCountInTorrent > 1 || playlist) && (
            <button
              onClick={handlePrev}
              className="p-1.5 rounded hover:bg-gray-800 text-gray-300"
              title="Anterior"
            >
              <SkipBack className="w-4 h-4" />
            </button>
          )}

          <button
            onClick={togglePlay}
            disabled={loading || !!error || !streamURL}
            className="p-2 rounded-full bg-green-500 hover:bg-green-400 text-gray-900 disabled:opacity-50 transition-colors"
            title={isPlaying ? 'Pausar' : 'Tocar'}
          >
            {loading
              ? <Loader2 className="w-4 h-4 animate-spin" />
              : isPlaying
                ? <Pause className="w-4 h-4" />
                : <Play className="w-4 h-4" />}
          </button>

          {(trackCountInTorrent > 1 || playlist) && (
            <button
              onClick={handleNext}
              className="p-1.5 rounded hover:bg-gray-800 text-gray-300"
              title="Próximo"
            >
              <SkipForward className="w-4 h-4" />
            </button>
          )}
          {playlist && (
            <button
              onClick={onCycleRepeat}
              className={`p-1.5 rounded hover:bg-gray-800 hidden sm:block relative ${repeat !== 'none' ? 'text-green-400' : 'text-gray-500'}`}
              title={`Repeat: ${repeat}`}
            >
              <Repeat className="w-4 h-4" />
              {repeat === 'one' && (
                <span className="absolute -bottom-0.5 -right-0.5 text-[8px] font-bold text-green-300">1</span>
              )}
            </button>
          )}

          {/* Speed dropdown */}
          <label className="hidden md:flex items-center gap-1 ml-1" title="Velocidade (pitch preservado)">
            <FastForward className="w-3.5 h-3.5 text-gray-500" />
            <select
              value={speed}
              onChange={e => setSpeed(parseFloat(e.target.value))}
              className="bg-gray-800 border border-gray-700 rounded px-1 py-0.5 text-xs text-gray-200 tabular-nums focus:outline-none focus:border-green-500"
            >
              {SPEED_OPTIONS.map(s => (
                <option key={s} value={s}>{s}x</option>
              ))}
            </select>
          </label>

          {/* Volume */}
          <button
            onClick={() => setMuted(m => !m)}
            className="hidden md:block p-1.5 rounded hover:bg-gray-800 text-gray-400"
            title={muted ? 'Desmutar' : 'Mutar'}
          >
            {muted ? <VolumeX className="w-4 h-4" /> : <Volume2 className="w-4 h-4" />}
          </button>
          <input
            type="range" min={0} max={1} step={0.01}
            value={muted ? 0 : volume}
            onChange={e => { setVolume(parseFloat(e.target.value)); setMuted(false) }}
            className="hidden md:block w-20 accent-green-500"
            aria-label="Volume"
          />

          {/* Expand / retract toggle */}
          <button
            onClick={() => setExpanded(e => !e)}
            className="p-1.5 rounded hover:bg-gray-800 text-gray-500 hover:text-gray-300"
            title={expanded ? 'Recolher player' : 'Expandir player'}
          >
            {expanded ? <ChevronDown className="w-4 h-4" /> : <ChevronUp className="w-4 h-4" />}
          </button>

          {/* Close */}
          <button
            onClick={onClose}
            className="p-1.5 rounded hover:bg-gray-800 text-gray-500 hover:text-gray-300 ml-1"
            title="Fechar player"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
      </div>
    </div>
    </>
  )
}

// Pick the first audio file in the torrent if there's no explicit override.
function pickAudioFile(t: TorrentInfo): number | null {
  const AUDIO = /\.(mp3|flac|m4a|aac|ogg|wav|opus|alac|wma)$/i
  for (let i = 0; i < t.files.length; i++) {
    if (AUDIO.test(t.files[i].path)) return i
  }
  return null
}
