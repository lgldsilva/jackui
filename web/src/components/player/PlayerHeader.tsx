import { X, Play, Cpu, Heart, EyeOff, Eye, Info, Maximize2, Minimize2, Home } from 'lucide-react'
import { SearchResult, TorrentInfo, TranscodeCapabilities } from '../../api/client'
import type { TFn } from './playerTypes'

// minimizedOrFullClass returns the outer panel classes for the player shell:
// minimized (audio bar / video PiP), full-viewport (deep-link tab — fills the
// whole browser window), or the default centered modal.
export function minimizedOrFullClass(minimized: boolean, audioMode: boolean, fullViewport: boolean): string {
  if (minimized) {
    return audioMode
      ? 'bg-surface-secondary rounded-t-xl border-t border-default shadow-2xl w-full flex flex-col overflow-hidden pb-[env(safe-area-inset-bottom,0px)]'
      : 'bg-surface-secondary rounded-xl border border-default shadow-2xl w-full flex flex-col overflow-hidden'
  }
  if (fullViewport) {
    return 'bg-surface-secondary w-full h-full max-w-none rounded-none border-0 min-h-0 flex flex-col'
  }
  return 'bg-surface-secondary rounded-none sm:rounded-2xl border-0 sm:border border-default w-full max-w-4xl lg:max-w-6xl 2xl:max-w-[min(90vw,1600px)] shadow-2xl sm:h-auto sm:max-h-[90vh] min-h-0 flex flex-col animate-[player-expand_320ms_cubic-bezier(0.16,1,0.3,1)]'
}

// Container/overlay attributes for the modal shell: minimized (audio bar / video
// PiP), full-viewport (deep-link tab — no backdrop dismiss, Escape goes Home), or
// the default centered modal (backdrop click / Escape minimizes).
export function shellProps(opts: {
  minimized: boolean
  audioMode: boolean
  fullViewport: boolean
  onHome?: () => void
  onClose: () => void
  setMinimized: (v: boolean | ((prev: boolean) => boolean)) => void
}): React.HTMLAttributes<HTMLDivElement> {
  const { minimized, audioMode, fullViewport, onHome, onClose, setMinimized } = opts
  if (minimized) {
    // Áudio: barra fina full-width colada no footer (acima da nav inferior, se houver)
    // — o "mini-player" de música de verdade. Vídeo: card PiP no canto (um vídeo numa
    // barra fina não faz sentido).
    if (audioMode) {
      return { className: 'fixed inset-x-0 z-50', style: { bottom: 'var(--bottom-bar-h, 0px)' } }
    }
    return { className: 'fixed right-3 z-50 w-[360px] max-w-[calc(100vw-1.5rem)]', style: { bottom: 'calc(0.75rem + var(--bottom-bar-h, 0px) + env(safe-area-inset-bottom, 0px))' } }
  }
  // Full-viewport (deep-link tab): fill the whole browser window — solid black,
  // no padding, no backdrop-click-to-minimize (the tab is dedicated to playback;
  // exit is the Home button). Escape goes Home.
  if (fullViewport) {
    return {
      className: 'fixed inset-0 bg-black flex items-stretch justify-center z-50',
      onKeyDown: (e) => { if (e.key === 'Escape') (onHome ?? onClose)() },
      role: 'dialog',
      'aria-modal': 'true',
      tabIndex: -1,
    }
  }
  return {
    className: 'fixed inset-0 bg-black/80 backdrop-blur-sm flex items-stretch sm:items-center justify-center z-50 sm:p-4',
    onClick: (e) => { if (e.target === e.currentTarget) setMinimized(true) },
    onKeyDown: (e) => { if (e.key === 'Escape') setMinimized(true) },
    role: 'dialog',
    'aria-modal': 'true',
    tabIndex: -1,
  }
}

