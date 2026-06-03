import { CheckCheck, Square } from 'lucide-react'

// SelectAllButton — controle padronizado para listas com multi-seleção.
// Toggle: quando nem tudo está marcado → "Selecionar todos"; quando tudo está
// marcado → "Limpar". Usado no Downloads e na BatchActionBar (Local) para um
// affordance consistente (ícone + rótulo, não só ícone).
export function SelectAllButton({
  allSelected, onToggle, className,
}: {
  readonly allSelected: boolean
  readonly onToggle: () => void
  readonly className?: string
}) {
  return (
    <button
      onClick={onToggle}
      title={allSelected ? 'Limpar seleção' : 'Selecionar todos'}
      className={className ?? 'flex items-center gap-1.5 text-xs text-gray-300 hover:text-gray-100 px-2.5 py-1 rounded-full hover:bg-gray-700 transition-colors whitespace-nowrap'}
    >
      {allSelected ? <Square className="w-3.5 h-3.5" /> : <CheckCheck className="w-3.5 h-3.5" />}
      {allSelected ? 'Limpar' : 'Selecionar todos'}
    </button>
  )
}
