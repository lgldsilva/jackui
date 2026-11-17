import { ListMusic, Shuffle, Repeat, ChevronLeft, ChevronRight } from 'lucide-react'
import type { TFn, PlaylistMeta, PlaylistBarControls } from './playerTypes'

export function renderPlaylistBar(playlist: PlaylistMeta, controls: PlaylistBarControls, t: TFn) {
  const { onPrev, onToggleShuffle, shuffle, onCycleRepeat, repeat, onNext } = controls
  return (
    <div className="flex items-center justify-between gap-2 px-4 py-2 bg-blue-500/10 border-b border-blue-500/30 text-xs text-blue-700 dark:text-blue-200 flex-shrink-0">
      <div className="flex items-center gap-2 min-w-0">
        <ListMusic className="w-3.5 h-3.5 flex-shrink-0" />
        <span className="font-medium truncate">{playlist.name}</span>
        <span className="text-blue-400/80 flex-shrink-0">· {t('player.modal.ofCount', { current: playlist.currentIndex + 1, total: playlist.items.length })}</span>
      </div>
      <div className="flex items-center gap-1 flex-shrink-0">
        <button onClick={onPrev} className="flex items-center justify-center min-w-[36px] min-h-[36px] sm:min-w-0 sm:min-h-0 p-2 sm:p-1 rounded hover:bg-blue-500/20 text-blue-700 dark:text-blue-200 hover:text-blue-900 dark:hover:text-white" title={t('player.modal.prevItem')}><ChevronLeft className="w-4 h-4" /></button>
        <button onClick={onToggleShuffle} className={`flex items-center justify-center min-w-[36px] min-h-[36px] sm:min-w-0 sm:min-h-0 p-2 sm:p-1 rounded hover:bg-blue-500/20 ${shuffle ? 'text-green-700 dark:text-green-300' : 'text-blue-700/60 dark:text-blue-200/60'} hover:text-blue-900 dark:hover:text-white`} title={shuffle ? t('player.controls.shuffleOn') : t('player.controls.shuffleOff')}><Shuffle className="w-3.5 h-3.5" /></button>
        <button onClick={onCycleRepeat} className={`flex items-center justify-center min-w-[36px] min-h-[36px] sm:min-w-0 sm:min-h-0 p-2 sm:p-1 rounded hover:bg-blue-500/20 ${repeat === 'none' ? 'text-blue-700/60 dark:text-blue-200/60' : 'text-green-700 dark:text-green-300'} hover:text-blue-900 dark:hover:text-white relative`} title={t('player.controls.repeatMode', { mode: repeat })}>
          <Repeat className="w-3.5 h-3.5" />
          {repeat === 'one' && <span className="absolute bottom-0.5 right-0.5 text-[8px] font-bold text-green-700 dark:text-green-300">1</span>}
        </button>
        <button onClick={onNext} className="flex items-center justify-center min-w-[36px] min-h-[36px] sm:min-w-0 sm:min-h-0 p-2 sm:p-1 rounded hover:bg-blue-500/20 text-blue-700 dark:text-blue-200 hover:text-blue-900 dark:hover:text-white" title={t('player.modal.nextItem')}><ChevronRight className="w-4 h-4" /></button>
      </div>
    </div>
  )
}
