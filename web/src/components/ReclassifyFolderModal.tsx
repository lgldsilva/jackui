import { useEffect, useState, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  FolderSync, Loader2, Sparkles, FolderOpen,
  Home, ChevronRight, HardDrive, AlertCircle, CheckCircle2,
} from 'lucide-react'
import {
  LocalEntry, localWalk, localPromote, localPromotePreview,
  fetchPromoteDestinations, downloadPromoteBrowse, PromoteDestination,
  PromoteItemResult,
} from '../api/client'
import { Sheet } from './Sheet'
import { useTrackedJobs } from '../lib/transfers'
import FileProgressBar from './FileProgressBar'
import ReclassifyTable from './reclassify/ReclassifyTable'
import {
  buildEditableRows, buildOverrides, selectedPaths, rowTargetPath,
  type ReclassifyRow,
} from './reclassify/rows'

type Props = {
  readonly mount: string
  readonly entry: LocalEntry | null
  readonly onClose: () => void
  readonly onDone: () => void
}

type Phase = 'scanning' | 'configure' | 'preview' | 'executing' | 'done'

type DoneResult = {
  readonly moved: number
  readonly failedCount: number
  readonly destLabel?: string
}

function renderDirList(
  loading: boolean, dirs: string[], browsePath: string,
  setBrowsePath: (p: string) => void, emptyLabel: string,
): JSX.Element {
  if (loading) {
    return <div className="flex items-center justify-center py-6 text-text-muted">
      <Loader2 className="w-4 h-4 animate-spin" />
    </div>
  }
  if (dirs.length === 0) {
    return <p className="text-xs text-text-muted text-center py-4">{emptyLabel}</p>
  }
  return <ul className="space-y-0.5">
    {dirs.map(d => (
      <li key={d}>
        <button
          onClick={() => setBrowsePath(browsePath ? `${browsePath}/${d}` : d)}
          className="w-full flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm text-text-primary hover:bg-surface-tertiary/60 transition-colors"
        >
          <FolderOpen className="w-4 h-4 text-cyan-400 flex-shrink-0" />
          <span className="truncate text-left flex-1 min-w-0">{d}</span>
          <ChevronRight className="w-4 h-4 text-text-muted" />
        </button>
      </li>
    ))}
  </ul>
}

