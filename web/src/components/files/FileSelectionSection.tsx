import { useMemo, useState } from 'react'
import { List, FolderTree } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { StreamFile } from '../../api/client'
import { hasSubdirs } from '../../lib/fileTree'
import { usePersistedState } from '../../lib/storage'
import { formatBytes } from '../../lib/format'
import { fileIcon } from './fileIcon'
import { SelectableFileTree } from './SelectableFileTree'

type FileSelectionSectionProps = {
  readonly files: readonly StreamFile[]
  readonly selected: Set<number>
  readonly onChange: (next: Set<number>) => void
  readonly label?: string
  readonly className?: string
}

type FileView = 'list' | 'tree'

// FileSelectionSection — the file-picker block shared by DownloadModal and
// AddTorrentModal. Wraps: a header (count + Todos/Nenhum), a List⇄Tree toggle
// (tree is the default once the torrent has folders; a flat torrent never shows
// the toggle), an optional text filter, and the body — the flat list or the
// recursive-selection SelectableFileTree. Selection stays a Set<number> of
// file.index so the modals' submit path (isWholeTorrentSelection/buildBatchFiles)
// is untouched. Extracted to keep the god-file modals thin.
export function FileSelectionSection({ files, selected, onChange, label, className }: FileSelectionSectionProps) {
  const { t } = useTranslation()
  const treeable = useMemo(() => hasSubdirs(files), [files])
  const [view, setView] = usePersistedState<FileView>('downloads.fileView', 'tree')
  const [filter, setFilter] = useState('')
  const effectiveView: FileView = treeable ? view : 'list'

  const selectAll = () => onChange(new Set(files.map(f => f.index)))
  const selectNone = () => onChange(new Set())

  const filterLower = filter.trim().toLowerCase()
  const flatFiles = useMemo(
    () => (filterLower ? files.filter(f => f.path.toLowerCase().includes(filterLower)) : files),
    [files, filterLower],
  )

  const toggleFlat = (index: number) => {
    const next = new Set(selected)
    if (next.has(index)) next.delete(index); else next.add(index)
    onChange(next)
  }

  const viewBtnClass = (active: boolean) =>
    `flex items-center gap-1 px-2 py-1 rounded text-[11px] border transition-colors ${
      active
        ? 'bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/40'
        : 'bg-surface text-text-secondary border-default hover:bg-surface-tertiary/60'
    }`

  return (
    <div className={className}>
      <div className="flex items-center justify-between mb-1.5 gap-2">
        <label className="block text-sm font-medium text-text-primary min-w-0 truncate">
          {label ?? t('files.to_download')}
          <span className="text-xs text-text-muted font-normal ml-2">
            {t('files.selected', { n: selected.size, total: files.length })}
          </span>
        </label>
        <div className="flex items-center gap-2 flex-shrink-0">
          {treeable && (
            <div className="flex items-center gap-1">
              <button type="button" onClick={() => setView('list')} title={t('player.view_list')} aria-label={t('player.view_list')} aria-pressed={effectiveView === 'list'} className={viewBtnClass(effectiveView === 'list')}>
                <List className="w-3.5 h-3.5" />
              </button>
              <button type="button" onClick={() => setView('tree')} title={t('player.view_tree')} aria-label={t('player.view_tree')} aria-pressed={effectiveView === 'tree'} className={viewBtnClass(effectiveView === 'tree')}>
                <FolderTree className="w-3.5 h-3.5" />
              </button>
            </div>
          )}
          {files.length > 1 && (
            <div className="flex gap-2 text-xs">
              <button type="button" onClick={selectAll} className="text-cyan-400 hover:text-cyan-500 dark:hover:text-cyan-300">{t('files.all')}</button>
              <span className="text-text-muted">·</span>
              <button type="button" onClick={selectNone} className="text-text-secondary hover:text-text-primary">{t('files.none')}</button>
            </div>
          )}
        </div>
      </div>

      {files.length > 6 && (
        <input
          type="text"
          value={filter}
          onChange={e => setFilter(e.target.value)}
          placeholder={t('files.filter_placeholder')}
          className="input-field mb-1.5 py-1.5 text-sm"
        />
      )}

      {effectiveView === 'tree' ? (
        <SelectableFileTree files={files} selected={selected} onChange={onChange} filter={filter} />
      ) : (
        <ul className="bg-surface border border-default rounded-lg max-h-72 overflow-y-auto divide-y divide-default">
          {flatFiles.length === 0 && (
            <li className="px-3 py-3 text-xs text-text-muted text-center">{t('files.no_match')}</li>
          )}
          {flatFiles.map(f => (
            <li key={f.index} className="px-3 py-2 hover:bg-surface-secondary/40">
              <label className="flex items-center gap-2.5 cursor-pointer">
                <input type="checkbox" checked={selected.has(f.index)} onChange={() => toggleFlat(f.index)} className="accent-cyan-500 flex-shrink-0" />
                {fileIcon(f)}
                <span className="flex-1 min-w-0 text-sm text-text-primary truncate" title={f.path}>{f.path}</span>
                <span className="text-xs text-text-muted flex-shrink-0">{formatBytes(f.size)}</span>
              </label>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
