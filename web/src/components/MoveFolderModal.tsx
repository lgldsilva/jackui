import { useEffect, useState } from 'react'
import { FolderInput, Loader2, Folder, ChevronRight, Home, HardDrive, AlertCircle, CheckCircle2 } from 'lucide-react'
import { LocalEntry, LocalMount, localList, localMounts, localMove } from '../api/client'
import { Sheet } from './Sheet'

type Props = {
  readonly mount: string
  readonly entry: LocalEntry | null
  /** Modo lote: quando preenchido (e não-vazio), move todos os itens de uma vez. */
  readonly entries?: readonly LocalEntry[]
  readonly onClose: () => void
  readonly onMoved: () => void
}

export default function MoveFolderModal({ mount, entry, entries, onClose, onMoved }: Props) {
  // Unifica os dois modos: lista de itens a mover (1 no modo single, N no lote).
  let items: readonly LocalEntry[] = []
  if (entries && entries.length > 0) items = entries
  else if (entry) items = [entry]
  const active = items.length > 0

  const [mounts, setMounts] = useState<LocalMount[]>([])
  const [dstMount, setDstMount] = useState('')
  const [browsePath, setBrowsePath] = useState('')
  const [dirs, setDirs] = useState<LocalEntry[]>([])
  const [dirsLoading, setDirsLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)

  // Load available mounts on open
  useEffect(() => {
    if (!active) return
    setDone(false)
    setError('')
    setBrowsePath('')
    localMounts().then(ms => {
      setMounts(ms)
      // Default to first mount that isn't the source
      const other = ms.find(m => m.name !== mount) || ms[0]
      setDstMount(other?.name || '')
    }).catch(() => {})
  }, [active, mount])

  // Browse directories in selected mount
  useEffect(() => {
    if (!dstMount || !active) return
    setDirsLoading(true)
    localList(dstMount, browsePath)
      .then(entries => setDirs(entries.filter(e => e.isDir)))
      .catch(() => setDirs([]))
      .finally(() => setDirsLoading(false))
  }, [dstMount, browsePath, active])

  if (!active) return null

  const isBatch = items.length > 1
  const breadcrumb = browsePath.split('/').filter(Boolean)

  const handleMove = async () => {
    if (!dstMount) return
    setSubmitting(true)
    setError('')
    try {
      // allSettled: um item que falha (ex: colisão de nome) não aborta os outros.
      const results = await Promise.allSettled(items.map(it => localMove(mount, it.path, dstMount, browsePath)))
      const failed = results.filter((r): r is PromiseRejectedResult => r.status === 'rejected')
      if (failed.length === items.length) {
        const first = failed[0]
        setError(first.reason?.response?.data?.error || first.reason?.message || 'Erro ao mover')
        return
      }
      if (failed.length > 0) setError(`${failed.length} de ${items.length} itens não puderam ser movidos.`)
      setDone(true)
      onMoved()
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message || 'Erro ao mover')
    } finally {
      setSubmitting(false)
    }
  }

  // No lote, não há uma única "localização atual"; deixa o backend validar cada item.
  const singlePath = items[0].path
  const isSameLoc = !isBatch && dstMount === mount &&
    browsePath === (singlePath.includes('/') ? singlePath.slice(0, singlePath.lastIndexOf('/')) : '')

  return (
    <Sheet
      open
      onClose={onClose}
      size="lg"
      title="Mover para…"
      icon={<FolderInput className="w-4 h-4 text-cyan-400 flex-shrink-0" />}
    >
      <>
        {/* Source */}
        <div className="-mx-4 -mt-4 px-4 py-2.5 border-b border-gray-700 bg-gray-900/40">
          {isBatch ? (
            <p className="text-xs text-gray-400">
              De: <span className="text-gray-300 font-medium">{items.length} itens</span> em <span className="text-gray-300 font-mono">{mount}</span>
            </p>
          ) : (
            <p className="text-xs text-gray-400 truncate" title={singlePath}>
              De: <span className="text-gray-300 font-mono">{mount} / {singlePath}</span>
            </p>
          )}
        </div>

        {done ? (
          <div className="flex-1 flex flex-col items-center justify-center gap-4 py-10 px-6">
            <CheckCircle2 className="w-10 h-10 text-green-400" />
            <p className="text-base font-semibold text-gray-100">Movido com sucesso</p>
            <p className="text-sm text-gray-400 font-mono truncate max-w-xs">
              {dstMount} / {browsePath || ''}
            </p>
            <button
              onClick={onClose}
              className="mt-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-300 border border-cyan-500/30 px-5 py-2 rounded transition-colors"
            >
              Fechar
            </button>
          </div>
        ) : (
          <>
            {/* Mount selector */}
            <div className="-mx-4 px-4 py-2 border-b border-gray-700 flex items-center gap-2 flex-wrap text-sm">
              <HardDrive className="w-4 h-4 text-gray-500 flex-shrink-0" />
              {mounts.map(m => (
                <button
                  key={m.name}
                  onClick={() => { setDstMount(m.name); setBrowsePath('') }}
                  className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
                    dstMount === m.name
                      ? 'bg-cyan-500/20 text-cyan-300 border border-cyan-500/30'
                      : 'bg-gray-700 text-gray-400 border border-gray-600 hover:bg-gray-600'
                  }`}
                >
                  {m.name}
                </button>
              ))}
            </div>

            {/* Breadcrumb */}
            <div className="-mx-4 px-4 py-2 border-b border-gray-700 flex items-center gap-1 flex-wrap text-sm text-gray-300">
              <button
                onClick={() => setBrowsePath('')}
                className={`flex items-center gap-1 px-2 py-0.5 rounded ${browsePath === '' ? 'bg-cyan-500/20 text-cyan-300' : 'hover:bg-gray-700'}`}
              >
                <Home className="w-3.5 h-3.5" /> {dstMount || '—'}
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
            <div className="min-h-[150px] py-3">
              {(() => {
                if (dirsLoading) return <div className="flex items-center justify-center py-8 text-gray-500"><Loader2 className="w-5 h-5 animate-spin" /></div>
                if (dirs.length === 0) return <p className="text-sm text-gray-500 text-center py-6">Sem subpastas — mover aqui na raiz.</p>
                return <ul className="space-y-0.5">{dirs.map(d => (
                  <li key={d.name}>
                    <button onClick={() => setBrowsePath(browsePath ? `${browsePath}/${d.name}` : d.name)}
                      className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-gray-200 hover:bg-gray-700/60 transition-colors">
                      <Folder className="w-4 h-4 text-cyan-400 flex-shrink-0" />
                      <span className="truncate text-left flex-1 min-w-0">{d.name}</span>
                      <ChevronRight className="w-4 h-4 text-gray-600" />
                    </button>
                  </li>
                ))}</ul>
              })()}
            </div>

            {/* Footer */}
            <div className="-mx-4 -mb-4 mt-2 border-t border-gray-700 p-4 flex flex-col gap-3 bg-gray-900/40">
              <div className="text-xs text-gray-500">
                Destino: <span className="text-gray-300 font-mono">{dstMount}/{browsePath || ''}</span>
              </div>

              {isSameLoc && (
                <p className="text-xs text-amber-400 bg-amber-500/10 border border-amber-500/20 rounded px-2 py-1.5">
                  Destino é igual à localização atual — escolha outra pasta.
                </p>
              )}

              {error && (
                <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded px-2 py-1.5 flex items-center gap-1">
                  <AlertCircle className="w-3.5 h-3.5 flex-shrink-0" />{error}
                </p>
              )}

              <div className="flex items-center gap-2 justify-end">
                <button onClick={onClose} disabled={submitting} className="text-sm text-gray-400 hover:text-gray-200 px-3 py-1.5 rounded">
                  Cancelar
                </button>
                <button
                  onClick={handleMove}
                  disabled={submitting || !dstMount || isSameLoc}
                  className="flex items-center gap-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 disabled:opacity-50 text-cyan-300 border border-cyan-500/30 px-4 py-1.5 rounded transition-colors"
                >
                  {submitting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <FolderInput className="w-3.5 h-3.5" />}
                  Mover aqui
                </button>
              </div>
            </div>
          </>
        )}
      </>
    </Sheet>
  )
}
