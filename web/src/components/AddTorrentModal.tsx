import { useState, useRef, DragEvent, useEffect } from 'react'
import {
  UploadCloud, Link2, Loader2, AlertCircle, Trash2,
  ChevronDown, ChevronUp, CheckCircle2, Server, Clock,
  FileVideo, FileAudio, FileText
} from 'lucide-react'
import {
  streamAdd, streamAddTorrentFile, streamMetadata,
  downloadCreate, downloadTorrent, getClients,
  SearchResult, StreamFile, DownloadClient
} from '../api/client'
import { Sheet } from './Sheet'
import { load, save, pushMRU } from '../lib/storage'
import { formatBytes } from '../lib/format'
import { uid } from '../lib/uid'

type Props = {
  readonly isOpen: boolean
  readonly onClose: () => void
  readonly onAdded: (result: SearchResult) => void
  readonly preloadFiles?: File[] | null
}


type TorrentItem = {
  readonly id: string
  readonly name: string
  readonly file?: File
  readonly magnet?: string
  readonly infoHash?: string
  readonly loading: boolean
  readonly error?: string
  readonly totalSize?: number
  readonly files?: StreamFile[]
  readonly selectedFiles: Set<number>
  readonly expanded?: boolean
}

const KEY_CLIENT = 'lastClientId'
const KEY_PATH = 'lastSavePath'
const KEY_RECENT_PATHS = 'recentSavePaths'
const INTERNAL_ID = '__internal__'

const DEFAULT_MIN_BYTES = 10 * 1024 * 1024
function defaultSelected(files: StreamFile[]): Set<number> {
  const sel = new Set<number>()
  for (const f of files) {
    if (f.isVideo || f.size >= DEFAULT_MIN_BYTES) sel.add(f.index)
  }
  if (sel.size === 0 && files.length > 0) {
    let biggest = files[0]
    for (const f of files) if (f.size > biggest.size) biggest = f
    sel.add(biggest.index)
  }
  return sel
}

function fileIcon(f: StreamFile) {
  if (f.isVideo) return <FileVideo className="w-4 h-4 text-green-400 flex-shrink-0" />
  if (/\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i.test(f.path)) {
    return <FileAudio className="w-4 h-4 text-purple-400 flex-shrink-0" />
  }
  return <FileText className="w-4 h-4 text-text-muted flex-shrink-0" />
}

function renderItemStatus(item: TorrentItem) {
  if (item.loading) {
    return <span className="text-xs text-text-muted flex items-center gap-1.5">
      <Loader2 className="w-3 h-3 animate-spin text-cyan-400" />
      Buscando metadados...
    </span>
  }
  if (item.error) {
    return <span className="text-xs text-red-400 flex items-center gap-1.5">
      <AlertCircle className="w-3.5 h-3.5" />
      {item.error}
    </span>
  }
  return <span className="text-xs text-text-secondary flex items-center gap-1.5">
    <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" />
    {formatBytes(item.totalSize || 0)}
    {item.files && <>
      <span className="text-text-muted">•</span>
      <span>{item.files.length} arquivos ({item.selectedFiles.size} selecionados)</span>
    </>}
  </span>
}

async function confirmDownloads(
  readyItems: TorrentItem[],
  selectedClientId: string,
  savePath: string,
): Promise<number> {
  let successCount = 0
  for (const item of readyItems) {
    const infoHash = item.infoHash
    const magnet = item.magnet || (infoHash ? `magnet:?xt=urn:btih:${infoHash}` : '')
    if (!infoHash || !magnet) continue
    if (selectedClientId === INTERNAL_ID) {
      if ((item.files?.length ?? 0) > 0) {
        const picks = (item.files ?? []).filter(f => item.selectedFiles.has(f.index))
        await Promise.all(picks.map(f => downloadCreate({ infoHash, fileIndex: f.index, magnet, name: item.name, filePath: f.path, fileSize: f.size })))
      } else {
        await downloadCreate({ infoHash, fileIndex: 0, magnet, name: item.name, filePath: '', fileSize: 0 })
      }
    } else {
      await downloadTorrent(selectedClientId, magnet, '', savePath || undefined)
    }
    successCount++
  }
  return successCount
}

