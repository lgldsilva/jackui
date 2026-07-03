import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CopyCheck, Loader2, Trash2, AlertCircle } from 'lucide-react'
import { Sheet } from '../Sheet'
import { localDuplicates, localDeleteDuplicates, DuplicateGroup } from '../../api/client'
import { formatSize } from '../player/playerFormat'

type DuplicatesModalProps = {
  readonly mount: string
  readonly path: string
  readonly onClose: () => void
  readonly onDeleted: (deleted: number) => void
}

// DuplicatesModal lists groups of byte-identical files (different names, same
// content) under the current folder and lets the user pick which copies to
// delete. Manual selection by design — nothing is pre-checked — with a helper
// that marks every copy except the first in each group ("keep one").
export function DuplicatesModal({ mount, path, onClose, onDeleted }: DuplicatesModalProps) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(true)
  const [groups, setGroups] = useState<DuplicateGroup[]>([])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState(false)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    localDuplicates(mount, path)
      .then(g => { if (!cancelled) setGroups(g) })
      .catch(e => { if (!cancelled) setError(e?.response?.data?.error || e?.message || t('local.duplicates.fetchError')) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [mount, path])

  const toggle = (p: string) => setSelected(prev => {
    const next = new Set(prev)
    if (next.has(p)) next.delete(p)
    else next.add(p)
    return next
  })

  // Mark every copy except the first in each group — the common "keep one" case.
  const selectExtras = () => {
    const next = new Set<string>()
    for (const g of groups) for (const f of g.files.slice(1)) next.add(f.path)
    setSelected(next)
  }

  const reclaimable = groups.reduce((sum, g) =>
    sum + g.files.filter(f => selected.has(f.path)).reduce((s, f) => s + f.size, 0), 0)

  // Footer summary — built without a nested ternary (Sonar S3358).
  const selectionSummary = selected.size > 0
    ? t('local.duplicates.selectionSummary', { count: selected.size, size: formatSize(reclaimable) })
    : t('local.duplicates.noneSelected')

  const doDelete = async () => {
    if (selected.size === 0) return
    setDeleting(true)
    setError('')
    try {
      const { deleted } = await localDeleteDuplicates(mount, [...selected])
      onDeleted(deleted)
      onClose()
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || t('local.duplicates.deleteError'))
      setDeleting(false)
    }
  }

  return (
    <Sheet
      open
      onClose={onClose}
      size="2xl"
      title={t('local.duplicates.title')}
      icon={<CopyCheck className="w-4 h-4 text-purple-400" />}
      footer={
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs text-text-secondary">
            {selectionSummary}
          </span>
          <button
            onClick={doDelete}
            disabled={selected.size === 0 || deleting}
            className="inline-flex items-center gap-1.5 text-sm bg-red-500/15 hover:bg-red-500/25 disabled:opacity-40 text-red-400 border border-red-500/30 px-3 py-1.5 rounded-lg transition-colors font-medium"
          >
            {deleting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Trash2 className="w-4 h-4" />}
            {t('local.duplicates.deleteSelected')}
          </button>
        </div>
      }
    >
      {loading && (
        <div className="flex flex-col items-center justify-center py-12 text-text-secondary gap-3">
          <Loader2 className="w-8 h-8 animate-spin text-purple-400" />
          <span className="text-sm">{t('local.duplicates.comparing')}</span>
          <span className="text-xs text-text-muted">{t('local.duplicates.comparingHint')}</span>
        </div>
      )}

      {!loading && error && (
        <div className="flex items-center gap-2 text-sm text-red-400 py-6">
          <AlertCircle className="w-4 h-4 flex-shrink-0" />{error}
        </div>
      )}

      {!loading && !error && groups.length === 0 && (
        <div className="text-center py-12 text-text-secondary text-sm">
          {t('local.duplicates.empty')}
        </div>
      )}

      {!loading && !error && groups.length > 0 && (
        <div className="flex flex-col gap-4">
          <div className="flex items-center justify-between gap-2 flex-wrap">
            <span className="text-xs text-text-secondary">
              {t('local.duplicates.groupsCount', { count: groups.length })}
            </span>
            <button
              onClick={selectExtras}
              className="text-xs bg-surface-tertiary/60 hover:bg-surface-tertiary text-text-primary border border-strong px-2.5 py-1 rounded-lg transition-colors"
            >
              {t('local.duplicates.markExtras')}
            </button>
          </div>
          {groups.map(g => (
            <GroupCard key={g.hash} group={g} selected={selected} onToggle={toggle} />
          ))}
        </div>
      )}
    </Sheet>
  )
}

// GroupCard renders one duplicate group (the files that share a fingerprint)
// with a checkbox per copy. Split out of DuplicatesModal to keep that component
// simple (the nested map/ternary lived here).
function GroupCard({ group, selected, onToggle }: {
  readonly group: DuplicateGroup
  readonly selected: Set<string>
  readonly onToggle: (path: string) => void
}) {
  const { t } = useTranslation()
  return (
    <div className="border border-default rounded-lg overflow-hidden">
      <div className="bg-surface-tertiary/40 px-3 py-1.5 text-xs text-text-secondary">
        {t('local.duplicates.copiesEach', { count: group.files.length, size: formatSize(group.size) })}
      </div>
      <div className="flex flex-col divide-y divide-default/40">
        {group.files.map(f => (
          <label key={f.path} className="flex items-center gap-2 px-3 py-2 cursor-pointer hover:bg-surface-tertiary/30">
            <input
              type="checkbox"
              checked={selected.has(f.path)}
              onChange={() => onToggle(f.path)}
              aria-label={t('local.duplicates.selectCopy', { name: f.name })}
              className="flex-shrink-0 accent-red-500"
            />
            <div className="min-w-0 flex-1">
              <div className="text-sm text-text-primary truncate">{f.name}</div>
              <div className="text-[11px] text-text-muted truncate">{f.path}</div>
            </div>
          </label>
        ))}
      </div>
    </div>
  )
}
