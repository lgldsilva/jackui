import { useTranslation } from 'react-i18next'
import { ChevronDown } from 'lucide-react'
import { LocalEntry } from '../../api/client'
import { EntryRow } from './EntryRow'
import { type IncrementalReveal } from '../player/useIncrementalReveal'

type Props = {
  readonly visible: LocalEntry[]
  readonly reveal: IncrementalReveal
  readonly activeMount: string
  readonly selectMode: boolean
  readonly selected: Set<string>
  readonly canManipulate: boolean
  readonly isAdmin: boolean
  readonly hiddenSet: Set<string>
  readonly onOpen: (e: LocalEntry) => void
  readonly onEnterSelect: (e: LocalEntry) => void
  readonly onToggleSelect: (e: LocalEntry) => void
  readonly onRename: (e: LocalEntry) => void
  readonly onPromote: (e: LocalEntry) => void
  readonly onReclassify: (e: LocalEntry) => void
  readonly onMove: (e: LocalEntry) => void
  readonly onLock: (e: LocalEntry) => void
  readonly onDelete: (e: LocalEntry) => void
  readonly onToggleHidden: (e: LocalEntry) => void
}

export function LocalEntryList({
  visible, reveal, activeMount, selectMode, selected, canManipulate, isAdmin, hiddenSet,
  onOpen, onEnterSelect, onToggleSelect, onRename, onPromote, onReclassify, onMove, onLock, onDelete, onToggleHidden,
}: Props) {
  const { t } = useTranslation()
  return (
    <ul className={`flex-1 min-h-0 overflow-y-auto divide-y divide-default bg-surface-secondary/50 rounded-xl border border-default ${selectMode ? 'pb-20' : ''}`}>
      {visible.slice(0, reveal.visible).map((e) => (
        <EntryRow
          key={e.path}
          entry={e}
          mount={activeMount}
          selectMode={selectMode}
          selected={selected.has(e.path)}
          canManipulate={canManipulate}
          isAdmin={isAdmin}
          onOpen={onOpen}
          onEnterSelect={onEnterSelect}
          onToggleSelect={onToggleSelect}
          onRename={onRename}
          onPromote={onPromote}
          onReclassify={onReclassify}
          onMove={onMove}
          onLock={onLock}
          onDelete={onDelete}
          hidden={hiddenSet.has(e.path)}
          onToggleHidden={onToggleHidden}
        />
      ))}
      {/* Sentinela do windowing: revela mais um lote ao rolar até aqui;
          o botão é o fallback (clique) — mesmo padrão de Search/Favorites. */}
      {reveal.hasMore && (
        <li className="px-2 pt-1 pb-2">
          <div ref={reveal.sentinelRef}>
            <button
              onClick={reveal.showMore}
              className="w-full flex items-center justify-center gap-1.5 rounded-lg bg-surface-tertiary/60 py-2 text-xs text-text-secondary hover:text-text-primary transition-colors"
            >
              <ChevronDown className="w-3.5 h-3.5" />
              {t('player.files.showMore', { count: reveal.remaining, total: visible.length })}
            </button>
          </div>
        </li>
      )}
    </ul>
  )
}
