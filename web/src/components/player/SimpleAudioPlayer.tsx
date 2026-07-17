import { useEffect, useRef } from 'react'
import { isIOS, isSafariBrowser } from '../../api/client'
import { clientLog } from '../../lib/diag'

type SimpleAudioPlayerProps = {
  readonly src: string
  readonly autoAdvance?: boolean
  readonly onEnded?: () => void
  readonly onTimeUpdate?: (currentTime: number, duration: number) => void
  readonly onPlaying?: () => void
  readonly onPause?: () => void
  readonly onError?: () => void
  // Espelha o <audio> real para o pai (callback ref) — sem mexer na máquina iOS.
  // Permite ao PlayerModal controlar play/pause/seek (MediaSession, atalhos) e
  // o replay do repeat-one no MESMO elemento que o usuário "abençoou" com o gesto.
  readonly elementRef?: (el: HTMLAudioElement | null) => void
  readonly className?: string
}

// SimpleAudioPlayer: <audio controls> NATIVO direto. O src é declarativo e o usuário
// toca no PLAY NATIVO — que no iOS já É o gesto que o WebKit exige pra tocar com som.
// Sem overlay custom, sem v.load(), sem máquina de gesto. No iOS o preload é 'none'
// (não pré-carrega → não estaciona em readyState 2; o play nativo dispara um load
// FRESCO dentro do gesto). Depois do 1º play (blessed), a faixa seguinte toca sozinha
// (auto-avanço: a Apple libera o play() programático no mesmo elemento pós-gesto).
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

  // Auto-avanço: quando o src muda E já tocou uma vez (blessed), toca a faixa nova.
  // Antes do 1º play NÃO auto-toca — o usuário usa o play nativo (gesto). O guard
  // attachedSrcRef evita re-disparar no mesmo src (re-render).
  useEffect(() => {
    const el = audioRef.current
    if (!el || !src) return
    if (attachedSrcRef.current === src) return
    attachedSrcRef.current = src
    if (blessedRef.current) {
      el.play().catch((e) => clientLog('warn', 'audio', 'auto-advance play falhou', { err: String(e) }))
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