export default function ReclassifyFolderModal({ mount, entry, onClose, onDone }: Props) {
  const { t } = useTranslation()
  const [phase, setPhase] = useState<Phase>('scanning')
  const [files, setFiles] = useState<LocalEntry[]>([])
  const [error, setError] = useState('')
  const { start: startTracking, jobs: promoteJobs, bump } = useTrackedJobs('promote')

  // destination state (same pattern as LocalPromoteModal)
  const [dests, setDests] = useState<PromoteDestination[]>([])
  const [selectedBase, setSelectedBase] = useState('')
  const [browsePath, setBrowsePath] = useState('')
  const [dirs, setDirs] = useState<string[]>([])
  const [dirsLoading, setDirsLoading] = useState(false)

  // Batch table state: editable rows + the IA's ORIGINAL target per path (so we
  // only send an override for rows the user actually changed), plus per-item
  // results after an apply.
  const [rows, setRows] = useState<ReclassifyRow[]>([])
  const [originalByPath, setOriginalByPath] = useState<Record<string, string>>({})
  const [previewLoading, setPreviewLoading] = useState(false)
  const [results, setResults] = useState<Map<string, PromoteItemResult>>(new Map())
  const [result, setResult] = useState<DoneResult | null>(null)

  const finalTarget = browsePath

  // Reset on open
  useEffect(() => {
    if (!entry) return
    setPhase('scanning')
    setFiles([])
    setError('')
    setSelectedBase('')
    setBrowsePath('')
    setRows([])
    setOriginalByPath({})
    setResults(new Map())
    setResult(null)
  }, [entry])

  // Scan folder (or set single file directly)
  useEffect(() => {
    if (!entry || phase !== 'scanning') return
    if (!entry.isDir) {
      setFiles([entry])
      setPhase('configure')
      return
    }
    let cancelled = false
    localWalk(mount, entry.path, true)
      .then(r => {
        if (cancelled) return
        setFiles(r.entries)
        setPhase('configure')
      })
      .catch(e => {
        if (!cancelled) setError(e?.response?.data?.error || e.message || t('reclassify.err_scan'))
      })
    return () => { cancelled = true }
  }, [entry, mount, phase, t])

  // Load destinations
  useEffect(() => {
    if (!entry) return
    fetchPromoteDestinations().then(setDests).catch(() => {})
  }, [entry])

  // Browse dirs inside target
  useEffect(() => {
    if (phase !== 'configure' && phase !== 'preview') return
    setDirsLoading(true)
    downloadPromoteBrowse(browsePath, selectedBase || undefined)
      .then(r => setDirs(r.dirs))
      .catch(() => setDirs([]))
      .finally(() => setDirsLoading(false))
  }, [browsePath, selectedBase, phase])

  const loadPreview = useCallback(async () => {
    if (!entry || files.length === 0) return
    setPreviewLoading(true)
    setError('')
    setResults(new Map())
    setPhase('preview')
    try {
      const r = await localPromotePreview(
        mount, entry.path, finalTarget, selectedBase || undefined,
        files.map(f => f.path),
      )
      const built = buildEditableRows(r.previews)
      setRows(built)
      const orig: Record<string, string> = {}
      built.forEach(row => { orig[row.path] = rowTargetPath(row) })
      setOriginalByPath(orig)
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || t('reclassify.err_preview'))
      setPhase('configure')
    } finally {
      setPreviewLoading(false)
    }
  }, [entry, files, mount, finalTarget, selectedBase, t])

  const toggleRow = useCallback((path: string, selected: boolean) => {
    setRows(rs => rs.map(r => r.path === path ? { ...r, selected } : r))
  }, [])
  const toggleAll = useCallback((selected: boolean) => {
    setRows(rs => rs.map(r => r.error ? r : { ...r, selected }))
  }, [])
  const editRow = useCallback((path: string, field: 'category' | 'finalName', value: string) => {
    setRows(rs => rs.map(r => r.path === path ? { ...r, [field]: value } : r))
  }, [])

  const currentDest = dests.find(d => d.path === selectedBase) ?? dests[0]
  const destLabel = currentDest?.name || t('reclassify.library')

  const selected = useMemo(() => selectedPaths(rows), [rows])

  const handleApply = async () => {
    if (!entry || selected.length === 0) return
    setPhase('executing')
    setError('')
    startTracking() // acompanha o job de promote (IA) no painel/barra
    const t1 = setTimeout(bump, 400)
    const t2 = setTimeout(bump, 1200)
    try {
      const overrides = buildOverrides(rows, originalByPath)
      const r = await localPromote(
        mount, entry.path, finalTarget, selectedBase || undefined,
        true, selected, overrides,
      )
      const map = new Map<string, PromoteItemResult>()
      ;(r.results ?? []).forEach(res => map.set(res.path, res))
      setResults(map)
      // If everything succeeded → show the done screen; otherwise stay on the
      // table with per-row markers so the user can retry the failures.
      if (r.failed === 0) {
        setResult({ moved: r.moved, failedCount: r.failed, destLabel })
        setPhase('done')
      } else {
        setPhase('preview')
        setError(t('reclassify.partial', { moved: r.moved, failed: r.failed }))
      }
      onDone()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || t('reclassify.err_apply'))
      setPhase('preview')
    } finally {
      clearTimeout(t1)
      clearTimeout(t2)
    }
  }

  if (!entry) return null

  const breadcrumb = browsePath.split('/').filter(Boolean)

  return (
    <Sheet
      open
      onClose={onClose}
      size="3xl"
      title={entry?.isDir ? t('reclassify.title_folder') : t('reclassify.title_file')}
      icon={<FolderSync className="w-4 h-4 text-cyan-400 flex-shrink-0" />}
    >
      <>
        {/* Source info */}
        <div className="-mx-4 -mt-4 px-4 py-2.5 border-b border-default bg-surface/40">
          <p className="text-xs text-text-secondary truncate" title={entry.name}>
            {t('reclassify.source')}: <span className="text-text-primary font-mono">{entry.name}</span>
          </p>
        </div>

        {/* Phase: scanning */}
        {phase === 'scanning' && (
          <div className="flex-1 flex flex-col items-center justify-center gap-3 py-12 text-text-secondary">
            <Loader2 className="w-8 h-8 animate-spin text-cyan-400" />
            <p className="text-sm">{t('reclassify.scanning')}</p>
          </div>
        )}

        {/* Phase: configure / preview */}
        {(phase === 'configure' || phase === 'preview') && (
          <>
            {/* File count */}
            <div className="-mx-4 px-4 py-3 border-b border-default flex items-center gap-3 text-sm flex-wrap">
              <span className="text-text-secondary">
                <span className="text-text-primary font-semibold">{files.length}</span>{' '}
                {t('reclassify.files_found', { count: files.length })}
              </span>
            </div>

            {/* Destination chips */}
            {dests.length > 1 && (
              <div className="-mx-4 px-4 py-2 border-b border-default flex items-center gap-2 flex-wrap text-sm">
                <HardDrive className="w-4 h-4 text-text-muted flex-shrink-0" />
                {dests.map(d => (
                  <button
                    key={d.path}
                    onClick={() => { setSelectedBase(d.path); setBrowsePath('') }}
                    className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
                      selectedBase === d.path || (!selectedBase && d.path === dests[0]?.path)
                        ? 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30'
                        : 'bg-surface-tertiary text-text-secondary border border-strong hover:bg-surface-tertiary'
                    }`}
                  >
                    {d.name}
                  </button>
                ))}
              </div>
            )}

            {/* Breadcrumb (only in configure, where the user picks the base subdir) */}
            {phase === 'configure' && (
              <>
                <div className="-mx-4 px-4 py-2 border-b border-default flex items-center gap-1 flex-wrap text-sm text-text-primary">
                  <button
                    onClick={() => setBrowsePath('')}
                    className={`flex items-center gap-1 px-2 py-0.5 rounded ${browsePath === '' ? 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300' : 'hover:bg-surface-tertiary'}`}
                  >
                    <Home className="w-3.5 h-3.5" /> {destLabel}
                  </button>
                  {breadcrumb.map((seg, i) => (
                    <span key={`${i}-${seg}`} className="flex items-center gap-1">
                      <ChevronRight className="w-3 h-3 text-text-muted" />
                      <button
                        onClick={() => setBrowsePath(breadcrumb.slice(0, i + 1).join('/'))}
                        className={`px-2 py-0.5 rounded ${i === breadcrumb.length - 1 ? 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300' : 'hover:bg-surface-tertiary'}`}
                      >
                        {seg}
                      </button>
                    </span>
                  ))}
                </div>
                <div className="min-h-[120px] py-3">
                  {renderDirList(dirsLoading, dirs, browsePath, setBrowsePath, t('reclassify.no_subfolders'))}
                </div>
              </>
            )}

            {/* Preview table (the batch centerpiece) */}
            {phase === 'preview' && (
              <div className="py-2">
                <h3 className="text-xs font-semibold text-cyan-400 flex items-center gap-1 mb-2">
                  <Sparkles className="w-3 h-3" />
                  {t('reclassify.preview_title', { count: rows.length })}
                </h3>
                {previewLoading ? (
                  <div className="flex items-center gap-2 text-xs text-text-muted py-6 justify-center">
                    <Loader2 className="w-3.5 h-3.5 animate-spin text-cyan-400" />
                    <span>{t('reclassify.analyzing')}</span>
                  </div>
                ) : (
                  <div className="max-h-[40vh] overflow-y-auto">
                    <ReclassifyTable
                      rows={rows}
                      destFolders={dirs}
                      results={results}
                      busy={false}
                      onToggle={toggleRow}
                      onToggleAll={toggleAll}
                      onEdit={editRow}
                    />
                  </div>
                )}
              </div>
            )}

            {/* Footer */}
            <div className="-mx-4 -mb-4 mt-2 border-t border-default p-4 flex flex-col gap-3 bg-surface/40">
              <div className="text-xs text-text-muted flex items-start gap-1.5">
                <FolderOpen className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
                <span>
                  {t('reclassify.destination')}: <span className="text-text-primary font-mono">{destLabel}/{finalTarget || ''}</span>
                  {!finalTarget && <span className="text-text-muted"> ({t('reclassify.root')})</span>}
                </span>
              </div>

              {error && (
                <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded px-2 py-1.5 flex items-center gap-1">
                  <AlertCircle className="w-3.5 h-3.5 flex-shrink-0" />{error}
                </p>
              )}

              <div className="flex items-center gap-2 justify-end">
                <button onClick={onClose} className="text-sm text-text-secondary hover:text-text-primary px-3 py-1.5 rounded">
                  {t('reclassify.cancel')}
                </button>
                {phase === 'configure' && (
                  <button
                    onClick={loadPreview}
                    disabled={files.length === 0}
                    className="flex items-center gap-2 text-sm bg-purple-500/20 hover:bg-purple-500/30 disabled:opacity-50 text-purple-700 dark:text-purple-300 border border-purple-500/30 px-4 py-1.5 rounded transition-colors"
                  >
                    <Sparkles className="w-3.5 h-3.5" />
                    {t('reclassify.see_preview')}
                  </button>
                )}
                {phase === 'preview' && !previewLoading && (
                  <>
                    <button
                      onClick={() => setPhase('configure')}
                      className="text-sm text-text-secondary hover:text-text-primary px-3 py-1.5 rounded border border-strong"
                    >
                      {t('reclassify.back')}
                    </button>
                    <button
                      onClick={handleApply}
                      disabled={selected.length === 0}
                      className="flex items-center gap-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 disabled:opacity-50 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-4 py-1.5 rounded transition-colors"
                    >
                      <FolderSync className="w-3.5 h-3.5" />
                      {t('reclassify.apply_selected', { count: selected.length })}
                    </button>
                  </>
                )}
              </div>
            </div>
          </>
        )}

        {/* Phase: executing */}
        {phase === 'executing' && (
          <div className="flex-1 flex flex-col items-center justify-center gap-3 py-12 text-text-secondary">
            {promoteJobs.length > 0 ? (
              <div className="w-full max-w-md flex flex-col gap-3 px-2">
                {promoteJobs.map(j => (
                  <FileProgressBar
                    key={j.id}
                    label={j.label}
                    status={j.status}
                    filesDone={j.filesDone}
                    filesTotal={j.filesTotal}
                    bytesDone={j.bytesDone}
                    bytesTotal={j.bytesTotal}
                    ratePerSec={j.ratePerSec}
                    etaSeconds={j.etaSeconds}
                    progress={j.progress}
                    error={j.error}
                  />
                ))}
              </div>
            ) : (
              <>
                <Loader2 className="w-8 h-8 animate-spin text-cyan-400" />
                <p className="text-sm">{t('reclassify.moving')}</p>
                <p className="text-xs text-text-muted">{selected.length} {t('reclassify.files_found', { count: selected.length })}</p>
              </>
            )}
          </div>
        )}

        {/* Phase: done */}
        {phase === 'done' && result && (
          <div className="flex-1 flex flex-col items-center justify-center gap-4 py-10 px-6">
            <CheckCircle2 className="w-10 h-10 text-green-400" />
            <p className="text-base font-semibold text-text-primary">{t('reclassify.done_title')}</p>
            <p className="text-sm text-text-secondary">
              {t('reclassify.done_count', { count: result.moved })}
              {result.failedCount > 0 && ` · ${t('reclassify.done_failed', { count: result.failedCount })}`}
            </p>
            {result.destLabel && (
              <p className="text-xs text-text-muted font-mono">
                {t('reclassify.destination')}: <span className="text-cyan-400">{result.destLabel}</span>
              </p>
            )}
            <button
              onClick={onClose}
              className="mt-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-5 py-2 rounded transition-colors"
            >
              {t('reclassify.close')}
            </button>
          </div>
        )}
      </>
    </Sheet>
  )
}
