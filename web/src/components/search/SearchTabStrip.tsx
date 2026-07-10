import type { RefObject, MutableRefObject, MouseEvent as ReactMouseEvent } from 'react'
import { X, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { TabState } from '../../lib/searchTabs'
import { PhaseIndicator } from './PhaseIndicator'

type Props = {
  readonly tabs: TabState[]
  readonly activeId: string
  readonly onSelect: (id: string) => void
  readonly stripRef: RefObject<HTMLDivElement>
  readonly activeTabRef: RefObject<HTMLButtonElement>
  readonly dragIndexRef: MutableRefObject<number | null>
  readonly dragOverIndex: number | null
  readonly setDragOverIndex: (i: number | null) => void
  readonly onMoveTab: (from: number, to: number) => void
  readonly onCloseTab: (id: string, e?: ReactMouseEvent) => void
  readonly onAddTab: () => void
}

// Faixa de abas de busca (arrastar-pra-reordenar + fechar + nova aba). Extraído
// do SearchPage (god-file) mantendo o mesmo comportamento de drag/drop e refs.
export function SearchTabStrip({
  tabs, activeId, onSelect, stripRef, activeTabRef, dragIndexRef,
  dragOverIndex, setDragOverIndex, onMoveTab, onCloseTab, onAddTab,
}: Props) {
  const { t } = useTranslation()
  return (
    <div className="bg-surface-secondary/60 border-b border-default px-4">
      <div ref={stripRef} className="max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto flex items-end gap-0.5 overflow-x-auto scroll-smooth snap-x safe-left">
        {tabs.map((tab, i) => (
          <button
            key={tab.id}
            ref={tab.id === activeId ? activeTabRef : undefined}
            onClick={() => onSelect(tab.id)}
            draggable
            onDragStart={(e) => { dragIndexRef.current = i; e.dataTransfer.effectAllowed = 'move' }}
            onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; if (dragOverIndex !== i) setDragOverIndex(i) }}
            onDrop={(e) => { e.preventDefault(); if (dragIndexRef.current !== null) { onMoveTab(dragIndexRef.current, i) } dragIndexRef.current = null; setDragOverIndex(null) }}
            onDragEnd={() => { dragIndexRef.current = null; setDragOverIndex(null) }}
            className={`group flex items-center gap-2 px-4 py-2.5 text-sm rounded-t-lg transition-colors min-w-0 max-w-[200px] border-t border-l border-r flex-shrink-0 snap-start cursor-grab active:cursor-grabbing ${
              tab.id === activeId
                ? 'bg-surface border-default text-text-primary'
                : 'border-transparent text-text-muted hover:text-text-primary hover:bg-surface-secondary'
            } ${dragOverIndex === i && dragIndexRef.current !== null && dragIndexRef.current !== i ? 'ring-2 ring-green-500/60' : ''}`}
          >
            <PhaseIndicator phase={tab.phase} />
            <span className="truncate flex-1 min-w-0 text-left">
              {tab.query.trim() || t('search.new_tab')}
            </span>
            {tabs.length > 1 && (
              <button
                type="button"
                onClick={e => onCloseTab(tab.id, e)}
                onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onCloseTab(tab.id) } }}
                className="opacity-60 sm:opacity-0 sm:group-hover:opacity-100 hover:text-red-400 transition-all flex-shrink-0 cursor-pointer p-0.5"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            )}
          </button>
        ))}
        <button
          onClick={onAddTab}
          className="flex items-center justify-center w-8 h-8 mb-0.5 text-text-muted hover:text-text-primary hover:bg-surface-tertiary rounded-lg transition-colors flex-shrink-0"
          title={t('search.new_tab_title')}
        >
          <Plus className="w-4 h-4" />
        </button>
      </div>
    </div>
  )
}
