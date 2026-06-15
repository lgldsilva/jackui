import { RefObject, useEffect, useState } from 'react'
import { Pause, Play, Repeat, Shuffle, SkipBack, SkipForward } from 'lucide-react'
import { TorrentInfo, streamArtworkURL } from '../../api/client'

type AudioTransportBarProps = {
  // HTMLMediaElement: o <video> normal OU o <audio> ativo do motor gapless.
  readonly videoRef: RefObject<HTMLMediaElement | null>
  readonly info: TorrentInfo
  readonly selectedFile: number
  readonly mediaToken: string | null
  readonly currentTime: number
  readonly duration: number
  readonly formatTime: (s: number) => string
  readonly onPrev: () => void
  readonly onNext: () => void
  readonly hasPrev: boolean
  readonly hasNext: boolean
  /** 1-based position label within the current queue (e.g. "3 / 12"); empty hides it. */
  readonly queueLabel?: string
  readonly shuffle?: boolean
  readonly repeat?: 'none' | 'one' | 'all'
  readonly onToggleShuffle?: () => void
  readonly onCycleRepeat?: () => void
  /** Compact = mini-player dock (single row); otherwise the full "music mode" bar. */
  readonly compact?: boolean
}

// usePausedState mirrors the <video>'s play/pause into React so the custom
// transport button shows the right icon. Re-syncs when the source file changes
// (the same <video> element is reused, only its src swaps).
function usePausedState(videoRef: RefObject<HTMLMediaElement | null>, selectedFile: number): boolean {
  const [paused, setPaused] = useState(true)
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    setPaused(v.paused)
    const onPlay = () => setPaused(false)
    const onPause = () => setPaused(true)
    v.addEventListener('play', onPlay)
    v.addEventListener('pause', onPause)
    return () => {
      v.removeEventListener('play', onPlay)
      v.removeEventListener('pause', onPause)
    }
  }, [videoRef, selectedFile])
  return paused
}

// Custom transport for AUDIO playback. The native <video controls> is great for
// video (fullscreen + AirPlay on iOS) but ugly overlaid on an album cover, and
// it has no track navigation. In audio mode the <video> renders WITHOUT controls
// and this bar drives it via videoRef: play/pause, seek, and ⏮⏭ across the
// unified queue (album tracks → playlist). Reused compact as the mini-player.
export function AudioTransportBar({
  videoRef,
  info,
  selectedFile,
  mediaToken,
  currentTime,
  duration,
  formatTime,
  onPrev,
  onNext,
  hasPrev,
  hasNext,
  queueLabel,
  shuffle = false,
  repeat = 'none',
  onToggleShuffle,
  onCycleRepeat,
  compact = false,
}: AudioTransportBarProps) {
  const paused = usePausedState(videoRef, selectedFile)
  const togglePlay = () => {
    const v = videoRef.current
    if (!v) return
    if (v.paused) v.play().catch(() => {})
    else v.pause()
  }
  const seek = (t: number) => {
    const v = videoRef.current
    if (v && Number.isFinite(t)) v.currentTime = t
  }
  const trackName = info.files[selectedFile]?.path?.split('/').pop() || info.name
  const max = Math.max(duration, 0)

  const seekInput = (
    <input
      type="range"
      min={0}
      max={max || 1}
      step={0.1}
      value={Math.min(currentTime, max || currentTime)}
      onChange={e => seek(Number.parseFloat(e.target.value))}
      aria-label="Posição da faixa"
      className="flex-1 h-1.5 accent-purple-500 cursor-pointer"
    />
  )

  if (compact) {
    // Mini-player dock: one tight row under the cover (replaces the old
    // time-only MinimizedAudioProgress). Play/pause + prev/next + slim seek.
    return (
      <div className="px-3 py-1.5 bg-surface border-t border-default flex items-center gap-2 text-xs text-text-secondary">
        <button onClick={onPrev} disabled={!hasPrev} title="Anterior" className="p-1 text-text-secondary hover:text-text-primary disabled:opacity-30">
          <SkipBack className="w-4 h-4" />
        </button>
        <button onClick={togglePlay} title={paused ? 'Tocar' : 'Pausar'} className="p-1 text-text-primary hover:text-purple-400">
          {paused ? <Play className="w-5 h-5" /> : <Pause className="w-5 h-5" />}
        </button>
        <button onClick={onNext} disabled={!hasNext} title="Próxima" className="p-1 text-text-secondary hover:text-text-primary disabled:opacity-30">
          <SkipForward className="w-4 h-4" />
        </button>
        <span className="font-mono tabular-nums">{formatTime(currentTime)}</span>
        {seekInput}
        <span className="font-mono tabular-nums">{formatTime(duration)}</span>
      </div>
    )
  }

  return (
    <div className="px-3 sm:px-4 py-3 bg-surface border-b border-default flex flex-col gap-2">
      {/* Track title + queue position */}
      <div className="flex items-center gap-2 min-w-0">
        <img
          src={streamArtworkURL(info.infoHash, selectedFile, mediaToken || undefined)}
          alt=""
          className="w-10 h-10 rounded object-cover bg-surface-tertiary flex-shrink-0"
          onError={e => { e.currentTarget.style.visibility = 'hidden' }}
        />
        <div className="min-w-0 flex-1">
          <p className="text-sm text-text-primary truncate" title={trackName}>{trackName}</p>
          {queueLabel && <p className="text-xs text-text-muted tabular-nums">{queueLabel}</p>}
        </div>
      </div>

      {/* Transport row: shuffle · prev · play/pause · next · repeat */}
      <div className="flex items-center justify-center gap-3">
        <button
          onClick={onToggleShuffle}
          title={shuffle ? 'Shuffle: ON' : 'Shuffle: OFF'}
          className={`p-2 rounded hover:bg-surface-secondary ${shuffle ? 'text-green-400' : 'text-text-muted'}`}
        >
          <Shuffle className="w-4 h-4" />
        </button>
        <button onClick={onPrev} disabled={!hasPrev} title="Faixa anterior" className="p-2 rounded text-text-primary hover:bg-surface-secondary disabled:opacity-30">
          <SkipBack className="w-5 h-5" />
        </button>
        <button
          onClick={togglePlay}
          title={paused ? 'Tocar' : 'Pausar'}
          className="flex items-center justify-center w-12 h-12 rounded-full bg-purple-600 text-white hover:bg-purple-500 transition-colors"
        >
          {paused ? <Play className="w-6 h-6 ml-0.5" /> : <Pause className="w-6 h-6" />}
        </button>
        <button onClick={onNext} disabled={!hasNext} title="Próxima faixa" className="p-2 rounded text-text-primary hover:bg-surface-secondary disabled:opacity-30">
          <SkipForward className="w-5 h-5" />
        </button>
        <button
          onClick={onCycleRepeat}
          title={`Repeat: ${repeat}`}
          className={`p-2 rounded hover:bg-surface-secondary relative ${repeat === 'none' ? 'text-text-muted' : 'text-green-400'}`}
        >
          <Repeat className="w-4 h-4" />
          {repeat === 'one' && <span className="absolute bottom-0.5 right-0.5 text-[8px] font-bold text-green-400">1</span>}
        </button>
      </div>

      {/* Seekbar */}
      <div className="flex items-center gap-2 text-xs text-text-secondary">
        <span className="font-mono tabular-nums w-10 text-right">{formatTime(currentTime)}</span>
        {seekInput}
        <span className="font-mono tabular-nums w-10">{formatTime(duration)}</span>
      </div>
    </div>
  )
}
