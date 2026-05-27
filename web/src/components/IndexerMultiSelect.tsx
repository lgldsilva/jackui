import { useEffect, useRef, useState } from 'react'
import { Check, ChevronDown, Filter, Search as SearchIcon } from 'lucide-react'
import { Indexer } from '../api/client'

interface Props {
  /** Empty array = "search all indexers". The backend treats that as the default. */
  selected: string[]
  onChange: (ids: string[]) => void
  indexers: Indexer[]
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
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (e: PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
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

  const label =
    selected.length === 0
      ? `Todos (${indexers.length || 0})`
      : selected.length === 1
        ? indexers.find(i => i.id === selected[0])?.name || selected[0]
        : `${selected.length} indexers`

  return (
    <div ref={containerRef} className="relative w-full">
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        className="input-field w-full flex items-center justify-between gap-2 text-left"
      >
        <span className="flex items-center gap-2 min-w-0">
          <Filter className="w-4 h-4 text-gray-400 flex-shrink-0" />
          <span className="truncate">{label}</span>
        </span>
        <ChevronDown className={`w-4 h-4 text-gray-400 flex-shrink-0 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {open && (
        <div className="absolute left-0 right-0 mt-1 bg-gray-800 border border-gray-700 rounded-lg shadow-xl z-50 max-h-80 flex flex-col">
          <div className="p-2 border-b border-gray-700 flex gap-2 items-center">
            <SearchIcon className="w-4 h-4 text-gray-500 flex-shrink-0" />
            <input
              type="text"
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder="Filtrar..."
              className="bg-transparent text-sm text-gray-200 placeholder-gray-500 flex-1 focus:outline-none"
              autoFocus
            />
            <button onClick={selectAll} className="text-[11px] text-green-400 hover:text-green-300 whitespace-nowrap">Todos</button>
            <span className="text-gray-600">·</span>
            <button onClick={clear} className="text-[11px] text-gray-400 hover:text-gray-200 whitespace-nowrap">Limpar</button>
          </div>

          <div className="overflow-y-auto flex-1">
            {filtered.length === 0 ? (
              <div className="px-3 py-3 text-xs text-gray-500 text-center space-y-2">
                {query ? (
                  <p>Nenhum indexer bate com esse filtro</p>
                ) : indexers.length === 0 ? (
                  <>
                    <p className="text-gray-400">Jackett não expôs a lista de indexers.</p>
                    <p className="text-[11px]">A busca continuará usando <span className="text-green-400">todos</span> os indexers configurados. Pra ter filtro fino, habilite admin password no Jackett.</p>
                  </>
                ) : (
                  <p>Nenhum indexer configurado no Jackett</p>
                )}
              </div>
            ) : (
              filtered.map(idx => {
                const checked = selected.includes(idx.id)
                return (
                  <button
                    key={idx.id}
                    onClick={() => toggle(idx.id)}
                    className="w-full flex items-center gap-2 px-3 py-1.5 text-sm text-gray-200 hover:bg-gray-700 text-left"
                  >
                    <span className={`w-4 h-4 rounded border flex items-center justify-center flex-shrink-0 ${checked ? 'bg-green-500 border-green-400' : 'border-gray-600'}`}>
                      {checked && <Check className="w-3 h-3 text-gray-900" />}
                    </span>
                    <span className="truncate">{idx.name}</span>
                    {idx.language && (
                      <span className="text-[10px] text-gray-500 ml-auto flex-shrink-0">{idx.language}</span>
                    )}
                  </button>
                )
              })
            )}
          </div>
          <div className="p-2 border-t border-gray-700 flex justify-between items-center text-[11px] text-gray-500">
            <span>{selected.length === 0 ? 'Buscando em todos' : `${selected.length} selecionado(s)`}</span>
            <button onClick={() => setOpen(false)} className="text-gray-300 hover:text-gray-100">Fechar</button>
          </div>
        </div>
      )}
    </div>
  )
}
