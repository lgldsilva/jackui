import { useEffect, useState, useCallback } from 'react'
import {
  FolderSync, X, Loader2, ArrowRight, Sparkles, FolderOpen,
  Home, ChevronRight, HardDrive, Plus, AlertCircle, CheckCircle2,
} from 'lucide-react'
import {
  LocalEntry, localWalk, localPromote, localPromotePreview,
  fetchPromoteDestinations, downloadPromoteBrowse, PromoteDestination, PromotePreviewEntry,
} from '../api/client'
import { useScrollLock } from '../lib/useScrollLock'

type Props = {
  readonly mount: string
  readonly entry: LocalEntry | null
  readonly onClose: () => void
  readonly onDone: () => void
}

type Phase = 'scanning' | 'configure' | 'preview' | 'executing' | 'done'

type DoneResult = {
  readonly moved: number
  readonly failed: readonly { path: string; error: string }[]
  readonly destLabel?: string
}

export default function ReclassifyFolderModal({ mount, entry, onClose, onDone }: Props) {
  useScrollLock(!!entry)

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
    return browsePath ? `${browsePath}/${trimmed}` : trimmed
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
      if (failedCount !== undefined) {
        setResult({ moved: files.length - failedCount, failed: [] })
        setPhase('done')
        onDone()
      } else {
        setError(msg)
        setPhase('preview')
      }
    }
  }

  if (!entry) return null

  const currentDest = dests.find(d => d.path === selectedBase) || dests[0]
  const destLabel = currentDest?.name || 'Biblioteca'
  const breadcrumb = browsePath.split('/').filter(Boolean)

  return (
    <div
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4"
      onClick={e => e.target === e.currentTarget && onClose()}
      onKeyDown={e => e.key === 'Escape' && onClose()}
      role="dialog" aria-modal="true" tabIndex={-1}
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-xl shadow-2xl max-h-[90vh] flex flex-col">
        {/* Header */}
        <header className="flex items-center justify-between p-4 border-b border-gray-700">
          <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2">
            <FolderSync className="w-5 h-5 text-cyan-400 flex-shrink-0" />
            {entry?.isDir ? 'Reclassificar pasta via IA' : 'Classificar e mover via IA'}
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-100">
            <X className="w-5 h-5" />
          </button>
        </header>

        {/* Source info */}
        <div className="px-4 py-2.5 border-b border-gray-700 bg-gray-900/40">
          <p className="text-xs text-gray-400 truncate" title={entry.name}>
            Pasta: <span className="text-gray-300 font-mono">{entry.name}</span>
          </p>
        </div>

        {/* Phase: scanning */}
        {phase === 'scanning' && (
          <div className="flex-1 flex flex-col items-center justify-center gap-3 py-12 text-gray-400">
            <Loader2 className="w-8 h-8 animate-spin text-cyan-400" />
            <p className="text-sm">Varrendo pasta de mídia…</p>
          </div>
        )}

        {/* Phase: configure / preview */}
        {(phase === 'configure' || phase === 'preview') && (
          <>
            {/* File count + destination selector */}
            <div className="px-4 py-3 border-b border-gray-700 flex items-center gap-3 text-sm flex-wrap">
              <span className="text-gray-400">
                {entry?.isDir
                  ? <><span className="text-white font-semibold">{files.length}</span> arquivo{files.length !== 1 ? 's' : ''} de mídia encontrado{files.length !== 1 ? 's' : ''}</>
                  : <><span className="text-white font-semibold">1</span> arquivo selecionado</>
                }
              </span>
            </div>

            {/* Destination chips */}
            {dests.length > 1 && (
              <div className="px-4 py-2 border-b border-gray-700 flex items-center gap-2 flex-wrap text-sm">
                <HardDrive className="w-4 h-4 text-gray-500 flex-shrink-0" />
                {dests.map(d => (
                  <button
                    key={d.path}
                    onClick={() => { setSelectedBase(d.path); setBrowsePath('') }}
                    className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
                      selectedBase === d.path || (!selectedBase && d === dests[0])
                        ? 'bg-cyan-500/20 text-cyan-300 border border-cyan-500/30'
                        : 'bg-gray-700 text-gray-400 border border-gray-600 hover:bg-gray-600'
                    }`}
                  >
                    {d.name}
                  </button>
                ))}
              </div>
            )}

            {/* Breadcrumb */}
            <div className="px-4 py-2 border-b border-gray-700 flex items-center gap-1 flex-wrap text-sm text-gray-300">
              <button
                onClick={() => setBrowsePath('')}
                className={`flex items-center gap-1 px-2 py-0.5 rounded ${browsePath === '' ? 'bg-cyan-500/20 text-cyan-300' : 'hover:bg-gray-700'}`}
              >
                <Home className="w-3.5 h-3.5" /> {destLabel}
              </button>
              {breadcrumb.map((seg, i) => (
                <span key={`${i}-${seg}`} className="flex items-center gap-1">
                  <ChevronRight className="w-3 h-3 text-gray-600" />
                  <button
                    onClick={() => setBrowsePath(breadcrumb.slice(0, i + 1).join('/'))}
                    className={`px-2 py-0.5 rounded ${i === breadcrumb.length - 1 ? 'bg-cyan-500/20 text-cyan-300' : 'hover:bg-gray-700'}`}
                  >
                    {seg}
                  </button>
                </span>
              ))}
            </div>

            {/* Dir browser */}
            <div className="flex-1 overflow-y-auto min-h-[120px] p-3">
              {dirsLoading ? (
                <div className="flex items-center justify-center py-6 text-gray-500">
                  <Loader2 className="w-4 h-4 animate-spin" />
                </div>
              ) : dirs.length === 0 ? (
                <p className="text-xs text-gray-500 text-center py-4">Sem subpastas. Crie uma abaixo ou organize na raiz.</p>
              ) : (
                <ul className="space-y-0.5">
                  {dirs.map(d => (
                    <li key={d}>
                      <button
                        onClick={() => setBrowsePath(browsePath ? `${browsePath}/${d}` : d)}
                        className="w-full flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm text-gray-200 hover:bg-gray-700/60 transition-colors"
                      >
                        <FolderOpen className="w-4 h-4 text-cyan-400 flex-shrink-0" />
                        <span className="truncate text-left flex-1">{d}</span>
                        <ChevronRight className="w-4 h-4 text-gray-600" />
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            {/* Preview panel */}
            {phase === 'preview' && (
              <div className="px-4 pb-2 max-h-52 overflow-y-auto">
                <div className="border border-cyan-800/40 bg-gray-950/60 rounded-xl p-3 space-y-2">
                  <h3 className="text-xs font-semibold text-cyan-400 flex items-center gap-1">
                    <Sparkles className="w-3 h-3" />
                    Preview IA — {previews.length} arquivo{previews.length !== 1 ? 's' : ''}
                  </h3>
                  {previewLoading ? (
                    <div className="flex items-center gap-2 text-xs text-gray-500 py-2 justify-center">
                      <Loader2 className="w-3.5 h-3.5 animate-spin text-cyan-400" />
                      <span>Analisando nomes com IA…</span>
                    </div>
                  ) : (
                    <div className="space-y-2 divide-y divide-gray-800/40">
                      {previews.map((p, i) => (
                        <div key={`${p.originalName}-${i}`} className="pt-2 first:pt-0 text-xs space-y-1">
                          <p className="text-[10px] text-gray-400 font-mono truncate" title={p.originalName}>
                            De: {p.originalName}
                          </p>
                          {p.error ? (
                            <p className="text-red-400 text-[11px] bg-red-950/30 px-2 py-1 rounded border border-red-900/30">
                              Erro: {p.error}
                            </p>
                          ) : (
                            <div className="flex items-start gap-1.5 bg-emerald-950/10 border border-emerald-900/30 px-2 py-1.5 rounded-lg text-emerald-300">
                              <ArrowRight className="w-3 h-3 mt-0.5 text-emerald-400 flex-shrink-0" />
                              <span className="font-mono text-[11px] break-all leading-tight">
                                <span className="text-gray-500">{p.targetPath.split('/').slice(0, -1).join('/')}/</span>
                                <span className="text-white font-bold">{p.targetPath.split('/').pop()}</span>
                                <span className="ml-1 px-1.5 text-[9px] font-bold rounded bg-cyan-900/40 text-cyan-300 border border-cyan-700/40">
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
            <div className="border-t border-gray-700 p-4 flex flex-col gap-3 bg-gray-900/40">
              <label className="flex items-center gap-2 text-sm text-gray-300">
                <Plus className="w-4 h-4 text-gray-500 flex-shrink-0" />
                <input
                  type="text"
                  value={newFolder}
                  onChange={e => setNewFolder(e.target.value)}
                  placeholder="Nova subpasta (opcional)"
                  className="flex-1 bg-gray-700 border border-gray-600 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-cyan-500 text-gray-100"
                />
              </label>

              <div className="text-xs text-gray-500 flex items-start gap-1.5">
                <FolderOpen className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
                <span>
                  Destino: <span className="text-gray-300 font-mono">{destLabel}/{finalTarget || ''}</span>
                  {!finalTarget && <span className="text-gray-600"> (raiz)</span>}
                </span>
              </div>

              {error && (
                <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded px-2 py-1.5 flex items-center gap-1">
                  <AlertCircle className="w-3.5 h-3.5 flex-shrink-0" />{error}
                </p>
              )}

              <div className="flex items-center gap-2 justify-end">
                <button onClick={onClose} className="text-sm text-gray-400 hover:text-gray-200 px-3 py-1.5 rounded">
                  Cancelar
                </button>
                {phase === 'configure' && (
                  <button
                    onClick={loadPreview}
                    disabled={files.length === 0}
                    className="flex items-center gap-2 text-sm bg-purple-500/20 hover:bg-purple-500/30 disabled:opacity-50 text-purple-300 border border-purple-500/30 px-4 py-1.5 rounded transition-colors"
                  >
                    <Sparkles className="w-3.5 h-3.5" />
                    Ver preview IA
                  </button>
                )}
                {phase === 'preview' && !previewLoading && (
                  <>
                    <button
                      onClick={() => setPhase('configure')}
                      className="text-sm text-gray-400 hover:text-gray-200 px-3 py-1.5 rounded border border-gray-600"
                    >
                      Voltar
                    </button>
                    <button
                      onClick={handleConfirm}
                      disabled={previews.length === 0}
                      className="flex items-center gap-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 disabled:opacity-50 text-cyan-300 border border-cyan-500/30 px-4 py-1.5 rounded transition-colors"
                    >
                      <FolderSync className="w-3.5 h-3.5" />
                      Mover {previews.filter(p => !p.error).length} arquivo{previews.filter(p => !p.error).length !== 1 ? 's' : ''}
                    </button>
                  </>
                )}
              </div>
            </div>
          </>
        )}

        {/* Phase: executing */}
        {phase === 'executing' && (
          <div className="flex-1 flex flex-col items-center justify-center gap-3 py-12 text-gray-400">
            <Loader2 className="w-8 h-8 animate-spin text-cyan-400" />
            <p className="text-sm">Movendo e organizando arquivos…</p>
            <p className="text-xs text-gray-500">{files.length} arquivo{files.length !== 1 ? 's' : ''}</p>
          </div>
        )}

        {/* Phase: done */}
        {phase === 'done' && result && (
          <div className="flex-1 flex flex-col items-center justify-center gap-4 py-10 px-6">
            <CheckCircle2 className="w-10 h-10 text-green-400" />
            <p className="text-base font-semibold text-gray-100">Reclassificação concluída</p>
            <p className="text-sm text-gray-400">
              {result.moved} arquivo{result.moved !== 1 ? 's' : ''} organizado{result.moved !== 1 ? 's' : ''} com sucesso
              {result.failed.length > 0 && ` · ${result.failed.length} com erro`}
            </p>
            {result.destLabel && (
              <p className="text-xs text-gray-500 font-mono">
                Destino: <span className="text-cyan-400">{result.destLabel}</span>
              </p>
            )}
            <button
              onClick={onClose}
              className="mt-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-300 border border-cyan-500/30 px-5 py-2 rounded transition-colors"
            >
              Fechar
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
