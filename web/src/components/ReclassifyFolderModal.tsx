import { useEffect, useState, useCallback } from 'react'
import {
  FolderSync, Loader2, ArrowRight, Sparkles, FolderOpen,
  Home, ChevronRight, HardDrive, Plus, AlertCircle, CheckCircle2,
} from 'lucide-react'
import {
  LocalEntry, localWalk, localPromote, localPromotePreview,
  fetchPromoteDestinations, downloadPromoteBrowse, PromoteDestination, PromotePreviewEntry,
} from '../api/client'
import { Sheet } from './Sheet'

type Props = {
  readonly mount: string
  readonly entry: LocalEntry | null
  readonly onClose: () => void
  readonly onDone: () => void
}

function fileCountLabel(entry: LocalEntry | null, files: LocalEntry[]): React.ReactNode {
  if (!entry?.isDir) {
    return <><span className="text-text-primary font-semibold">1</span> arquivo selecionado</>
  }
  const s = files.length === 1 ? '' : 's'
  return <><span className="text-text-primary font-semibold">{files.length}</span> arquivo{s} de mídia encontrado{s}</>
}

type Phase = 'scanning' | 'configure' | 'preview' | 'executing' | 'done'

type DoneResult = {
  readonly moved: number
  readonly failed: readonly { path: string; error: string }[]
  readonly destLabel?: string
}

