import { useEffect, useRef } from 'react'
import { isIOS, isSafariBrowser } from '../../api/client'
import { clientLog } from '../../lib/diag'
import { usePersistedVolume } from './usePersistedVolume'

type SimpleAudioPlayerProps = {
  readonly src: string
  readonly autoAdvance?: boolean
  readonly onEnded?: () => void
  readonly onTimeUpdate?: (currentTime: number, duration: number) => void
  readonly onPlaying?: () => void
  readonly onPause?: () => void
  readonly onError?: () => void
  // Mirrors the real <audio> to the parent (callback ref) — without touching the iOS machine.
  // Lets PlayerModal control play/pause/seek (MediaSession, shortcuts) and
  // repeat-one's replay on the SAME element the user "blessed" with the gesture.
  readonly elementRef?: (el: HTMLAudioElement | null) => void
  readonly className?: string
}

// SimpleAudioPlayer: direct NATIVE <audio controls>. The src is declarative and the user
// presses the NATIVE play — which on iOS already IS the gesture WebKit requires to play with sound.
// No custom overlay, no v.load(), no gesture machine. On iOS preload is 'none'
// (no pre-fetch → doesn't park at readyState 2; the native play fires a FRESH load
// inside the gesture). After the 1st play (blessed), the next track plays by itself
// (auto-advance: Apple allows programmatic play() on the same element post-gesture).
export function SimpleAudioPlayer({
  src,
  autoAdvance = true,
  onEnded,
  onTimeUpdate,
  onPlaying,
  onPause,
  onError,
  elementRef,
  className = '',
}: SimpleAudioPlayerProps) {
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const isWebKit = isSafariBrowser() || isIOS()
  const blessedRef = useRef(false)
  const attachedSrcRef = useRef('')

  // Mute/volume are a user preference: kept across tracks and across
  // player sessions (the native controls are the mute path here).
  usePersistedVolume({ mediaRef: audioRef, elementKey: src })

  // Auto-advance: when the src changes AND it has played once (blessed), plays the new track.
  // Before the 1st play it does NOT auto-play — the user uses the native play (gesture). The
  // attachedSrcRef guard avoids re-firing on the same src (re-render).
  useEffect(() => {
    const el = audioRef.current
    if (!el || !src) return
    if (attachedSrcRef.current === src) return
    attachedSrcRef.current = src
    if (blessedRef.current) {
      el.play().catch((e) => clientLog('warn', 'audio', 'auto-advance play failed', { err: String(e) }))
    }
  }, [src])

  useEffect(() => {
    const el = audioRef.current
    if (!el) return
    const onTime = () => onTimeUpdate?.(el.currentTime, el.duration || 0)
    const onEnd = () => { if (autoAdvance) onEnded?.() }
    const onErr = () => { clientLog('warn', 'audio', 'error', { code: el.error?.code }); onError?.() }
    const onPlay = () => { blessedRef.current = true; onPlaying?.() }
    const onPauseEv = () => onPause?.()
    el.addEventListener('timeupdate', onTime)
    el.addEventListener('ended', onEnd)
    el.addEventListener('error', onErr)
    el.addEventListener('playing', onPlay)
    el.addEventListener('pause', onPauseEv)
    return () => {
      el.removeEventListener('timeupdate', onTime)
      el.removeEventListener('ended', onEnd)
      el.removeEventListener('error', onErr)
      el.removeEventListener('playing', onPlay)
      el.removeEventListener('pause', onPauseEv)
    }
  }, [autoAdvance, onEnded, onTimeUpdate, onPlaying, onPause, onError])

  return (
    <audio
      ref={(el) => { audioRef.current = el; elementRef?.(el) }}
      src={src || undefined}
      controls
      preload={isWebKit ? 'none' : 'metadata'}
      className={`w-full ${className}`}
    >
      {/* Captions track required by a11y rules; pure audio has no timed text. */}
      <track kind="captions" src="data:text/vtt,WEBVTT" srcLang="und" label="None" />
    </audio>
  )
}
