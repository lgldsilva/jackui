import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Pause, Play, Trash2, ArrowUpCircle, ChevronDown, ChevronRight, Folder, RotateCcw } from 'lucide-react'
import type { DownloadEntry } from '../../api/client'
import { formatBytesPair } from '../../lib/format'
import { viewGroupFiles, groupStatusCounts, type GroupFileStatusFilter, type GroupFileSortKey, type GroupFileSortDir } from '../../lib/groupFileView'
import { groupProgress, type CompletedGroup } from '../../lib/downloadGroups'
import DownloadGroupFilterBar from '../DownloadGroupFilterBar'

// CompletedGroupActions — the promote/stop-seed/remove-all controls for a
// completed (or seeding) multi-file group. Extracted so DownloadGroupCard stays
// presentation-only and each lifecycle section supplies the right action set.
export function CompletedGroupActions({ onPromote, onStopSeed, onDelete, busy }: {
  readonly onPromote: () => void
  readonly onStopSeed?: () => void
  readonly onDelete: () => void
  readonly busy: boolean
}) {
  const { t } = useTranslation()
  return (
    <>
      <button onClick={onPromote} disabled={busy} title={t('downloads.page.promoteAll')} className="p-1.5 rounded-lg text-cyan-400 hover:bg-cyan-500/10 disabled:opacity-50">
        <ArrowUpCircle className="w-4 h-4" />
      </button>
      {onStopSeed && (
        <button onClick={onStopSeed} disabled={busy} title={t('downloads.page.stopSeedAll')} className="p-1.5 rounded-lg text-text-secondary hover:bg-surface-tertiary disabled:opacity-50">
          <Pause className="w-4 h-4" />
        </button>
      )}
      <button onClick={onDelete} disabled={busy} title={t('downloads.page.removeTorrentFromList')} className="p-1.5 rounded-lg text-red-400 hover:bg-red-500/10 disabled:opacity-50">
        <Trash2 className="w-4 h-4" />
      </button>
    </>
  )
}

// ActiveGroupActions — torrent-level pause/resume/retry + remove-all for an
// in-progress multi-file group (Baixando/Fila/Pausados/Erro). Pause/resume act
// on the whole torrent by infoHash; onRetryFailed re-queues only failed files.
export function ActiveGroupActions({ paused, onPause, onResume, onRetryFailed, onDelete, busy }: {
  readonly paused: boolean
  readonly onPause: () => void
  readonly onResume: () => void
  readonly onRetryFailed?: () => void
  readonly onDelete: () => void
  readonly busy: boolean
}) {
  const { t } = useTranslation()
  return (
    <>
      {onRetryFailed && (
        <button onClick={onRetryFailed} disabled={busy} title={t('downloads.page.retryAllFailed')} className="p-1.5 rounded-lg text-amber-400 hover:bg-amber-500/10 disabled:opacity-50">
          <RotateCcw className="w-4 h-4" />
        </button>
      )}
      {paused ? (
        <button onClick={onResume} disabled={busy} title={t('downloads.page.resumeTorrent')} className="p-1.5 rounded-lg text-emerald-400 hover:bg-emerald-500/10 disabled:opacity-50">
          <Play className="w-4 h-4" />
        </button>
      ) : (
        <button onClick={onPause} disabled={busy} title={t('downloads.page.pauseTorrent')} className="p-1.5 rounded-lg text-text-secondary hover:bg-surface-tertiary disabled:opacity-50">
          <Pause className="w-4 h-4" />
        </button>
      )}
      <button onClick={onDelete} disabled={busy} title={t('downloads.page.removeTorrentFromList')} className="p-1.5 rounded-lg text-red-400 hover:bg-red-500/10 disabled:opacity-50">
        <Trash2 className="w-4 h-4" />
      </button>
    </>
  )
}

// DownloadGroupCard — collapsible header for a multi-file torrent (2+ files), with
// an aggregate progress bar and a slot for torrent-level actions. Single-file and
// whole-torrent groups render their lone card directly (no wrapper). Children are
// the per-file DownloadCards (shown when expanded).
export function DownloadGroupCard({
  group, expanded, onToggle, actions, renderFile,
}: {
  readonly group: CompletedGroup
  readonly expanded: boolean
  readonly onToggle: () => void
  readonly actions: React.ReactNode
  readonly renderFile: (d: DownloadEntry) => React.ReactNode
}) {
  const { t } = useTranslation()
  // Filtro/ordenação interno do torrent (só multi-arquivo chega aqui). Default
  // 'all' + nome asc preserva o comportamento anterior (ordem natural por nome).
  const [statusFilter, setStatusFilter] = useState<GroupFileStatusFilter>('all')
  const [sortKey, setSortKey] = useState<GroupFileSortKey>('name')
  const [sortDir, setSortDir] = useState<GroupFileSortDir>('asc')
  // Clicar na chave já ativa inverte a direção; trocar de chave reinicia em asc.
  const onSort = (key: GroupFileSortKey) => {
    if (key === sortKey) setSortDir(d => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortKey(key); setSortDir('asc') }
  }
  const counts = groupStatusCounts(group.files)
  const view = viewGroupFiles(group.files, statusFilter, sortKey, sortDir)

  const prog = groupProgress(group)
  const showBar = !group.files.every(f => f.status === 'completed')
  return (
    <div className="rounded-xl border border-default/50 bg-surface-secondary/40 overflow-hidden">
      <div className="flex items-center gap-2 p-3">
        <button onClick={onToggle} className="flex items-center gap-2 min-w-0 flex-1 text-left">
          {expanded ? <ChevronDown className="w-4 h-4 flex-shrink-0 text-text-secondary" /> : <ChevronRight className="w-4 h-4 flex-shrink-0 text-text-secondary" />}
          <Folder className="w-4 h-4 flex-shrink-0 text-emerald-400" />
          <span className="font-semibold text-text-primary text-sm truncate" title={group.name}>{group.name}</span>
          <span className="text-[11px] text-text-muted flex-shrink-0">{t('downloads.whole_torrent_files', { count: group.files.length })}</span>
          {group.seeding && (
            <span className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded-md border font-medium bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30 flex-shrink-0">
              <ArrowUpCircle className="w-3 h-3" />{t('downloads.page.seeding')}
            </span>
          )}
        </button>
        <div className="flex items-center gap-1.5 flex-shrink-0">{actions}</div>
      </div>
      {showBar && (
        <div className="px-3 pb-2 -mt-1">
          <div className="h-1.5 rounded-full bg-surface-tertiary/60 overflow-hidden">
            <div className="h-full rounded-full bg-gradient-to-r from-cyan-500 to-blue-500 transition-all" style={{ width: `${prog.pct}%` }} />
          </div>
          <div className="text-[10px] text-text-muted mt-1">
            {formatBytesPair(prog.downloaded, prog.total)} · {Math.round(prog.pct)}%
          </div>
        </div>
      )}
      {expanded && (
        <div className="px-3 pb-3 pl-6 border-l-2 border-default/50 ml-3">
          <DownloadGroupFilterBar
            counts={counts}
            statusFilter={statusFilter}
            onStatusFilter={setStatusFilter}
            sortKey={sortKey}
            sortDir={sortDir}
            onSort={onSort}
          />
          {view.length === 0 ? (
            <p className="text-xs text-text-muted text-center py-3">{t('downloads.page.noFilesFilter')}</p>
          ) : (
            <div className="flex flex-col gap-2">{view.map(renderFile)}</div>
          )}
        </div>
      )}
    </div>
  )
}
