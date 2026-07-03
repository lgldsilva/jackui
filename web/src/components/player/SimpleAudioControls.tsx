import { SkipBack, SkipForward, Shuffle, Repeat } from 'lucide-react'
import { useTranslation } from 'react-i18next'

// SimpleAudioControls: barra de transporte MÍNIMA para o modo áudio. Só prev/next
// (+ shuffle/repeat + posição). NÃO toca no elemento <audio> (play/pause/seek ficam
// nos controls nativos do SimpleAudioPlayer) — são apenas botões que trocam a FAIXA
// via os handlers do pai (handlePrev/handleNext mudam selectedFile → o src do
// SimpleAudioPlayer muda → ele toca a nova faixa). Zero Web Audio → iOS-safe.
// Substitui o prev/next que a AudioTransportBar (removida na simplificação) dava.
type SimpleAudioControlsProps = {
  readonly onPrev: () => void
  readonly onNext: () => void
  readonly hasPrev: boolean
  readonly hasNext: boolean
  readonly shuffle?: boolean
  readonly repeat?: 'none' | 'one' | 'all'
  readonly onToggleShuffle?: () => void
  readonly onCycleRepeat?: () => void
  readonly position?: string
  readonly className?: string
}

const smallBtn = 'flex items-center justify-center min-w-[40px] min-h-[40px] p-2 rounded-full transition-colors disabled:opacity-30 disabled:cursor-not-allowed'

export function SimpleAudioControls({
  onPrev,
  onNext,
  hasPrev,
  hasNext,
  shuffle = false,
  repeat = 'none',
  onToggleShuffle,
  onCycleRepeat,
  position,
  className = '',
}: SimpleAudioControlsProps) {
  const { t } = useTranslation()
  return (
    <div className={`flex items-center justify-center gap-3 sm:gap-4 py-3 ${className}`}>
      {onToggleShuffle && (
        <button
          type="button"
          onClick={onToggleShuffle}
          title={shuffle ? t('player.controls.shuffleOn') : t('player.controls.shuffleOff')}
          aria-label={t('player.controls.shuffle')}
          className={`${smallBtn} hover:bg-blue-500/20 ${shuffle ? 'text-green-600 dark:text-green-300' : 'text-text-muted'}`}
        >
          <Shuffle className="w-4 h-4" />
        </button>
      )}
      <button
        type="button"
        onClick={onPrev}
        disabled={!hasPrev}
        title={t('player.controls.prev')}
        aria-label={t('player.controls.prev')}
        className={`${smallBtn} bg-blue-500/15 hover:bg-blue-500/30 text-blue-700 dark:text-blue-200`}
      >
        <SkipBack className="w-5 h-5 fill-current" />
      </button>
      <button
        type="button"
        onClick={onNext}
        disabled={!hasNext}
        title={t('player.controls.next')}
        aria-label={t('player.controls.next')}
        className={`${smallBtn} bg-blue-500/15 hover:bg-blue-500/30 text-blue-700 dark:text-blue-200`}
      >
        <SkipForward className="w-5 h-5 fill-current" />
      </button>
      {onCycleRepeat && (
        <button
          type="button"
          onClick={onCycleRepeat}
          title={t('player.controls.repeatMode', { mode: repeat })}
          aria-label={t('player.controls.repeat')}
          className={`${smallBtn} relative hover:bg-blue-500/20 ${repeat === 'none' ? 'text-text-muted' : 'text-green-600 dark:text-green-300'}`}
        >
          <Repeat className="w-4 h-4" />
          {repeat === 'one' && (
            <span className="absolute bottom-0.5 right-0.5 text-[8px] font-bold text-green-600 dark:text-green-300">1</span>
          )}
        </button>
      )}
      {position && (
        <span className="text-xs text-text-muted tabular-nums ml-1">{position}</span>
      )}
    </div>
  )
}