export function renderPlayerHeader(props: {
  minimized: boolean
  info: TorrentInfo | null
  result: SearchResult
  isTranscoded: boolean
  caps: TranscodeCapabilities | null
  encoderLabel: string
  isFavorite: boolean
  toggleFavorite: () => void
  incognito: boolean
  setIncognito: (v: boolean) => void
  setMinimized: (v: boolean | ((prev: boolean) => boolean)) => void
  onClose: () => void
  onShowInfo: () => void
  headerRef: React.RefObject<HTMLDivElement>
  fullViewport?: boolean
  onHome?: () => void
  t: TFn
}) {
  const { minimized, info, result, isTranscoded, caps, encoderLabel, isFavorite, toggleFavorite, incognito, setIncognito, setMinimized, onClose, onShowInfo, headerRef, fullViewport, onHome, t } = props
  if (minimized) return null
  return (
    <div ref={headerRef} className="flex items-center justify-between px-4 pb-4 pt-statusbar sm:!pt-4 border-b border-default flex-shrink-0 touch-pan-y">
      <h2 className="text-base font-semibold text-text-primary flex items-center gap-2 min-w-0">
        <Play className="w-4 h-4 text-green-500 flex-shrink-0" />
        <span className="truncate">{info?.name || result.title}</span>
        {isTranscoded && caps?.preferred && <span className="text-[10px] bg-purple-500/20 text-purple-700 dark:text-purple-300 border border-purple-500/30 px-1.5 py-0.5 rounded flex items-center gap-1 flex-shrink-0" title={t('player.modal.encoderTitle', { encoder: caps.preferred })}><Cpu className="w-2.5 h-2.5" />{encoderLabel}</span>}
        {isTranscoded && !caps?.preferred && <span className="text-[10px] bg-purple-500/20 text-purple-700 dark:text-purple-300 border border-purple-500/30 px-1.5 py-0.5 rounded flex items-center gap-1 flex-shrink-0"><Cpu className="w-2.5 h-2.5" />GPU</span>}
      </h2>
      <div className="flex items-center gap-2 flex-shrink-0 ml-2">
        {info && <button onClick={onShowInfo} title={t('player.modal.torrentInfo')} className="text-text-secondary hover:text-text-primary transition-colors"><Info className="w-5 h-5" /></button>}
        {info && <button onClick={toggleFavorite} title={isFavorite ? t('player.modal.removeFavorite') : t('player.modal.addFavorite')} className={`transition-colors ${isFavorite ? 'text-pink-400 hover:text-pink-500 dark:hover:text-pink-300' : 'text-text-muted hover:text-pink-400'}`}><Heart className={`w-5 h-5 ${isFavorite ? 'fill-current' : ''}`} /></button>}
        <button onClick={() => setIncognito(!incognito)} title={incognito ? t('player.modal.incognitoActive') : t('player.modal.incognitoEnable')} className={`transition-colors ${incognito ? 'text-amber-400 hover:text-amber-500 dark:hover:text-amber-300' : 'text-text-secondary hover:text-text-primary'}`}>{incognito ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}</button>
        {fullViewport
          ? (
            // Deep-link tab dedicated to playback: the only navigation affordance
            // is a single Home button (no minimize/PiP — there's nothing behind).
            <button onClick={() => (onHome ?? onClose)()} title={t('player.modal.backHome')} className="flex items-center gap-1 text-sm text-text-secondary hover:text-text-primary transition-colors">
              <Home className="w-5 h-5" />
            </button>
          )
          : (
            <>
              <button onClick={() => setMinimized(m => !m)} title={minimized ? t('player.modal.expand') : t('player.modal.minimize')} className="text-text-secondary hover:text-text-primary transition-colors">{minimized ? <Maximize2 className="w-4 h-4" /> : <Minimize2 className="w-5 h-5" />}</button>
              <button onClick={onClose} className="text-text-secondary hover:text-text-primary transition-colors"><X className="w-5 h-5" /></button>
            </>
          )}
      </div>
    </div>
  )
}
