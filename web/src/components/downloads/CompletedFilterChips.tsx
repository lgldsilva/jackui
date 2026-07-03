export type CompletedFilterKey = 'all' | 'seeding' | 'ondisk'

// Filtro da aba de concluídos: ver tudo, só o que está semeando ao vivo, ou só
// o que está parado no disco. Top-level para evitar componente-no-pai (S6478).
export function CompletedFilterChips({ value, onChange, seedingN, onDiskN }: {
  readonly value: CompletedFilterKey
  readonly onChange: (v: CompletedFilterKey) => void
  readonly seedingN: number
  readonly onDiskN: number
}) {
  const opts: { key: CompletedFilterKey; label: string }[] = [
    { key: 'all', label: 'Todos' },
    { key: 'seeding', label: `Semeando (${seedingN})` },
    { key: 'ondisk', label: `No disco (${onDiskN})` },
  ]
  return (
    <div className="flex items-center gap-1.5 flex-wrap">
      {opts.map(o => (
        <button
          key={o.key}
          onClick={() => onChange(o.key)}
          className={`text-xs px-3 py-1.5 rounded-lg border transition-colors ${value === o.key
            ? 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/40'
            : 'bg-surface-secondary text-text-secondary border-default hover:text-text-primary'}`}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}
