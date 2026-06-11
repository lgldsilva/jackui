import { ArrowDownWideNarrow, ArrowUpWideNarrow, ChevronsUpDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { TableSort } from '../lib/useTableSort'

// Sortable <th>: the inner <button> makes the header keyboard-navigable and
// aria-sort tells screen readers which column drives the current order.
export default function SortableTh<K extends string>({
  label,
  columnKey,
  sort,
  align = 'left',
  className = 'py-1 pr-3',
}: {
  label: string
  columnKey: K
  sort: TableSort<K>
  align?: 'left' | 'right'
  className?: string
}) {
  const { t } = useTranslation()
  const active = sort.sortKey === columnKey
  const Icon = active
    ? (sort.dir === 'asc' ? ArrowUpWideNarrow : ArrowDownWideNarrow)
    : ChevronsUpDown
  return (
    <th
      scope="col"
      aria-sort={sort.ariaSort(columnKey)}
      className={`${className} font-medium ${align === 'right' ? 'text-right' : 'text-left'}`}
    >
      <button
        type="button"
        onClick={() => sort.toggle(columnKey)}
        title={t('table.sort_by', { column: label })}
        className="inline-flex items-center gap-1 hover:text-text-primary transition-colors select-none"
      >
        {label}
        <Icon className={`w-3.5 h-3.5 shrink-0 ${active ? '' : 'opacity-40'}`} />
      </button>
    </th>
  )
}
