import { useTranslation } from 'react-i18next'
import { ArrowRight, AlertCircle, CheckCircle2, XCircle, Loader2, Sparkles } from 'lucide-react'
import type { ReclassifyRow } from './rows'
import { categoryOptions, rowTargetPath } from './rows'
import type { PromoteItemResult } from '../../api/client'

type Props = {
  readonly rows: readonly ReclassifyRow[]
  readonly destFolders: readonly string[]
  readonly results: ReadonlyMap<string, PromoteItemResult>
  readonly busy: boolean
  readonly onToggle: (path: string, selected: boolean) => void
  readonly onToggleAll: (selected: boolean) => void
  readonly onEdit: (path: string, field: 'category' | 'finalName', value: string) => void
}

// CATEGORY_DATALIST is shared by every row's <input list=>; a datalist gives a
// free-text field with autocomplete (pick an existing folder OR type a new one)
// — friendlier than a hard <select> and keeps the "create a new category"
// affordance the single-item flow had.
const CATEGORY_DATALIST = 'reclassify-categories'

export default function ReclassifyTable({
  rows, destFolders, results, busy, onToggle, onToggleAll, onEdit,
}: Props) {
  const { t } = useTranslation()
  const options = categoryOptions(destFolders, rows)
  const selectable = rows.filter(r => !r.error)
  const allSelected = selectable.length > 0 && selectable.every(r => r.selected)

  return (
    <div className="overflow-x-auto -mx-4 px-4">
      <datalist id={CATEGORY_DATALIST}>
        {options.map(o => <option key={o} value={o} />)}
      </datalist>
      <table className="w-full text-xs border-collapse">
        <caption className="sr-only">{t('reclassify.table_caption')}</caption>
        <thead>
          <tr className="text-left text-text-muted border-b border-default">
            <th scope="col" className="py-2 pr-2 w-8">
              <input
                type="checkbox"
                aria-label={t('reclassify.select_all')}
                checked={allSelected}
                disabled={busy || selectable.length === 0}
                onChange={e => onToggleAll(e.target.checked)}
                className="accent-cyan-500"
              />
            </th>
            <th scope="col" className="py-2 pr-2">{t('reclassify.col_source')}</th>
            <th scope="col" className="py-2 pr-2">{t('reclassify.col_category')}</th>
            <th scope="col" className="py-2 pr-2">{t('reclassify.col_name')}</th>
            <th scope="col" className="py-2 w-6" />
          </tr>
        </thead>
        <tbody className="divide-y divide-default">
          {rows.map(row => (
            <Row
              key={row.path}
              row={row}
              result={results.get(row.path)}
              busy={busy}
              listId={CATEGORY_DATALIST}
              onToggle={onToggle}
              onEdit={onEdit}
            />
          ))}
        </tbody>
      </table>
    </div>
  )
}

function StatusIcon({ result, busy, selected }: Readonly<{ result?: PromoteItemResult; busy: boolean; selected: boolean }>) {
  if (result?.ok) return <CheckCircle2 className="w-4 h-4 text-green-400" aria-label="ok" />
  if (result && !result.ok) return <XCircle className="w-4 h-4 text-red-400" aria-label={result.error || 'erro'} />
  if (busy && selected) return <Loader2 className="w-4 h-4 animate-spin text-cyan-400" aria-label="..." />
  return null
}

function Row({
  row, result, busy, listId, onToggle, onEdit,
}: {
  readonly row: ReclassifyRow
  readonly result?: PromoteItemResult
  readonly busy: boolean
  readonly listId: string
  readonly onToggle: (path: string, selected: boolean) => void
  readonly onEdit: (path: string, field: 'category' | 'finalName', value: string) => void
}) {
  const { t } = useTranslation()
  const disabled = busy || !!row.error
  const preview = rowTargetPath(row)
  return (
    <tr className={row.error ? 'opacity-60' : ''}>
      <td className="py-2 pr-2 align-top">
        <input
          type="checkbox"
          aria-label={t('reclassify.select_row', { name: row.originalName })}
          checked={row.selected}
          disabled={disabled}
          onChange={e => onToggle(row.path, e.target.checked)}
          className="accent-cyan-500 mt-1.5"
        />
      </td>
      <td className="py-2 pr-2 align-top max-w-[10rem]">
        <p className="font-mono text-[10px] text-text-secondary break-all leading-tight" title={row.originalName}>
          {row.originalName}
        </p>
        <span className="inline-flex items-center gap-1 mt-1 px-1.5 text-[9px] font-bold rounded bg-cyan-500/15 dark:bg-cyan-900/40 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30">
          {row.kind === 'tv' ? t('reclassify.kind_tv') : t('reclassify.kind_movie')}
        </span>
        {row.reusedFolder && (
          <span
            title={t('reclassify.reused_title', { folder: row.reusedFolder })}
            className="inline-flex items-center gap-0.5 ml-1 mt-1 px-1.5 text-[9px] font-bold rounded bg-emerald-500/15 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 border border-emerald-500/30"
          >
            <Sparkles className="w-2.5 h-2.5" /> {row.reusedFolder}
          </span>
        )}
      </td>
      <td className="py-2 pr-2 align-top">
        <input
          type="text"
          list={listId}
          aria-label={t('reclassify.col_category')}
          value={row.category}
          disabled={disabled}
          onChange={e => onEdit(row.path, 'category', e.target.value)}
          placeholder={t('reclassify.category_placeholder')}
          className="w-full min-w-[7rem] bg-surface-tertiary border border-strong rounded px-2 py-1 text-xs focus:outline-none focus:border-cyan-500 text-text-primary disabled:opacity-50"
        />
      </td>
      <td className="py-2 pr-2 align-top">
        <input
          type="text"
          aria-label={t('reclassify.col_name')}
          value={row.finalName}
          disabled={disabled}
          onChange={e => onEdit(row.path, 'finalName', e.target.value)}
          className="w-full min-w-[10rem] bg-surface-tertiary border border-strong rounded px-2 py-1 text-xs focus:outline-none focus:border-cyan-500 text-text-primary disabled:opacity-50"
        />
        {row.error ? (
          <p className="mt-1 text-[10px] text-red-700 dark:text-red-400 flex items-start gap-1">
            <AlertCircle className="w-3 h-3 mt-0.5 flex-shrink-0" />{row.error}
          </p>
        ) : (
          <p className="mt-1 text-[10px] text-text-muted font-mono break-all flex items-start gap-1" title={preview}>
            <ArrowRight className="w-3 h-3 mt-0.5 flex-shrink-0 text-emerald-400" />{preview}
          </p>
        )}
        {result && !result.ok && result.error && (
          <p className="mt-1 text-[10px] text-red-700 dark:text-red-400 flex items-start gap-1">
            <XCircle className="w-3 h-3 mt-0.5 flex-shrink-0" />{result.error}
          </p>
        )}
      </td>
      <td className="py-2 align-top">
        <div className="mt-1.5">
          <StatusIcon result={result} busy={busy} selected={row.selected} />
        </div>
      </td>
    </tr>
  )
}
