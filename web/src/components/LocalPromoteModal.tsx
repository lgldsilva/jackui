import { useEffect, useState } from 'react'
import { ArrowUpCircle, Folder, Loader2, X, ChevronRight, Plus, FolderOpen, Home, HardDrive } from 'lucide-react'
import { LocalEntry, downloadPromoteBrowse, localPromote, fetchPromoteDestinations, PromoteDestination } from '../api/client'
import { useScrollLock } from '../lib/useScrollLock'

interface Props {
  mount: string
  entry: LocalEntry | null
  onClose: () => void
  onPromoted: () => void
}

/**
 * Navegador de subpastas de SHARED_DIR + seletor de destino + ações de
 * promover para arquivos/pastas locais.
 */
export default function LocalPromoteModal({ mount, entry, onClose, onPromoted }: Props) {
  useScrollLock(!!entry)
  const [dests, setDests] = useState<PromoteDestination[]>([])
  const [selectedBase, setSelectedBase] = useState('')
  const [path, setPath] = useState('')
  const [dirs, setDirs] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [newFolder, setNewFolder] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (!entry) return
    fetchPromoteDestinations().then(setDests).catch(() => {})
  }, [entry])

  useEffect(() => {
    if (!entry) return
    setLoading(true)
    setError('')
    downloadPromoteBrowse(path, selectedBase || undefined)
      .then(r => setDirs(r.dirs))
      .catch(e => setError(e?.response?.data?.error || e.message || 'Erro listando subpastas'))
      .finally(() => setLoading(false))
  }, [path, entry, selectedBase])

  useEffect(() => {
    if (entry) {
      setSelectedBase('')
      setPath('')
      setNewFolder('')
      setError('')
    }
  }, [entry])

  if (!entry) return null

  const currentDest = dests.find(d => d.path === selectedBase) || dests[0]
  const destLabel = currentDest?.name || 'Biblioteca'

  const finalTarget = newFolder.trim()
    ? (path ? `${path}/${newFolder.trim()}` : newFolder.trim())
    : path

  const handlePromote = async () => {
    setSubmitting(true)
    try {
      await localPromote(mount, entry.path, finalTarget, selectedBase || undefined)
      onPromoted()
      onClose()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || 'Erro ao promover arquivo')
    } finally {
      setSubmitting(false)
    }
  }

  const breadcrumb = path.split('/').filter(Boolean)

  return (
    <div
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4"
      onClick={e => e.target === e.currentTarget && onClose()}
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-lg shadow-2xl max-h-[90vh] flex flex-col">
        <header className="flex items-center justify-between p-4 border-b border-gray-700">
          <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2">
            <ArrowUpCircle className="w-5 h-5 text-cyan-400" />
            Promover {entry.isDir ? 'pasta' : 'arquivo'} local
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-100">
            <X className="w-5 h-5" />
          </button>
        </header>

        <div className="px-4 py-2.5 border-b border-gray-700 bg-gray-900/40">
          <p className="text-xs text-gray-400 truncate" title={entry.name}>
            Origem: <span className="text-gray-300 font-mono">{entry.name}</span>
          </p>
        </div>

        {/* Seletor de destino */}
        {dests.length > 1 && (
          <div className="px-4 py-2 border-b border-gray-700 flex items-center gap-2 flex-wrap text-sm">
            <HardDrive className="w-4 h-4 text-gray-500 flex-shrink-0" />
            {dests.map(d => (
              <button
                key={d.path}
                onClick={() => { setSelectedBase(d.path); setPath('') }}
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

        <div className="px-4 py-2 border-b border-gray-700 flex items-center gap-1 flex-wrap text-sm text-gray-300">
          <button
            onClick={() => setPath('')}
            className={`flex items-center gap-1 px-2 py-0.5 rounded ${path === '' ? 'bg-cyan-500/20 text-cyan-300' : 'hover:bg-gray-700'}`}
          >
            <Home className="w-3.5 h-3.5" /> {destLabel}
          </button>
          {breadcrumb.map((seg, i) => (
            <span key={i} className="flex items-center gap-1">
              <ChevronRight className="w-3 h-3 text-gray-600" />
              <button
                onClick={() => setPath(breadcrumb.slice(0, i + 1).join('/'))}
                className={`px-2 py-0.5 rounded ${i === breadcrumb.length - 1 ? 'bg-cyan-500/20 text-cyan-300' : 'hover:bg-gray-700'}`}
              >
                {seg}
              </button>
            </span>
          ))}
        </div>

        <div className="flex-1 overflow-y-auto p-4 min-h-[150px]">
          {loading ? (
            <div className="flex items-center justify-center py-8 text-gray-500">
              <Loader2 className="w-5 h-5 animate-spin" />
            </div>
          ) : dirs.length === 0 ? (
            <p className="text-sm text-gray-500 text-center py-4">Sem subpastas aqui. Crie uma abaixo ou promova nesta raiz.</p>
          ) : (
            <ul className="space-y-1">
              {dirs.map(d => (
                <li key={d}>
                  <button
                    onClick={() => setPath(path ? `${path}/${d}` : d)}
                    className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-gray-200 hover:bg-gray-700/60 transition-colors"
                  >
                    <Folder className="w-4 h-4 text-cyan-400 flex-shrink-0" />
                    <span className="truncate text-left flex-1">{d}</span>
                    <ChevronRight className="w-4 h-4 text-gray-600" />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

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
            <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded px-2 py-1.5">{error}</p>
          )}

          <div className="flex items-center gap-2 justify-end">
            <button
              onClick={onClose}
              disabled={submitting}
              className="text-sm text-gray-400 hover:text-gray-200 px-3 py-1.5 rounded"
            >
              Cancelar
            </button>
            <button
              onClick={handlePromote}
              disabled={submitting}
              className="flex items-center gap-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 disabled:opacity-50 text-cyan-300 border border-cyan-500/30 px-4 py-1.5 rounded transition-colors"
            >
              {submitting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <ArrowUpCircle className="w-3.5 h-3.5" />}
              Promover
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
