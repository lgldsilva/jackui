import { useEffect, useState } from 'react'
import { ArrowUpCircle, Folder, Loader2, X, ChevronRight, Plus, FolderOpen, Home, HardDrive, Sparkles, ArrowRight } from 'lucide-react'
import { DownloadEntry, downloadPromoteBrowse, downloadPromoteBatch, fetchPromoteDestinations, PromoteDestination, downloadPromotePreview, PromotePreviewEntry } from '../api/client'
import { useScrollLock } from '../lib/useScrollLock'

type Props = {
  readonly items: DownloadEntry[] | null
  readonly onClose: () => void
  readonly onPromoted: (result: { promoted: DownloadEntry[]; failed: { id: number; error: string }[] }) => void
}

/**
 * Navegador de subpastas + seletor de destino + ações de promover. Suporta
 * single OU batch — sempre chama o endpoint batch (single = ids:[1]). Permite
 * digitar uma subpasta nova (criada pelo backend com os.MkdirAll).
 */
export default function PromoteModal({ items, onClose, onPromoted }: Props) {
  useScrollLock(!!items)
  const [dests, setDests] = useState<PromoteDestination[]>([])
  // selectedBase é o path do destino selecionado; "" = sharedDir (default).
  const [selectedBase, setSelectedBase] = useState('')
  const [path, setPath] = useState('')
  const [dirs, setDirs] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [newFolder, setNewFolder] = useState('')
  const [keepSeeding, setKeepSeeding] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [renameIA, setRenameIA] = useState(false)
  const [previews, setPreviews] = useState<PromotePreviewEntry[]>([])
  const [previewLoading, setPreviewLoading] = useState(false)

  const finalTarget = (() => {
    const trimmed = newFolder.trim()
    if (!trimmed) return path
    return path ? `${path}/${trimmed}` : trimmed
  })()

  // Carrega destinos disponíveis ao abrir
  useEffect(() => {
    if (!items) return
    fetchPromoteDestinations().then(setDests).catch(() => {})
  }, [items])

  // Carrega subpastas do destino selecionado sempre que path ou base mudar
  useEffect(() => {
    if (!items) return
    setLoading(true)
    setError('')
    downloadPromoteBrowse(path, selectedBase || undefined)
      .then(r => setDirs(r.dirs))
      .catch(e => setError(e?.response?.data?.error || e.message || 'Erro listando subpastas'))
      .finally(() => setLoading(false))
  }, [path, items, selectedBase])

  // Carrega preview da Renomeação IA
  useEffect(() => {
    if (!items || !renameIA) {
      setPreviews([])
      return
    }
    setPreviewLoading(true)
    setError('')
    downloadPromotePreview(
      items.map(i => i.id),
      { targetSubdir: finalTarget, targetBase: selectedBase || undefined }
    )
      .then(r => setPreviews(r.previews))
      .catch(e => setError(e?.response?.data?.error || e.message || 'Erro gerando preview IA'))
      .finally(() => setPreviewLoading(false))
  }, [renameIA, items, finalTarget, selectedBase])

  // Reset ao abrir/fechar
  useEffect(() => {
    if (items) {
      setSelectedBase('')
      setPath('')
      setNewFolder('')
      setKeepSeeding(true)
      setRenameIA(false)
      setPreviews([])
      setError('')
    }
  }, [items])

  if (!items) return null

  const currentDest = dests.find(d => d.path === selectedBase) || dests[0]
  const destLabel = currentDest?.name || 'Biblioteca'

  const handlePromote = async () => {
    setSubmitting(true)
    try {
      const result = await downloadPromoteBatch(
        items.map(i => i.id),
        { keepSeeding, targetSubdir: finalTarget, targetBase: selectedBase || undefined, renameIA },
      )
      onPromoted(result)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSubmitting(false)
    }
  }

  const breadcrumb = path.split('/').filter(Boolean)

  return (
    <dialog
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-end sm:items-center justify-center z-50 sm:p-4 open:flex"
      onClick={e => e.target === e.currentTarget && onClose()}
      onKeyDown={e => e.key === 'Escape' && onClose()}
      onClose={onClose}
      onFocus={() => {}} tabIndex={-1}
      open
    >
      {/* No mobile vira bottom-sheet (items-end) com altura em dvh — vh não
          desconta a barra do browser e o modal centralizado estourava a tela. */}
      <div className="bg-surface-secondary rounded-t-2xl sm:rounded-2xl border border-default w-full max-w-lg shadow-2xl max-h-[92dvh] sm:max-h-[90vh] flex flex-col">
        <header className="flex items-center justify-between p-4 border-b border-default">
          <h2 className="text-base font-semibold text-text-primary flex items-center gap-2">
            <ArrowUpCircle className="w-5 h-5 text-cyan-400" />
            Promover {items.length > 1 ? `${items.length} downloads` : 'download'}
          </h2>
          <button onClick={onClose} className="text-text-secondary hover:text-text-primary">
            <X className="w-5 h-5" />
          </button>
        </header>

        {/* Lista de items sendo promovidos */}
        <div className="px-4 py-2 border-b border-default bg-surface/40 max-h-32 overflow-y-auto">
          {items.map(d => (
            <p key={d.id} className="text-xs text-text-secondary truncate" title={d.name || d.filePath}>
              • {d.name || d.filePath}
            </p>
          ))}
        </div>

        {/* Seletor de destino */}
        {dests.length > 1 && (
          <div className="px-4 py-2 border-b border-default flex items-center gap-2 flex-wrap text-sm">
            <HardDrive className="w-4 h-4 text-text-muted flex-shrink-0" />
            {dests.map(d => (
              <button
                key={d.path}
                onClick={() => { setSelectedBase(d.path); setPath('') }}
                className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
                  selectedBase === d.path || (!selectedBase && d === dests[0])
                    ? 'bg-cyan-500/20 text-cyan-300 border border-cyan-500/30'
                    : 'bg-surface-tertiary text-text-secondary border border-strong hover:bg-surface-tertiary'
                }`}
              >
                {d.name}
              </button>
            ))}
          </div>
        )}

        {/* Breadcrumb navegador */}
        <div className="px-4 py-2 border-b border-default flex items-center gap-1 flex-wrap text-sm text-text-primary">
          <button
            onClick={() => setPath('')}
            className={`flex items-center gap-1 px-2 py-0.5 rounded ${path === '' ? 'bg-cyan-500/20 text-cyan-300' : 'hover:bg-surface-tertiary'}`}
          >
            <Home className="w-3.5 h-3.5" /> {destLabel}
          </button>
          {breadcrumb.map((seg, i) => (
            <span key={`${i}-${seg}`} className="flex items-center gap-1">
              <ChevronRight className="w-3 h-3 text-text-muted" />
              <button
                onClick={() => setPath(breadcrumb.slice(0, i + 1).join('/'))}
                className={`px-2 py-0.5 rounded ${i === breadcrumb.length - 1 ? 'bg-cyan-500/20 text-cyan-300' : 'hover:bg-surface-tertiary'}`}
              >
                {seg}
              </button>
            </span>
          ))}
        </div>

        {/* Subpastas */}
        <div className="flex-1 overflow-y-auto p-4">
          {(() => {
            if (loading) return <div className="flex items-center justify-center py-8 text-text-muted"><Loader2 className="w-5 h-5 animate-spin" /></div>
            if (dirs.length === 0) return <p className="text-sm text-text-muted text-center py-4">Sem subpastas aqui. Crie uma abaixo ou promova nesta raiz.</p>
            return <ul className="space-y-1">{dirs.map(d => (
              <li key={d}>
                <button onClick={() => setPath(path ? `${path}/${d}` : d)}
                  className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-text-primary hover:bg-surface-tertiary/60 transition-colors">
                  <Folder className="w-4 h-4 text-cyan-400 flex-shrink-0" />
                  <span className="truncate text-left flex-1 min-w-0">{d}</span>
                  <ChevronRight className="w-4 h-4 text-text-muted" />
                </button>
              </li>
            ))}</ul>
          })()}
        </div>

        {/* Nova pasta + opções + ações */}
        <div className="border-t border-default p-4 safe-bottom flex flex-col gap-3 bg-surface/40">
          <label className="flex items-center gap-2 text-sm text-text-primary">
            <Plus className="w-4 h-4 text-text-muted flex-shrink-0" />
            <input
              type="text"
              value={newFolder}
              onChange={e => setNewFolder(e.target.value)}
              placeholder="Nova subpasta (opcional)"
              className="flex-1 bg-surface-tertiary border border-strong rounded px-3 py-1.5 text-sm focus:outline-none focus:border-cyan-500"
            />
          </label>

          <label className="flex items-center gap-2 text-sm text-text-primary cursor-pointer font-medium hover:text-white transition-colors">
            <input
              type="checkbox"
              checked={renameIA}
              onChange={e => setRenameIA(e.target.checked)}
              className="accent-cyan-500 w-4 h-4 rounded border-strong focus:ring-cyan-500 bg-surface-tertiary"
            />
            <span className="flex items-center gap-1.5 text-cyan-300 font-semibold bg-cyan-950/40 border border-cyan-800/50 px-2 py-0.5 rounded-full text-xs">
              <Sparkles className="w-3.5 h-3.5 text-cyan-400" />
              Renomear e Organizar via IA (Plex style)
            </span>
          </label>

          {renameIA && (
            <div className="mt-1 border border-cyan-800/40 bg-surface-elevated/60 rounded-xl p-3 max-h-48 overflow-y-auto space-y-2 backdrop-blur-md">
              <h3 className="text-xs font-semibold text-cyan-400 flex items-center gap-1">
                <Sparkles className="w-3 h-3" />
                Visualização do Destino Organizado:
              </h3>
              {(() => {
                if (previewLoading) return <div className="flex items-center gap-2 text-xs text-text-muted py-2 justify-center"><Loader2 className="w-3.5 h-3.5 animate-spin text-cyan-400" /><span>Analisando nomes com IA...</span></div>
                if (previews.length === 0) return <p className="text-xs text-text-muted text-center py-2">Nenhum preview gerado.</p>
                return <div className="space-y-2 divide-y divide-default">{previews.map((p, index) => (
                  <div key={`${p.originalName}-${index}`} className="pt-2 first:pt-0 text-xs space-y-1">
                    <div className="text-[10px] text-text-secondary font-mono truncate" title={p.originalName}>De: {p.originalName}</div>
                    {p.error ? (
                      <div className="text-red-400 text-[11px] bg-red-950/30 px-2 py-1 rounded border border-red-900/30">Erro: {p.error}</div>
                    ) : (
                      <div className="flex items-start gap-1.5 bg-emerald-950/10 border border-emerald-900/30 px-2 py-1.5 rounded-lg text-emerald-300">
                        <ArrowRight className="w-3 h-3 mt-0.5 text-emerald-400 flex-shrink-0" />
                        <div className="font-mono text-[11px] break-all leading-tight">
                          <span className="text-text-muted">Para: </span>
                          <span className="font-semibold text-emerald-450">{p.targetPath.split('/').slice(0, -1).join('/')}/</span>
                          <span className="text-white font-bold">{p.targetPath.split('/').pop()}</span>
                          <span className="ml-1 px-1.5 py-0.2 text-[9px] font-bold rounded bg-cyan-900/40 text-cyan-300 border border-cyan-700/40">{p.kind === 'tv' ? 'Série' : 'Filme'}</span>
                        </div>
                      </div>
                    )}
                  </div>
                ))}</div>
              })()}
            </div>
          )}

          <label className="flex items-center gap-2 text-sm text-text-primary cursor-pointer">
            <input
              type="checkbox"
              checked={keepSeeding}
              onChange={e => setKeepSeeding(e.target.checked)}
              className="accent-cyan-500"
            />
            {' '}Continuar seedando após mover (preserva ratio em trackers privados)
          </label>

          <div className="text-xs text-text-muted flex items-start gap-1.5">
            <FolderOpen className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
            <span>
              Destino: <span className="text-text-primary font-mono">{destLabel}/{finalTarget || ''}</span>
              {!finalTarget && <span className="text-text-muted"> (raiz)</span>}
            </span>
          </div>

          {error && (
            <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded px-2 py-1.5">{error}</p>
          )}

          <div className="flex items-center gap-2 justify-end">
            <button
              onClick={onClose}
              disabled={submitting}
              className="text-sm text-text-secondary hover:text-text-primary px-3 py-1.5 rounded"
            >
              Cancelar
            </button>
            <button
              onClick={handlePromote}
              disabled={submitting || previewLoading}
              className="flex items-center gap-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 disabled:opacity-50 text-cyan-300 border border-cyan-500/30 px-4 py-1.5 rounded transition-colors"
            >
              {submitting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <ArrowUpCircle className="w-3.5 h-3.5" />}
              Promover {items.length > 1 ? `(${items.length})` : ''}
            </button>
          </div>
        </div>
      </div>
    </dialog>
  )
}
