import { useState } from 'react'
import { Pin, X, Clock, Search } from 'lucide-react'
import { load, save } from '../lib/storage'

const PINNED_KEY = 'pinnedSearches'

type Props = {
  readonly recent: readonly string[]
  readonly onPick: (q: string) => void
}

// SavedSearches shows pinned queries (localStorage) and recent ones (from the
// server-side per-user history) as clickable chips on the empty search screen.
// Pinning moves a query into the persisted list, which is rendered first.
export default function SavedSearches({ recent, onPick }: Props) {
  const [pinned, setPinned] = useState<string[]>(() => load<string[]>(PINNED_KEY, []))

  const togglePin = (q: string) => {
    setPinned(prev => {
      const next = prev.includes(q) ? prev.filter(p => p !== q) : [q, ...prev].slice(0, 12)
      save(PINNED_KEY, next)
      return next
    })
  }

  // Drop pinned entries from the recents so a query never shows in both groups.
  const pinnedSet = new Set(pinned)
  const recents = recent.filter(q => !pinnedSet.has(q)).slice(0, 12)

  if (pinned.length === 0 && recents.length === 0) return null

  return (
    <div className="w-full max-w-2xl mx-auto flex flex-col gap-4 mt-8">
      {pinned.length > 0 && (
        <ChipGroup label="Fixadas" icon={<Pin className="w-3.5 h-3.5" />}>
          {pinned.map(q => (
            <Chip key={q} query={q} pinned onPick={onPick} onTogglePin={togglePin} />
          ))}
        </ChipGroup>
      )}
      {recents.length > 0 && (
        <ChipGroup label="Recentes" icon={<Clock className="w-3.5 h-3.5" />}>
          {recents.map(q => (
            <Chip key={q} query={q} pinned={false} onPick={onPick} onTogglePin={togglePin} />
          ))}
        </ChipGroup>
      )}
    </div>
  )
}

function ChipGroup({ label, icon, children }: { readonly label: string; readonly icon: React.ReactNode; readonly children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <span className="text-xs uppercase tracking-wide text-text-muted flex items-center gap-1.5">{icon} {label}</span>
      <div className="flex flex-wrap gap-2">{children}</div>
    </div>
  )
}

function Chip({ query, pinned, onPick, onTogglePin }: {
  readonly query: string
  readonly pinned: boolean
  readonly onPick: (q: string) => void
  readonly onTogglePin: (q: string) => void
}) {
  return (
    <span className="group flex items-center gap-1.5 bg-surface-tertiary border border-strong rounded-full pl-3 pr-1.5 py-1 text-sm text-text-secondary hover:border-green-500/50 transition-colors">
      <button onClick={() => onPick(query)} className="flex items-center gap-1.5 hover:text-text-primary transition-colors max-w-[14rem] truncate">
        <Search className="w-3 h-3 opacity-60 flex-shrink-0" />
        <span className="truncate">{query}</span>
      </button>
      <button
        onClick={() => onTogglePin(query)}
        title={pinned ? 'Desafixar busca' : 'Fixar busca'}
        className={`p-0.5 rounded-full transition-colors flex-shrink-0 ${pinned ? 'text-green-400 hover:text-green-500 dark:hover:text-green-300' : 'text-text-muted hover:text-text-primary opacity-0 group-hover:opacity-100'}`}
      >
        {pinned ? <X className="w-3.5 h-3.5" /> : <Pin className="w-3.5 h-3.5" />}
      </button>
    </span>
  )
}