function notifyAdded(readyItems: TorrentItem[], onAdded: (r: SearchResult) => void, onClose: () => void) {
  if (readyItems.length === 1) {
    const first = readyItems[0]
    onAdded({ title: first.name, tracker: '', categoryId: 0, category: '', size: first.totalSize || 0, seeders: 0, leechers: 0, age: '', magnetUri: first.magnet || `magnet:?xt=urn:btih:${first.infoHash}`, link: '', infoHash: first.infoHash || '', publishDate: '' })
  } else {
    setTimeout(() => onClose(), 1200)
  }
}

export default function AddTorrentModal({ isOpen, onClose, onAdded, preloadFiles }: Props) {
  const [view, setView] = useState<'drop_paste' | 'configure'>('drop_paste')
  const [magnets, setMagnets] = useState('')
  const [items, setItems] = useState<TorrentItem[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState(false)
  const [dragActive, setDragActive] = useState(false)
  
  // Client selection states
  const [clients, setClients] = useState<DownloadClient[]>([])
  const [selectedClientId, setSelectedClientId] = useState('')
  const [savePath, setSavePath] = useState('')
  const [recentPaths, setRecentPaths] = useState<string[]>([])
  const [showRecent, setShowRecent] = useState(false)
  
  const fileInputRef = useRef<HTMLInputElement>(null)
  const pathInputRef = useRef<HTMLInputElement>(null)

  // Load clients and paths when modal opens
  useEffect(() => {
    if (!isOpen) return

    setView('drop_paste')
    setMagnets('')
    setItems([])
    setError('')
    setSuccess(false)
    setRecentPaths(load<string[]>(KEY_RECENT_PATHS, []))
    setSavePath(load<string>(KEY_PATH, ''))

    getClients()
      .then((data) => {
        setClients(data)
        const lastId = load<string>(KEY_CLIENT, '')
        const lastValid = lastId === INTERNAL_ID || data.some((c) => c.id === lastId)
        const fallback = data.find((c) => c.default)?.id || INTERNAL_ID
        setSelectedClientId(lastValid ? lastId : fallback)
      })
      .catch(() => {
        setClients([])
        setSelectedClientId(INTERNAL_ID)
      })

    // Preload files if provided!
    if (preloadFiles && preloadFiles.length > 0) {
      addFilesToResolve(preloadFiles)
    }
  }, [isOpen, preloadFiles])


  if (!isOpen) return null

  const handleDrag = (e: DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    if (e.type === 'dragenter' || e.type === 'dragover') {
      setDragActive(true)
    } else if (e.type === 'dragleave') {
      setDragActive(false)
    }
  }

  const resolveTorrentItem = async (item: TorrentItem) => {
    try {
      let info
      if (item.file) {
        info = await streamAddTorrentFile(item.file)
      } else if (item.magnet) {
        info = await streamAdd(item.magnet)
      } else {
        return
      }

      let filesList = info.files || []
      if (filesList.length === 0 && info.infoHash) {
        const metadata = await streamMetadata(info.infoHash)
        if (metadata?.files) {
          filesList = metadata.files
        }
      }

      setItems(prev => prev.map(p => p.id === item.id ? {
        ...p,
        name: info.name || p.name,
        infoHash: info.infoHash,
        totalSize: info.totalSize,
        files: filesList,
        selectedFiles: defaultSelected(filesList),
        loading: false
      } : p))

    } catch (err: any) {
      setItems(prev => prev.map(p => p.id === item.id ? {
        ...p,
        loading: false,
        error: err.message || 'Falha ao resolver metadados'
      } : p))
    }
  }

  const addFilesToResolve = (files: File[]) => {
    setView('configure')
    const newItems: TorrentItem[] = files.map(f => ({
      id: uid(),
      name: f.name,
      file: f,
      loading: true,
      selectedFiles: new Set()
    }))

    setItems(prev => [...prev, ...newItems])
    newItems.forEach(item => {
      void resolveTorrentItem(item)
    })
  }

  const handleDrop = async (e: DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setDragActive(false)

    if (e.dataTransfer?.files?.length > 0) {
      const files = Array.from(e.dataTransfer.files)
      const torrentFiles = files.filter(f => f.name.endsWith('.torrent'))
      if (torrentFiles.length > 0) {
        addFilesToResolve(torrentFiles)
      } else {
        setError('Por favor, arraste apenas arquivos com a extensão .torrent')
      }
    }
  }

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      addFilesToResolve(Array.from(e.target.files))
    }
  }

  const handleAddMagnets = () => {
    const lines = magnets
      .split('\n')
      .map(l => l.trim())
      .filter(l => l.length > 0)

    if (lines.length === 0) {
      setError('Por favor, insira pelo menos um link Magnet.')
      return
    }

    setView('configure')
    const newItems: TorrentItem[] = lines.map(line => {
      // Tenta extrair um hash ou nome amigável do magnet
      const btihMatch = /btih:([a-f0-9]{40})/i.exec(line)
      const nameMatch = /dn=([^&]+)/i.exec(line)
      const hash = btihMatch ? btihMatch[1].toLowerCase() : ''
      const name = nameMatch ? decodeURIComponent(nameMatch[1].replaceAll('+', ' ')) : `Magnet (hash: ${hash || '?'})`

      return {
        id: uid(),
        name,
        magnet: line,
        infoHash: hash || undefined,
        loading: true,
        selectedFiles: new Set()
      }
    })

    setItems(prev => [...prev, ...newItems])
    newItems.forEach(item => {
      void resolveTorrentItem(item)
    })
  }

  const handleRemoveItem = (id: string) => {
    setItems(prev => prev.filter(item => item.id !== id))
  }

  const toggleExpandItem = (id: string) => {
    setItems(prev => prev.map(item => item.id === id ? { ...item, expanded: !item.expanded } : item))
  }

  const handleFileToggle = (itemId: string, fileIndex: number) => {
    setItems(prev => prev.map(item => {
      if (item.id !== itemId) return item
      const nextSelected = new Set(item.selectedFiles)
      if (nextSelected.has(fileIndex)) {
        nextSelected.delete(fileIndex)
      } else {
        nextSelected.add(fileIndex)
      }
      return { ...item, selectedFiles: nextSelected }
    }))
  }

  const handleSelectAllFiles = (itemId: string, allIndices: number[]) => {
    setItems(prev => prev.map(item => {
      if (item.id !== itemId) return item
      return { ...item, selectedFiles: new Set(allIndices) }
    }))
  }

  const handleSelectNoFiles = (itemId: string) => {
    setItems(prev => prev.map(item => {
      if (item.id !== itemId) return item
      return { ...item, selectedFiles: new Set() }
    }))
  }

  const handleConfirmDownloads = async () => {
    const readyItems = items.filter(item => !item.loading && !item.error)
    if (readyItems.length === 0) {
      setError('Nenhum torrent válido para baixar.')
      return
    }
    if (selectedClientId === INTERNAL_ID && readyItems.some(item => (item.files?.length ?? 0) > 0 && item.selectedFiles.size === 0)) {
      setError('Por favor, selecione ao menos um arquivo para cada torrent ou remova-o da lista.')
      return
    }

    setLoading(true)
    setError('')
    try {
      await confirmDownloads(readyItems, selectedClientId, savePath)
      save(KEY_CLIENT, selectedClientId)
      if (savePath.trim()) { save(KEY_PATH, savePath.trim()); pushMRU(KEY_RECENT_PATHS, savePath.trim()) }
      setSuccess(true)
      notifyAdded(readyItems, onAdded, onClose)
    } catch (err: any) {
      setError(err.message || 'Erro ao iniciar os downloads.')
    } finally {
      setLoading(false)
    }
  }

  const pickRecentPath = (p: string) => {
    setSavePath(p)
    setShowRecent(false)
    pathInputRef.current?.focus()
  }

  return (
    <Sheet
      open
      onClose={onClose}
      size="xl"
      title={view === 'drop_paste' ? 'Adicionar Novo Torrent / Magnet' : `Configurar Downloads (${items.length})`}
      icon={<Link2 className="w-4 h-4 text-cyan-400 flex-shrink-0" />}
      footer={
        <div className="flex items-center justify-between">
          <div>
            {view === 'configure' && (
              <button
                onClick={() => setView('drop_paste')}
                className="px-4 py-2 rounded-xl text-sm font-medium text-text-secondary hover:text-text-primary transition-colors"
              >
                Voltar
              </button>
            )}
          </div>

          <div className="flex items-center gap-3">
            <button
              onClick={onClose}
              disabled={loading}
              className="px-4 py-2 rounded-xl text-sm font-medium text-text-secondary hover:text-text-primary transition-colors"
            >
              Fechar
            </button>

            {view === 'drop_paste' ? (
              <button
                onClick={handleAddMagnets}
                disabled={magnets.trim().length === 0}
                className="flex items-center gap-1.5 bg-cyan-500 hover:bg-cyan-600 disabled:opacity-50 text-white px-5 py-2 rounded-xl text-sm font-semibold transition-all duration-200 shadow-lg shadow-cyan-500/10"
              >
                Configurar Magnet
              </button>
            ) : (
              <button
                onClick={handleConfirmDownloads}
                disabled={loading || items.filter(i => !i.loading && !i.error).length === 0 || success}
                className="flex items-center gap-1.5 bg-emerald-500 hover:bg-emerald-600 disabled:opacity-50 text-white px-5 py-2 rounded-xl text-sm font-semibold transition-all duration-200 shadow-lg shadow-emerald-500/10"
              >
                {loading ? (
                  <>
                    <Loader2 className="w-4 h-4 animate-spin" />
                    Iniciando downloads...
                  </>
                ) : (
                  'Confirmar Downloads'
                )}
              </button>
            )}
          </div>
        </div>
      }
    >
      <div className="space-y-4">
          {error && (
            <div className="flex items-start gap-2 bg-red-500/10 border border-red-500/30 text-red-400 rounded-lg px-3 py-2.5 text-xs">
              <AlertCircle className="w-4 h-4 mt-0.5 flex-shrink-0" />
              <span>{error}</span>
            </div>
          )}

          {success && (
            <div className="flex items-start gap-2 bg-green-500/10 border border-green-500/30 text-green-400 rounded-lg px-3 py-2.5 text-xs">
              <CheckCircle2 className="w-4 h-4 mt-0.5 flex-shrink-0" />
              <span>Downloads enfileirados com sucesso!</span>
            </div>
          )}

          {view === 'drop_paste' ? (
            <>
              {/* Drag & Drop Area */}
              <button
                type="button"
                onDragEnter={handleDrag}
                onDragOver={handleDrag}
                onDragLeave={handleDrag}
                onDrop={handleDrop}
                onClick={() => fileInputRef.current?.click()}
                onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') fileInputRef.current?.click() }}
                className={`
                  w-full border-2 border-dashed rounded-xl p-8 flex flex-col items-center justify-center gap-3 cursor-pointer transition-all duration-200
                  ${dragActive
                    ? 'border-cyan-400 bg-cyan-500/5' 
                    : 'border-default hover:border-strong bg-surface/40 hover:bg-surface/60'
                  }
                `}
              >
                <input
                  ref={fileInputRef}
                  type="file"
                  accept=".torrent"
                  multiple
                  className="hidden"
                  onChange={handleFileChange}
                />
                <UploadCloud className={`w-12 h-12 ${dragActive ? 'text-cyan-400 animate-bounce' : 'text-text-muted'}`} />
                <div className="text-center">
                  <p className="text-sm font-medium text-text-primary">Arraste e solte um ou mais arquivos .torrent aqui</p>
                  <p className="text-xs text-text-muted mt-1">ou clique para navegar no seu computador</p>
                </div>
              </button>

              <div className="relative flex py-2 items-center">
                <div className="flex-grow border-t border-default"></div>
                <span className="flex-shrink mx-4 text-text-muted text-xs uppercase tracking-wider font-semibold">ou cole links magnet</span>
                <div className="flex-grow border-t border-default"></div>
              </div>

              {/* Paste Magnets Area */}
              <div className="space-y-2">
                <textarea
                  value={magnets}
                  onChange={e => setMagnets(e.target.value)}
                  placeholder="Cole os links Magnet aqui (um por linha)&#10;ex: magnet:?xt=urn:btih:..."
                  className="w-full h-36 bg-surface border border-default rounded-xl px-3.5 py-2.5 text-sm text-text-primary placeholder-gray-600 focus:outline-none focus:border-cyan-500 transition-colors font-mono resize-none"
                />
                <p className="text-[10px] text-text-muted">
                  Suporta múltiplos links Magnet (um em cada linha). O sistema carregará os metadados de cada um para você escolher o que baixar.
                </p>
              </div>
            </>
          ) : (
            // Configure list stage
            <div className="space-y-4">
              {/* Destination inputs */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 bg-surface/60 p-4 rounded-xl border border-default/50">
                <div>
                  <label htmlFor="download-dest" className="block text-xs font-medium text-text-secondary mb-1.5 uppercase tracking-wider">
                    Destino do download
                  </label>
                  <select
                    id="download-dest"
                    value={selectedClientId}
                    onChange={(e) => setSelectedClientId(e.target.value)}
                    className="w-full bg-surface border border-default rounded-lg px-3 py-2 text-text-primary text-sm focus:outline-none focus:border-cyan-500 transition-colors cursor-pointer"
                  >
                    <option value={INTERNAL_ID}>JackUI (servidor — assistir aqui)</option>
                    {clients.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name} ({c.type})
                      </option>
                    ))}
                  </select>
                  {selectedClientId === INTERNAL_ID && (
                    <p className="text-[10px] text-text-muted mt-1 flex items-center gap-1">
                      <Server className="w-3 h-3 flex-shrink-0" />
                      Baixa no servidor e aparece em Downloads.
                    </p>
                  )}

                </div>

                <div className={`relative ${selectedClientId === INTERNAL_ID ? 'opacity-50 pointer-events-none' : ''}`}>
                  <label htmlFor="save-path" className="block text-xs font-medium text-text-secondary mb-1.5 uppercase tracking-wider">
                    Pasta de Destino <span className="text-text-muted font-normal">(opcional)</span>
                  </label>
                  <div className="relative">
                    <input
                      id="save-path"
                      ref={pathInputRef}
                      type="text"
                      value={savePath}
                      disabled={selectedClientId === INTERNAL_ID}
                      onChange={(e) => setSavePath(e.target.value)}
                      onFocus={() => setShowRecent(recentPaths.length > 0)}
                      onBlur={() => setTimeout(() => setShowRecent(false), 150)}
                      placeholder="/downloads/filmes"
                      className="w-full bg-surface border border-default rounded-lg px-3 py-2 pr-10 text-text-primary text-sm focus:outline-none focus:border-cyan-500 transition-colors"
                    />
                    {recentPaths.length > 0 && selectedClientId !== INTERNAL_ID && (
                      <button
                        type="button"
                        onMouseDown={(e) => { e.preventDefault(); setShowRecent(s => !s) }}
                        className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"
                        title="Pastas recentes"
                      >
                        <Clock className="w-4 h-4" />
                      </button>
                    )}
                  </div>

                  {showRecent && recentPaths.length > 0 && (
                    <div className="absolute z-10 left-0 right-0 mt-1 bg-surface border border-default rounded-lg shadow-xl max-h-48 overflow-y-auto">
                      {recentPaths.map((p) => (
                        <button
                          key={p}
                          type="button"
                          onMouseDown={(e) => { e.preventDefault(); pickRecentPath(p) }}
                          className="w-full text-left px-3 py-2 text-sm text-text-primary hover:bg-surface-secondary transition-colors truncate"
                          title={p}
                        >
                          {p}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              </div>

              {/* Resolved Torrents List */}
              <div className="space-y-2">
                <label className="block text-xs font-semibold text-text-secondary uppercase tracking-wider">
                  Torrents na fila para carregar ({items.length})
                </label>
                <div className="space-y-3 max-h-[40vh] overflow-y-auto pr-1">
                  {items.map(item => (
                    <div 
                      key={item.id} 
                      className={`
                        border rounded-xl bg-surface/40 overflow-hidden transition-all duration-200
                        ${item.error ? 'border-red-500/20' : 'border-default'}
                      `}
                    >
                      <div className="p-3.5 flex items-start justify-between gap-3">
                        <div className="min-w-0 flex-1">
                          <h4 className="text-sm font-medium text-text-primary truncate" title={item.name}>
                            {item.name}
                          </h4>
                          <div className="flex items-center gap-2.5 mt-1">
                            {renderItemStatus(item)}
                          </div>
                        </div>

                        <div className="flex items-center gap-2">
                          {(item.files?.length ?? 0) > 0 && selectedClientId === INTERNAL_ID && (
                            <button
                              onClick={() => toggleExpandItem(item.id)}
                              className="text-text-secondary hover:text-text-primary p-1 rounded-lg hover:bg-surface-secondary transition-colors"
                              title="Configurar arquivos individuais"
                            >
                              {item.expanded ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
                            </button>
                          )}
                          <button
                            onClick={() => handleRemoveItem(item.id)}
                            className="text-text-muted hover:text-red-400 p-1 rounded-lg hover:bg-surface-secondary transition-colors"
                            title="Remover torrent"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </div>
                      </div>

                      {/* Expandable files checklist (only for internal downloads) */}
                      {item.expanded && (item.files?.length ?? 0) > 0 && selectedClientId === INTERNAL_ID && (
                        <div className="border-t border-default bg-surface-elevated/60 p-3 space-y-2">
                          <div className="flex items-center justify-between text-xs px-1">
                            <span className="text-text-secondary font-medium">Lista de Arquivos</span>
                            <div className="flex gap-2">
                              <button
                                type="button"
                                onClick={() => handleSelectAllFiles(item.id, item.files!.map(f => f.index))}
                                className="text-cyan-400 hover:text-cyan-500 dark:hover:text-cyan-300 font-semibold"
                              >
                                Todos
                              </button>
                              <span className="text-text-muted">•</span>
                              <button
                                type="button"
                                onClick={() => handleSelectNoFiles(item.id)}
                                className="text-text-muted hover:text-text-primary font-semibold"
                              >
                                Nenhum
                              </button>
                            </div>
                          </div>
                          
                          <ul className="border border-default rounded-lg max-h-44 overflow-y-auto divide-y divide-default bg-surface/60">
                            {(item.files ?? []).map(f => {
                              const isChecked = item.selectedFiles.has(f.index)
                              const toggle = () => handleFileToggle(item.id, f.index)
                              return (
                                <li 
                                  key={f.index} 
                                  className="px-2.5 py-1.5 hover:bg-surface-secondary/40 text-xs"
                                >
                                  <label className="flex items-center gap-2 cursor-pointer">
                                  <input
                                    type="checkbox"
                                    checked={isChecked}
                                    onChange={toggle}
                                    className="accent-cyan-500 flex-shrink-0"
                                  />
                                  {fileIcon(f)}
                                  <span className="flex-1 min-w-0 text-text-primary truncate" title={f.path}>
                                    {f.path}
                                  </span>
                                  <span className="text-text-muted flex-shrink-0 font-mono">
                                    {formatBytes(f.size)}
                                  </span>
                                  </label>
                                </li>
                              )
                            })}
                          </ul>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            </div>
          )}
      </div>
    </Sheet>
  )
}
