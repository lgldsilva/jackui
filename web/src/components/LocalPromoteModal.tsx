import { useEffect, useState } from 'react'
import { ArrowUpCircle, Folder, Loader2, ChevronRight, Plus, FolderOpen, Home, HardDrive, Sparkles, ArrowRight } from 'lucide-react'
import { LocalEntry, downloadPromoteBrowse, localPromote, fetchPromoteDestinations, PromoteDestination, localPromotePreview, PromotePreviewEntry } from '../api/client'
import { Sheet } from './Sheet'

type Props = {
  readonly mount: string
  // The files to promote. Empty = closed. One = single promote; many = batch —
  // the destination + "rename via AI" choice is made ONCE and applied to all,
  // in a single backend call (no more one-modal-per-file queue).
  readonly entries: readonly LocalEntry[]
  readonly onClose: () => void
  readonly onPromoted: () => void
}

/**
 * Navegador de subpastas de SHARED_DIR + seletor de destino + ações de
 * promover para arquivos locais — individual OU em lote (uma chamada para N
 * arquivos, destino e renomeação IA escolhidos uma única vez).
 */
export default function LocalPromoteModal({ mount, entries, onClose, onPromoted }: Props) {
  const [dests, setDests] = useState<PromoteDestination[]>([])
  const [selectedBase, setSelectedBase] = useState('')
  const [path, setPath] = useState('')
  const [dirs, setDirs] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [newFolder, setNewFolder] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [renameIA, setRenameIA] = useState(false)
  const [previews, setPreviews] = useState<PromotePreviewEntry[]>([])
  const [previewLoading, setPreviewLoading] = useState(false)

  const open = entries.length > 0
  const primary = entries[0] ?? null
  const count = entries.length
  // Stable identity of the current selection — drives the reset/preview effects
  // without re-firing on every render (entries is a new array each render).
  const batchKey = entries.map(e => e.path).join('|')
  const paths = entries.map(e => e.path)

  const finalTarget = (() => {
    const trimmed = newFolder.trim()
    if (!trimmed) return path
    return path ? `${path}/${trimmed}` : trimmed
  })()

  useEffect(() => {
    if (!open) return
    fetchPromoteDestinations().then(setDests).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [batchKey])

  useEffect(() => {
    if (!open) return
    setLoading(true)
    setError('')
    downloadPromoteBrowse(path, selectedBase || undefined)
      .then(r => setDirs(r.dirs))
      .catch(e => setError(e?.response?.data?.error || e.message || 'Erro listando subpastas'))
      .finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, batchKey, selectedBase])

  // AI rename preview — for ALL selected files at once. Cancels in flight when
  // the selection/target changes so a slow preview can't overwrite a newer one.
  useEffect(() => {
    if (!open || !renameIA || !primary) {
      setPreviews([])
      return
    }
    let cancelled = false
    setPreviewLoading(true)
    setError('')
    localPromotePreview(mount, primary.path, finalTarget, selectedBase || undefined, paths)
      .then(r => { if (!cancelled) setPreviews(r.previews) })
      .catch(e => { if (!cancelled) setError(e?.response?.data?.error || e.message || 'Erro gerando preview IA') })
      .finally(() => { if (!cancelled) setPreviewLoading(false) })
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [renameIA, batchKey, finalTarget, selectedBase, mount])

  useEffect(() => {
    if (open) {
      setSelectedBase('')
      setPath('')
      setNewFolder('')
      setRenameIA(false)
      setPreviews([])
      setError('')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [batchKey])

  if (!open || !primary) return null

  const currentDest = dests.find(d => d.path === selectedBase) || dests[0]
  const destLabel = currentDest?.name || 'Biblioteca'

  const handlePromote = async () => {
    setSubmitting(true)
    try {
      const r = await localPromote(mount, primary.path, finalTarget, selectedBase || undefined, renameIA, paths)
      if (r.failed > 0) {
        // Partial success: keep the modal open with a summary; refresh so the
        // ones that did move disappear from the list.
        setError(`${r.moved} de ${count} movido(s) · ${r.failed} com erro`)
        onPromoted()
      } else {
        onPromoted()
        onClose()
      }
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || 'Erro ao promover')
    } finally {
      setSubmitting(false)
    }
  }

  const breadcrumb = path.split('/').filter(Boolean)

  return (
    <Sheet
      open
      onClose={onClose}
      size="lg"
      title={count > 1 ? `Promover ${count} arquivos locais` : 'Promover arquivo local'}
      icon={<ArrowUpCircle className="w-4 h-4 text-cyan-400 flex-shrink-0" />}
      footer={
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
            {count > 1 ? `Promover ${count}` : 'Promover'}
          </button>
        </div>
      }
    >
      <>
        <div className="-mx-4 -mt-4 px-4 py-2.5 border-b border-default bg-surface/40">
          {count > 1 ? (
            <p className="text-xs text-text-secondary">
              <span className="text-white font-semibold">{count}</span> arquivos selecionados — mesmo destino para todos
            </p>
          ) : (
            <p className="text-xs text-text-secondary truncate" title={primary.name}>
              Origem: <span className="text-text-primary font-mono">{primary.name}</span>
            </p>
          )}
        </div>

        {/* Seletor de destino */}
        {dests.length > 1 && (
          <div className="-mx-4 px-4 py-2 border-b border-default flex items-center gap-2 flex-wrap text-sm">
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

        <div className="-mx-4 px-4 py-2 border-b border-default flex items-center gap-1 flex-wrap text-sm text-text-primary">
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

        <div className="py-4 min-h-[150px]">
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
                Visualização do Destino Organizado{count > 1 ? ` — ${count} arquivos` : ''}:
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
        </div>
      </>
    </Sheet>
  )
}
