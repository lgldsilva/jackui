import { useEffect, useRef, useState } from 'react'
import { useTranslation, Trans } from 'react-i18next'
import { Check, ChevronDown, Filter, Search as SearchIcon, Plus } from 'lucide-react'
import { Indexer } from '../api/client'


type Props = {
  readonly selected: string[]
  readonly onChange: (ids: string[]) => void
  readonly indexers: Indexer[]
}

/**
 * Multi-select for indexers. Replaces the old single-pick `<select>` so the user
 * can scope a search to e.g. just "1337x + RARBG + ThePirateBay" without losing
 * the others entirely.
 *
 * Persists choices in localStorage so the next search remembers what was set
 * — most users keep the same set for weeks. Behaviour matches the existing
 * contract: `[]` means "all indexers".
 */
export default function IndexerMultiSelect({ selected, onChange, indexers }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handlePointerDown = (e: PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('pointerdown', handlePointerDown)
    return () => document.removeEventListener('pointerdown', handlePointerDown)
  }, [open])

  const toggle = (id: string) => {
    const next = selected.includes(id)
      ? selected.filter(x => x !== id)
      : [...selected, id]
    onChange(next)
  }
  const selectAll = () => onChange([])
  const clear = () => onChange([])

  const filtered = query.trim()
    ? indexers.filter(i => i.name.toLowerCase().includes(query.toLowerCase()))
    : indexers

  let label: string
  if (selected.length === 0) {
    label = t('search.all_count', { count: indexers.length || 0 })
  } else if (selected.length === 1) {
    label = indexers.find(i => i.id === selected[0])?.name || selected[0]
  } else {
    label = t('search.n_indexers', { count: selected.length })
  }

  let dropdownContent: React.ReactNode
  if (filtered.length === 0) {
    let emptyContent: React.ReactNode
    if (query) {
      emptyContent = (
        <>
          <p>{t('search.no_indexer_match', { query })}</p>
          <button
            type="button"
            onClick={() => {
              const newId = query.trim().toLowerCase().replaceAll(/[^a-z0-9]+/g, '-')
              if (newId) {
                toggle(newId)
                setQuery('')
              }
            }}
            className="mx-auto flex items-center gap-1 bg-green-500/25 hover:bg-green-500/35 text-green-700 dark:text-green-300 border border-green-500/40 px-3 py-1.5 rounded-lg transition-colors font-medium cursor-pointer"
          >
            <Plus className="w-3.5 h-3.5" /> {t('search.add_indexer', { name: query.trim() })}
          </button>
        </>
      )
    } else if (indexers.length === 0) {
      emptyContent = (
        <>
          <p className="text-text-secondary font-medium">{t('search.jackett_no_list')}</p>
          <p className="text-[11px] leading-relaxed text-text-muted">
            <Trans i18nKey="search.indexer_help" components={{ g: <span className="text-green-400" /> }} />
          </p>
        </>
      )
    } else {
      emptyContent = (
        <p>{t('search.no_indexer_configured')}</p>
      )
    }
    dropdownContent = (
      <div className="px-3 py-4 text-xs text-text-muted text-center space-y-3">
        {emptyContent}
      </div>
    )
  } else {
    dropdownContent = filtered.map(idx => {
      const checked = selected.includes(idx.id)
      return (
        <button
          key={idx.id}
          onClick={() => toggle(idx.id)}
          className="w-full flex items-center gap-2 px-3 py-1.5 text-sm text-text-primary hover:bg-surface-tertiary text-left"
        >
          <span className={`w-4 h-4 rounded border flex items-center justify-center flex-shrink-0 ${checked ? 'bg-green-500 border-green-400' : 'border-strong'}`}>
            {checked && <Check className="w-3 h-3 text-white" />}
          </span>
          <span className="truncate">{idx.name}</span>
          {idx.language && (
            <span className="text-[10px] text-text-muted ml-auto flex-shrink-0">{idx.language}</span>
          )}
        </button>
      )
    })
  }

  return (
    <div ref={containerRef} className="relative w-full">
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        className="input-field w-full flex items-center justify-between gap-2 text-left"
      >
        <span className="flex items-center gap-2 min-w-0">
          <Filter className="w-4 h-4 text-text-secondary flex-shrink-0" />
          <span className="truncate">{label}</span>
        </span>
        <ChevronDown className={`w-4 h-4 text-text-secondary flex-shrink-0 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {open && (
        <div className="absolute left-0 right-0 mt-1 bg-surface-secondary border border-default rounded-lg shadow-xl z-50 max-h-80 flex flex-col">
          <div className="p-2 border-b border-default flex gap-2 items-center">
            <SearchIcon className="w-4 h-4 text-text-muted flex-shrink-0" />
            <input
              type="text"
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder={t('search.filter_ellipsis')}
              className="bg-transparent text-sm text-text-primary placeholder-gray-500 flex-1 focus:outline-none"
              autoFocus
            />
            <button onClick={selectAll} className="text-[11px] text-green-400 hover:text-green-500 dark:hover:text-green-300 whitespace-nowrap">{t('search.all')}</button>
            <span className="text-text-muted">·</span>
            <button onClick={clear} className="text-[11px] text-text-secondary hover:text-text-primary whitespace-nowrap">{t('search.clean')}</button>
          </div>

          <div className="overflow-y-auto flex-1">
            {dropdownContent}
          </div>
          <div className="p-2 border-t border-default flex justify-between items-center text-[11px] text-text-muted">
            <span>{selected.length === 0 ? t('search.searching_all') : t('search.n_selected', { count: selected.length })}</span>
            <button onClick={() => setOpen(false)} className="text-text-primary hover:text-text-primary">{t('search.close')}</button>
          </div>
        </div>
      )}
    </div>
  )
}
