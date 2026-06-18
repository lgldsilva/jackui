import { useEffect, useState } from 'react'
import { FolderInput, Loader2, Folder, ChevronRight, Home, HardDrive, AlertCircle, CheckCircle2, FolderPlus } from 'lucide-react'
import { LocalEntry, LocalMount, localList, localMounts, localMove } from '../api/client'
import { Sheet } from './Sheet'
import { useTrackedJobs } from '../lib/transfers'
import FileProgressBar from './FileProgressBar'

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
  const [newFolder, setNewFolder] = useState('') // subpasta a criar no destino (opcional)
  const [dirs, setDirs] = useState<LocalEntry[]>([])
  const [dirsLoading, setDirsLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)
  const { start: startTracking, jobs: moveJobs, reset: resetTracking } = useTrackedJobs('local-move')

  // Load available mounts on open
  useEffect(() => {
    if (!active) return
    setDone(false)
    setError('')
    resetTracking()
    setBrowsePath('')
    setNewFolder('')
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
  // Subpasta nova (opcional) anexada ao destino. O backend (localMove) faz
  // MkdirAll no destino, então a pasta é criada na hora de mover — sem endpoint
  // extra. Aceita aninhado (a/b) e ignora barras nas pontas.
  const cleanNew = newFolder.trim().replaceAll(/^\/+|\/+$/g, '')
  const finalPath = cleanNew ? (browsePath ? `${browsePath}/${cleanNew}` : cleanNew) : browsePath

  const handleMove = async () => {
    if (!dstMount) return
    setSubmitting(true)
    setError('')
    startTracking() // snapshot + bump: passa a acompanhar os jobs deste move
    try {
      // allSettled: um item que falha na validação (ex: colisão de nome) não
      // aborta os outros. Cada move aceito roda em background (202) e reporta ao
      // painel de Transferências; aqui só validamos o aceite.
      const results = await Promise.allSettled(items.map(it => localMove(mount, it.path, dstMount, finalPath)))
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
        <div className="-mx-4 -mt-4 px-4 py-2.5 border-b border-default bg-surface/40">
          {isBatch ? (
            <p className="text-xs text-text-secondary">
              De: <span className="text-text-primary font-medium">{items.length} itens</span> em <span className="text-text-primary font-mono">{mount}</span>
            </p>
          ) : (
            <p className="text-xs text-text-secondary truncate" title={singlePath}>
              De: <span className="text-text-primary font-mono">{mount} / {singlePath}</span>
            </p>
          )}
        </div>

        {done ? (
          <div className="flex-1 flex flex-col items-center justify-center gap-4 py-8 px-6">
            <CheckCircle2 className="w-10 h-10 text-green-400" />
            <p className="text-base font-semibold text-text-primary">Movimentação iniciada</p>
            <p className="text-sm text-text-secondary font-mono truncate max-w-xs">
              {dstMount} / {finalPath || ''}
            </p>
            {moveJobs.length > 0 ? (
              <div className="w-full max-w-sm flex flex-col gap-3 mt-1">
                {moveJobs.map(j => (
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
              <p className="text-xs text-text-muted text-center max-w-xs">
                Acompanhe o progresso no painel de Transferências (canto inferior). A lista atualiza ao concluir.
              </p>
            )}
            <button
              onClick={onClose}
              className="mt-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-5 py-2 rounded transition-colors"
            >
              Fechar
            </button>
          </div>
        ) : (
          <>
            {/* Mount selector */}
            <div className="-mx-4 px-4 py-2 border-b border-default flex items-center gap-2 flex-wrap text-sm">
              <HardDrive className="w-4 h-4 text-text-muted flex-shrink-0" />
              {mounts.map(m => (
                <button
                  key={m.name}
                  onClick={() => { setDstMount(m.name); setBrowsePath('') }}
                  className={`px-3 py-1 rounded-full text-xs font-medium transition-colors ${
                    dstMount === m.name
                      ? 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30'
                      : 'bg-surface-tertiary text-text-secondary border border-strong hover:bg-surface-tertiary'
                  }`}
                >
                  {m.name}
                </button>
              ))}
            </div>

            {/* Breadcrumb */}
            <div className="-mx-4 px-4 py-2 border-b border-default flex items-center gap-1 flex-wrap text-sm text-text-primary">
              <button
                onClick={() => setBrowsePath('')}
                className={`flex items-center gap-1 px-2 py-0.5 rounded ${browsePath === '' ? 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300' : 'hover:bg-surface-tertiary'}`}
              >
                <Home className="w-3.5 h-3.5" /> {dstMount || '—'}
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
            <div className="min-h-[150px] py-3">
              {(() => {
                if (dirsLoading) return <div className="flex items-center justify-center py-8 text-text-muted"><Loader2 className="w-5 h-5 animate-spin" /></div>
                if (dirs.length === 0) return <p className="text-sm text-text-muted text-center py-6">Sem subpastas — mover aqui na raiz.</p>
                return <ul className="space-y-0.5">{dirs.map(d => (
                  <li key={d.name}>
                    <button onClick={() => setBrowsePath(browsePath ? `${browsePath}/${d.name}` : d.name)}
                      className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-text-primary hover:bg-surface-tertiary/60 transition-colors">
                      <Folder className="w-4 h-4 text-cyan-400 flex-shrink-0" />
                      <span className="truncate text-left flex-1 min-w-0">{d.name}</span>
                      <ChevronRight className="w-4 h-4 text-text-muted" />
                    </button>
                  </li>
                ))}</ul>
              })()}
            </div>

            {/* Footer */}
            <div className="-mx-4 -mb-4 mt-2 border-t border-default p-4 flex flex-col gap-3 bg-surface/40">
              {/* Criar subpasta no destino atual (criada ao mover). */}
              <label className="flex items-center gap-2 text-sm text-text-primary">
                <FolderPlus className="w-4 h-4 text-text-muted flex-shrink-0" />
                <input
                  type="text"
                  value={newFolder}
                  onChange={e => setNewFolder(e.target.value)}
                  placeholder="Nova subpasta (opcional)"
                  className="flex-1 min-w-0 bg-surface-tertiary border border-strong rounded px-3 py-1.5 text-sm focus:outline-none focus:border-cyan-500"
                />
              </label>

              <div className="text-xs text-text-muted">
                Destino: <span className="text-text-primary font-mono">{dstMount}/{finalPath || ''}</span>
              </div>

              {isSameLoc && !cleanNew && (
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
                <button onClick={onClose} disabled={submitting} className="text-sm text-text-secondary hover:text-text-primary px-3 py-1.5 rounded">
                  Cancelar
                </button>
                <button
                  onClick={handleMove}
                  disabled={submitting || !dstMount || (isSameLoc && !cleanNew)}
                  className="flex items-center gap-2 text-sm bg-cyan-500/20 hover:bg-cyan-500/30 disabled:opacity-50 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-4 py-1.5 rounded transition-colors"
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