function renderDirList(loading: boolean, dirs: string[], browsePath: string, setBrowsePath: (p: string) => void): JSX.Element {
  if (loading) {
    return <div className="flex items-center justify-center py-6 text-text-muted">
      <Loader2 className="w-4 h-4 animate-spin" />
    </div>
  }
  if (dirs.length === 0) {
    return <p className="text-xs text-text-muted text-center py-4">Sem subpastas. Crie uma abaixo ou organize na raiz.</p>
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
  const [phase, setPhase] = useState<Phase>('scanning')
  const [files, setFiles] = useState<LocalEntry[]>([])
  const [error, setError] = useState('')

  // destination state (same pattern as LocalPromoteModal)
  const [dests, setDests] = useState<PromoteDestination[]>([])
  const [selectedBase, setSelectedBase] = useState('')
  const [browsePath, setBrowsePath] = useState('')
  const [dirs, setDirs] = useState<string[]>([])
  const [dirsLoading, setDirsLoading] = useState(false)
  const [newFolder, setNewFolder] = useState('')

  const [previews, setPreviews] = useState<PromotePreviewEntry[]>([])
  const [previewLoading, setPreviewLoading] = useState(false)

  const [result, setResult] = useState<DoneResult | null>(null)

  const finalTarget = (() => {
    const trimmed = newFolder.trim()
    if (!trimmed) return browsePath
    if (browsePath) return `${browsePath}/${trimmed}`
    return trimmed
  })()

  // Reset on open
  useEffect(() => {
    if (!entry) return
    setPhase('scanning')
    setFiles([])
    setError('')
    setSelectedBase('')
    setBrowsePath('')
    setNewFolder('')
    setPreviews([])
    setResult(null)
  }, [entry])

  // Scan folder (or set single file directly)
  useEffect(() => {
    if (!entry || phase !== 'scanning') return
    // Single file: skip scan, use entry directly
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
        if (!cancelled) setError(e?.response?.data?.error || e.message || 'Erro ao varrer pasta')
      })
    return () => { cancelled = true }
  }, [entry, mount, phase])

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
    setPhase('preview')
    try {
      const r = await localPromotePreview(
        mount,
        entry.path,
        finalTarget,
        selectedBase || undefined,
        files.map(f => f.path),
      )
      setPreviews(r.previews)
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || 'Erro gerando preview')
      setPhase('configure')
    } finally {
      setPreviewLoading(false)
    }
  }, [entry, files, mount, finalTarget, selectedBase])

  const handleConfirm = async () => {
    if (!entry) return
    setPhase('executing')
    setError('')
    try {
      const r = await localPromote(
        mount,
        entry.path,
        finalTarget,
        selectedBase || undefined,
        true, // renameIA always on for reclassify
        files.map(f => f.path),
      )
      // Report the real counts from the backend — it moves every entry in the
      // batch and tells us how many actually succeeded vs failed.
      setResult({ moved: r.moved, failed: r.errors, destLabel })
      setPhase('done')
      onDone()
    } catch (e: any) {
      const msg = e?.response?.data?.error || e.message || 'Erro ao reclassificar'
      const failedCount = typeof e?.response?.data?.failed === 'number' ? e.response.data.failed : undefined
      if (failedCount === undefined) {
        setError(msg)
        setPhase('preview')
      } else {
        setResult({ moved: files.length - failedCount, failed: [] })
        setPhase('done')
        onDone()
      }
    }
  }

  if (!entry) return null

  const currentDest = dests.find(d => d.path === selectedBase) ?? dests[0]
  const destLabel = currentDest?.name || 'Biblioteca'
  const breadcrumb = browsePath.split('/').filter(Boolean)

  return (
    <Sheet
      open
      onClose={onClose}
      size="xl"
      title={entry?.isDir ? 'Reclassificar pasta via IA' : 'Classificar e mover via IA'}
      icon={<FolderSync className="w-4 h-4 text-cyan-400 flex-shrink-0" />}
    >
      <>
        {/* Source info */}
        <div className="-mx-4 -mt-4 px-4 py-2.5 border-b border-default bg-surface/40">
          <p className="text-xs text-text-secondary truncate" title={entry.name}>
            Pasta: <span className="text-text-primary font-mono">{entry.name}</span>
          </p>
        </div>

        {/* Phase: scanning */}
        {phase === 'scanning' && (
          <div className="flex-1 flex flex-col items-center justify-center gap-3 py-12 text-text-secondary">
            <Loader2 className="w-8 h-8 animate-spin text-cyan-400" />
            <p className="text-sm">Varrendo pasta de mídia…</p>
          </div>
        )}

        {/* Phase: configure / preview */}
        {(phase === 'configure' || phase === 'preview') && (
          <>
            {/* File count + destination selector */}
            <div className="-mx-4 px-4 py-3 border-b border-default flex items-center gap-3 text-sm flex-wrap">
              <span className="text-text-secondary">{fileCountLabel(entry, files)}</span>
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

            {/* Breadcrumb */}
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

            {/* Dir browser */}
            <div className="min-h-[120px] py-3">
              {renderDirList(dirsLoading, dirs, browsePath, setBrowsePath)}
            </div>

            {/* Preview panel */}
            {phase === 'preview' && (
              <div className="pb-2 max-h-52 overflow-y-auto">
                <div className="border border-cyan-500/30 dark:border-cyan-800/40 bg-surface-elevated/60 rounded-xl p-3 space-y-2">
                  <h3 className="text-xs font-semibold text-cyan-400 flex items-center gap-1">
                    <Sparkles className="w-3 h-3" />
                    Preview IA — {previews.length} arquivo{previews.length === 1 ? '' : 's'}
                  </h3>
                  {previewLoading ? (
                    <div className="flex items-center gap-2 text-xs text-text-muted py-2 justify-center">
                      <Loader2 className="w-3.5 h-3.5 animate-spin text-cyan-400" />
                      <span>Analisando nomes com IA…</span>
                    </div>
                  ) : (
                    <div className="space-y-2 divide-y divide-default">
                      {previews.map((p, i) => (
                        <div key={`${p.originalName}-${i}`} className="pt-2 first:pt-0 text-xs space-y-1">
                          <p className="text-[10px] text-text-secondary font-mono truncate" title={p.originalName}>
                            De: {p.originalName}
                          </p>
                          {p.error ? (
                            <p className="text-red-700 dark:text-red-400 text-[11px] bg-red-500/10 dark:bg-red-950/30 px-2 py-1 rounded border border-red-500/30 dark:border-red-900/30">
                              Erro: {p.error}
                            </p>
                          ) : (
                            <div className="flex items-start gap-1.5 bg-emerald-500/10 dark:bg-emerald-950/10 border border-emerald-500/30 dark:border-emerald-900/30 px-2 py-1.5 rounded-lg text-emerald-700 dark:text-emerald-300">
                              <ArrowRight className="w-3 h-3 mt-0.5 text-emerald-400 flex-shrink-0" />
                              <span className="font-mono text-[11px] break-all leading-tight">
                                <span className="text-text-muted">{p.targetPath.split('/').slice(0, -1).join('/')}/</span>
                                <span className="text-text-primary font-bold">{p.targetPath.split('/').pop()}</span>
                                <span className="ml-1 px-1.5 text-[9px] font-bold rounded bg-cyan-500/15 dark:bg-cyan-900/40 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 dark:border-cyan-700/40">
                                  {p.kind === 'tv' ? 'Série' : 'Filme'}
                                </span>
                              </span>
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* Footer */}
            <div className="-mx-4 -mb-4 mt-2 border-t border-default p-4 flex flex-col gap-3 bg-surface/40">
              <label className="flex items-center gap-2 text-sm text-text-primary">
                <Plus className="w-4 h-4 text-text-muted flex-shrink-0" />
                <input
                  type="text"
                  value={newFolder}
                  onChange={e => setNewFolder(e.target.value)}
                  placeholder="Nova subpasta (opcional)"
                  className="flex-1 bg-surface-tertiary border border-strong rounded px-3 py-1.5 text-sm focus:outline-none focus:border-cyan-500 text-text-primary"
                />
              </label>

              <div className="text-xs text-text-muted flex items-start gap-1.5">
                <FolderOpen className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
                <span>
                  Destino: <span className="text-text-primary font-mono">{destLabel}/{finalTarget || ''}</span>
                  {!finalTarget && <span className="text-text-muted"> (raiz)</span>}
                </span>
              </div>

              {error && (
                <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded px-2 py-1.5 flex items-center gap-1">
                  <AlertCircle className="w-3.5 h-3.5 flex-shrink-0" />{error}
                </p>
              )}

              <div className="flex items-center gap-2 justify-end">
                <button onClick={onClose} className="text-sm text-text-secondary hover:text-text-primary px-3 py-1.5 rounded">
                  Cancelar
                </button>
                {phase === 'configure' && (
                  <button
                    onClick={loadPreview}
                    disabled={files.length === 0}
                    className="flex items-center gap-2 text-sm bg-purple-500/20 hover:bg-purple-500/30 disabled:opacity-50 text-purple-700 dark:text-purple-300 border border-purple-500/30 px-4 py-1.5 rounded transition-colors"
                  >
                    <Sparkles className="w-3.5 h-3.5" />
                    Ver preview IA
                  </button>
                )}
                {phase === 'preview' && !previewLoading && (
                  <>
                    <button
                      onClick={() => setPhase('configure')}
                      className="text-sm text-text-secondary hover:text-text-primary px-3 py-1.5 rounded border border-strong"
                    >
                      Voltar
                    </button>
                    <button
                      onClick={handleConfirm}
                      disabled={previews.length === 0}
                      className="flex items-center gap-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 disabled:opacity-50 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-4 py-1.5 rounded transition-colors"
                    >
                      <FolderSync className="w-3.5 h-3.5" />
                      Mover {previews.filter(p => !p.error).length} arquivo{previews.filter(p => !p.error).length === 1 ? '' : 's'}
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
            <Loader2 className="w-8 h-8 animate-spin text-cyan-400" />
            <p className="text-sm">Movendo e organizando arquivos…</p>
            <p className="text-xs text-text-muted">{files.length} arquivo{files.length === 1 ? '' : 's'}</p>
          </div>
        )}

        {/* Phase: done */}
        {phase === 'done' && result && (
          <div className="flex-1 flex flex-col items-center justify-center gap-4 py-10 px-6">
            <CheckCircle2 className="w-10 h-10 text-green-400" />
            <p className="text-base font-semibold text-text-primary">Reclassificação concluída</p>
            <p className="text-sm text-text-secondary">
              {result.moved} arquivo{result.moved === 1 ? '' : 's'} organizado{result.moved === 1 ? '' : 's'} com sucesso
              {result.failed.length > 0 && ` · ${result.failed.length} com erro`}
            </p>
            {result.destLabel && (
              <p className="text-xs text-text-muted font-mono">
                Destino: <span className="text-cyan-400">{result.destLabel}</span>
              </p>
            )}
            <button
              onClick={onClose}
              className="mt-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-5 py-2 rounded transition-colors"
            >
              Fechar
            </button>
          </div>
        )}
      </>
    </Sheet>
  )
}
