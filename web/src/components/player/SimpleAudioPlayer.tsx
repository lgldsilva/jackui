import { useEffect, useRef, useState, useCallback } from 'react'
import { Play } from 'lucide-react'
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
  readonly className?: string
}

// Helpers puros extraídos para testes sem React Testing Library.
export const computeAudioPreload = (isWebKit: boolean, blessed: boolean): 'none' | 'auto' =>
  isWebKit && !blessed ? 'none' : 'auto'

export const computeEffectiveSrc = (
  isWebKit: boolean,
  blessed: boolean,
  src: string,
): string | undefined => (isWebKit && !blessed ? undefined : src || undefined)

export const shouldShowAudioOverlay = (
  isWebKit: boolean,
  blessed: boolean,
  startOverlayDismissed: boolean,
  errored: boolean,
): boolean => isWebKit && !blessed && !startOverlayDismissed && !errored

// SimpleAudioPlayer: player de áudio "pelado" inspirado no audiotest.html.
// Usa um <audio controls> nativo com src DIRECT. No iOS/WebKit o elemento só
// ganha src real DENTRO do primeiro gesto do usuário, exatamente como o teste
// que comprovadamente toca no iOS 18. Nada de Web Audio, gapless, HLS.js,
// <track> ou load() — tudo o que quebrava o áudio no iPhone.
export function SimpleAudioPlayer({
  src,
  autoAdvance = true,
  onEnded,
  onTimeUpdate,
  onPlaying,
  onPause,
  onError,
  className = '',
}: SimpleAudioPlayerProps) {
  const audioRef = useRef<HTMLAudioElement>(null)
  // Última URL (relativa) já anexada ao elemento. `el.src` (getter) é ABSOLUTO, então
  // comparar `el.src !== effectiveSrc` (relativo) reanexava o src a cada render e podia
  // abortar o play (reload). Comparar contra esta ref evita isso.
  const attachedSrcRef = useRef('')
  const isWebKit = isSafariBrowser() || isIOS()
  const [blessed, setBlessed] = useState(false)
  const [startOverlayDismissed, setStartOverlayDismissed] = useState(false)
  const [errored, setErrored] = useState(false)

  // iOS/WebKit: não setamos src antes do primeiro gesto. O elemento existe no
  // DOM com preload='none' e sem src, então não pré-carrega e não estaciona em
  // readyState 2. O tap chama startPlayback(), que seta src e play() no mesmo
  // gesto — espelhando o audiotest.html.
  const effectiveSrc = computeEffectiveSrc(isWebKit, blessed, src)
  const preload = computeAudioPreload(isWebKit, blessed)

  const startPlayback = useCallback(() => {
    const el = audioRef.current
    if (!el) return
    setStartOverlayDismissed(true)
    clientLog('info', 'audio', 'tap-to-play (gesto)', { readyState: el.readyState, hadSrc: !!el.src })
    if (isWebKit && !blessed) {
      el.src = src
      attachedSrcRef.current = src
    }
    el.play()
      .then(() => {
        clientLog('info', 'audio', 'tap-to-play ok', { readyState: el.readyState })
        setBlessed(true)
        onPlaying?.()
      })
      .catch((e) => {
        clientLog('warn', 'audio', 'tap-to-play falhou', { err: String(e) })
        setStartOverlayDismissed(false)
      })
  }, [audioRef, src, blessed, isWebKit, onPlaying])

  // Anexa o src quando ele muda DE VERDADE (vs. a última URL relativa anexada).
  // Depois do 1º gesto (blessed), TOCA a faixa nova sozinha — auto-avanço: a Apple
  // libera play() programático no MESMO elemento pós-gesto. Antes do gesto no WebKit
  // o effectiveSrc é undefined, então isto só dispara em troca de faixa pós-tap (ou
  // no desktop, onde blessed vira true no 1º 'playing' nativo).
  useEffect(() => {
    const el = audioRef.current
    if (!el || !effectiveSrc) return
    if (attachedSrcRef.current === effectiveSrc) return
    attachedSrcRef.current = effectiveSrc
    el.src = effectiveSrc
    if (blessed) {
      el.play().catch((e) => clientLog('warn', 'audio', 'auto-advance play falhou', { err: String(e) }))
    }
  }, [effectiveSrc, blessed])

  useEffect(() => {
    const el = audioRef.current
    if (!el) return
    const handleTimeUpdate = () => {
      onTimeUpdate?.(el.currentTime, el.duration || 0)
    }
    const handleEnded = () => {
      clientLog('info', 'audio', 'ended')
      if (autoAdvance) onEnded?.()
    }
    const handleError = () => {
      clientLog('warn', 'audio', 'error', { code: el.error?.code })
      setErrored(true)
      onError?.()
    }
    const handlePlaying = () => {
      setErrored(false)
      // Qualquer 1º play (tap no iOS OU play nativo no desktop) marca blessed → o
      // efeito de src passa a tocar a faixa nova sozinho (auto-avanço em todas as plataformas).
      setBlessed(true)
      onPlaying?.()
    }
    const handlePause = () => {
      onPause?.()
    }
    el.addEventListener('timeupdate', handleTimeUpdate)
    el.addEventListener('ended', handleEnded)
    el.addEventListener('error', handleError)
    el.addEventListener('playing', handlePlaying)
    el.addEventListener('pause', handlePause)
    return () => {
      el.removeEventListener('timeupdate', handleTimeUpdate)
      el.removeEventListener('ended', handleEnded)
      el.removeEventListener('error', handleError)
      el.removeEventListener('playing', handlePlaying)
      el.removeEventListener('pause', handlePause)
    }
  }, [autoAdvance, onEnded, onTimeUpdate, onPlaying, onPause, onError])

  const showOverlay = shouldShowAudioOverlay(isWebKit, blessed, startOverlayDismissed, errored)

  return (
    <div className={`relative ${className}`}>
      <audio
        ref={audioRef}
        controls
        preload={preload}
        className="w-full h-full"
      />
      {showOverlay && (
        <button
          type="button"
          onClick={startPlayback}
          className="absolute inset-0 flex items-center justify-center bg-black/40 hover:bg-black/50 transition-colors"
          aria-label="Tocar"
        >
          <div className="flex items-center gap-2 px-4 py-2 bg-purple-600 hover:bg-purple-500 text-white rounded-full">
            <Play className="w-5 h-5 fill-current" />
            <span className="text-sm font-medium">Tocar</span>
          </div>
        </button>
      )}
    </div>
  )
}
