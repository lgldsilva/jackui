import { Loader2, Cpu, ChevronLeft, ChevronRight, Airplay, Play } from 'lucide-react'
import { TorrentInfo } from '../../api/client'
import { formatRate } from '../../lib/format'
import { formatSize } from './playerFormat'
import { type AirPlayState } from './playerHooks'

// Resume-from-position prompt shown over the video when reopening a torrent
// with a saved playhead. Extracted to keep VideoPlayerElement's cognitive
// complexity low (zero visual change vs the inline block).
export type ResumePromptProps = {
  readonly resumePosition: number
  readonly formatTime: (s: number) => string
  readonly onContinue: (pos: number) => void
  readonly onRestart: () => void
}

export function ResumePrompt({ resumePosition, formatTime, onContinue, onRestart }: ResumePromptProps) {
  return (
    <div className="absolute inset-0 z-30 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4">
      <div className="bg-surface-secondary border border-default rounded-2xl p-5 flex flex-col gap-3 w-full max-w-xs">
        <p className="text-text-primary text-sm text-center">Você parou em</p>
        <p className="text-blue-300 text-center font-mono text-2xl">{formatTime(resumePosition)}</p>
        <button
          onClick={() => onContinue(resumePosition)}
          className="btn-primary w-full justify-center"
        >
          Continuar
        </button>
        <button
          onClick={onRestart}
          className="btn-secondary w-full justify-center"
        >
          Começar do início
        </button>
      </div>
    </div>
  )
}

// StartAudioOverlay: botão grande "Tocar" sobre a capa, mostrado no iOS-áudio
// quando a faixa abriu mas ainda não tocou. O iOS proíbe play() de áudio fora de
// um gesto, então o onClick (gesto) é o que de fato inicia a reprodução com som —
// não é só estética: é o caminho de start no iPhone/iPad. Espelha o ResumePrompt.
export function StartAudioOverlay({ onPlay }: { readonly onPlay: () => void }) {
  return (
    <button
      onClick={onPlay}
      aria-label="Tocar"
      className="absolute inset-0 z-30 flex flex-col items-center justify-center gap-3 bg-black/45 backdrop-blur-sm"
    >
      <span className="flex items-center justify-center w-20 h-20 rounded-full bg-purple-600 text-white shadow-2xl hover:bg-purple-500 transition-colors">
        <Play className="w-10 h-10 ml-1" />
      </span>
      <span className="text-text-primary text-sm font-medium">Tocar</span>
    </button>
  )
}

type PlayerLoadingOverlayProps = {
  readonly serverReady: boolean
  readonly resumePosition: number | null
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly isTranscoded: boolean
  readonly transcodeFallbackAttempted: boolean
  readonly formatTime: (s: number) => string
}

// Loading overlay shown while the first pieces download (currentTime/bufferedEnd
// still 0). Extracted from VideoPlayerElement — it concentrated ~7 nested
// conditionals/ternaries (swarm stats, downRate, transcode hint) that inflated
// the cognitive complexity. JSX/classes/texts kept IDENTICAL.
export function PlayerLoadingOverlay({
  serverReady,
  resumePosition,
  info,
  selectedFile,
  isTranscoded,
  transcodeFallbackAttempted,
  formatTime,
}: PlayerLoadingOverlayProps) {
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none z-10 bg-black/40">
      <Loader2 className="w-12 h-12 animate-spin text-green-500 mb-3" />
      <p className="text-text-primary font-medium">
        {serverReady ? 'Baixando primeiras peças do torrent...' : 'Conectando ao swarm...'}
      </p>
      {resumePosition !== null && (
        <p className="text-xs text-blue-300 mt-2">
          Continuando de {formatTime(resumePosition)}
        </p>
      )}
      <p className="text-xs text-text-secondary mt-1">
        {info && info.peers > 0
          ? `${info.seeders} seeders / ${info.peers} peers conectados`
          : 'Aguardando peers...'}
      </p>
      {info && info.downRate > 0 && (
        <p className="text-[11px] text-text-secondary mt-1 tabular-nums">
          <span className="text-green-400">↓ {formatRate(info.downRate)}</span>
          {info.files?.[selectedFile] && (
            <span className="text-text-muted"> · {formatSize(info.files[selectedFile].downloaded)} em buffer</span>
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
  )
}

// Prev/next navigation for the in-torrent queue (episodes/tracks) shown in the
// video transport row. Extracted from PlayerControlsPanel so its conditional
// JSX (the show-gate + episode label + position) lives here instead of inflating
// that panel's cognitive complexity (keeps it under the gate).
export function MediaNavButtons({ mediaFileIndices, mediaCursor, currentEp, onPrevMedia, onNextMedia, hasPrevMedia, hasNextMedia }: {
  readonly mediaFileIndices: number[]
  readonly mediaCursor: number
  readonly currentEp: string | null
  readonly onPrevMedia: () => void
  readonly onNextMedia: () => void
  readonly hasPrevMedia: boolean
  readonly hasNextMedia: boolean
}) {
  if (mediaFileIndices.length <= 1 && !hasPrevMedia && !hasNextMedia) return null
  return (
    <>
      <button
        onClick={onPrevMedia}
        disabled={!hasPrevMedia}
        title="Episódio anterior"
        className="flex items-center gap-1 text-sm sm:text-xs bg-blue-500/20 hover:bg-blue-500/30 text-blue-700 dark:text-blue-300 border border-blue-500/30 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors disabled:opacity-30 flex-shrink-0"
      >
        <ChevronLeft className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
        <span className="hidden xs:inline">Ep ant.</span>
      </button>
      <button
        onClick={onNextMedia}
        disabled={!hasNextMedia}
        title="Próximo episódio"
        className="flex items-center gap-1 text-sm sm:text-xs bg-blue-500/20 hover:bg-blue-500/30 text-blue-700 dark:text-blue-300 border border-blue-500/30 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors disabled:opacity-30 flex-shrink-0"
      >
        <span className="hidden xs:inline">Próx.</span>
        <ChevronRight className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
      </button>
      {currentEp && (
        <span className="text-xs text-blue-700 dark:text-blue-300 px-2 py-1 bg-blue-500/10 rounded border border-blue-500/20 font-mono flex-shrink-0">
          {currentEp}
        </span>
      )}
      {mediaFileIndices.length > 1 && (
        <span className="text-xs text-text-muted flex-shrink-0">
          {mediaCursor + 1}/{mediaFileIndices.length}
        </span>
      )}
    </>
  )
}

// Small presentational overlays, extracted from VideoPlayerElement so its
// cognitive complexity stays under the gate (each one keeps its own guard
// instead of a `cond && (...)` inline in the player's JSX).
export function TranscodingBadge({ attempted, videoError }: { readonly attempted: boolean; readonly videoError: boolean }) {
  if (!attempted || videoError) return null
  return (
    <div className="absolute top-2 right-2 bg-purple-600/85 text-white text-[10px] px-2 py-1 rounded-md flex items-center gap-1 backdrop-blur-sm pointer-events-none z-20">
      <Cpu className="w-3 h-3" />
      Convertendo via GPU
    </div>
  )
}

export function AirPlayButton({ airplay, videoError }: { readonly airplay: AirPlayState; readonly videoError: boolean }) {
  if (!airplay.available || videoError) return null
  return (
    <button
      onClick={airplay.show}
      title={airplay.active ? 'Transmitindo via AirPlay' : 'Transmitir via AirPlay'}
      className={`absolute top-2 left-2 z-20 p-2 rounded-md backdrop-blur-sm transition-colors ${airplay.active ? 'bg-blue-600/85 text-white' : 'bg-black/55 text-white hover:bg-black/75'}`}
    >
      <Airplay className="w-4 h-4" />
    </button>
  )
}
