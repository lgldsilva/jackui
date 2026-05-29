import { useState, useRef, DragEvent, useEffect } from 'react'
import {
  X, UploadCloud, Link2, Loader2, AlertCircle, Trash2,
  ChevronDown, ChevronUp, CheckCircle2, Server, Clock,
  FileVideo, FileAudio, FileText
} from 'lucide-react'
import {
  streamAdd, streamAddTorrentFile, streamMetadata,
  downloadCreate, downloadTorrent, getClients,
  SearchResult, StreamFile, DownloadClient
} from '../api/client'
import { useScrollLock } from '../lib/useScrollLock'
import { load, save, pushMRU } from '../lib/storage'
import { formatBytes } from '../lib/format'

interface Props {
  readonly isOpen: boolean
  readonly onClose: () => void
  readonly onAdded: (result: SearchResult) => void
  readonly preloadFiles?: File[] | null
}


interface TorrentItem {
  id: string
  name: string
  file?: File
  magnet?: string
  infoHash?: string
  loading: boolean
  error?: string
  totalSize?: number
  files?: StreamFile[]
  selectedFiles: Set<number>
  expanded?: boolean
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
  return <FileText className="w-4 h-4 text-gray-500 flex-shrink-0" />
}

export default function AddTorrentModal({ isOpen, onClose, onAdded, preloadFiles }: Props) {
  useScrollLock(isOpen)
  
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
      id: crypto.randomUUID(),
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
      const btihMatch = /btih:([a-fA-F0-9]{40})/i.exec(line)
      const nameMatch = /dn=([^&]+)/i.exec(line)
      const hash = btihMatch ? btihMatch[1].toLowerCase() : ''
      const name = nameMatch ? decodeURIComponent(nameMatch[1].replace(/\+/g, ' ')) : `Magnet (hash: ${hash || '?'})`

      return {
        id: crypto.randomUUID(),
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

    // Valida arquivos selecionados para JackUI
    if (selectedClientId === INTERNAL_ID) {
      const anyEmpty = readyItems.some(item => (item.files?.length ?? 0) > 0 && item.selectedFiles.size === 0)
      if (anyEmpty) {
        setError('Por favor, selecione ao menos um arquivo para cada torrent ou remova-o da lista.')
        return
      }
    }

    setLoading(true)
    setError('')
    
    try {
      let successCount = 0
      
      for (const item of readyItems) {
        const infoHash = item.infoHash
        const magnet = item.magnet || (infoHash ? `magnet:?xt=urn:btih:${infoHash}` : '')
        
        if (!infoHash || !magnet) continue

        if (selectedClientId === INTERNAL_ID) {
          // Destino interno: cria rows na fila
          if ((item.files?.length ?? 0) > 0) {
            const picks = (item.files ?? []).filter(f => item.selectedFiles.has(f.index))
            await Promise.all(picks.map(f =>
              downloadCreate({
                infoHash,
                fileIndex: f.index,
                magnet,
                name: item.name,
                filePath: f.path,
                fileSize: f.size,
              })
            ))
            successCount++
          } else {
            // Fallback se não resolveu arquivos ainda
            await downloadCreate({
              infoHash,
              fileIndex: 0,
              magnet,
              name: item.name,
              filePath: '',
              fileSize: 0
            })
            successCount++
          }
        } else {
          // Cliente externo
          await downloadTorrent(
            selectedClientId,
            magnet,
            '', // torrentUrl
            savePath || undefined
          )
          successCount++
        }
      }

      // Salva preferências
      save(KEY_CLIENT, selectedClientId)
      if (savePath.trim()) {
        save(KEY_PATH, savePath.trim())
        pushMRU(KEY_RECENT_PATHS, savePath.trim())
      }

      setSuccess(true)
      
      // Cria um SearchResult sintético para notificar a página inicial (compatibilidade)
      if (readyItems.length === 1) {
        const first = readyItems[0]
        onAdded({
          title: first.name,
          tracker: '',
          categoryId: 0,
          category: '',
          size: first.totalSize || 0,
          seeders: 0,
          leechers: 0,
          age: '',
          magnetUri: first.magnet || `magnet:?xt=urn:btih:${first.infoHash}`,
          link: '',
          infoHash: first.infoHash || '',
          publishDate: ''
        })
      } else {
        // Para múltiplos, apenas fecha e atualiza
        setTimeout(() => {
          onClose()
        }, 1200)
      }

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
    <dialog
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4 open:flex"
      onClick={e => e.target === e.currentTarget && onClose()}
      onKeyDown={e => e.key === 'Escape' && onClose()}
      onClose={onClose}
      open
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-xl shadow-2xl overflow-hidden flex flex-col max-h-[85vh]">
        <header className="flex items-center justify-between p-4 border-b border-gray-700 bg-gray-850">
          <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2">
            <Link2 className="w-5 h-5 text-cyan-400" />
            {view === 'drop_paste' ? 'Adicionar Novo Torrent / Magnet' : `Configurar Downloads (${items.length})`}
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-100">
            <X className="w-5 h-5" />
          </button>
        </header>

        <div className="p-5 flex-1 overflow-y-auto space-y-4">
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
              <div
                onDragEnter={handleDrag}
                onDragOver={handleDrag}
                onDragLeave={handleDrag}
                onDrop={handleDrop}
                onClick={() => fileInputRef.current?.click()}
                onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); fileInputRef.current?.click() } }}
                role="button" tabIndex={0}
                className={`
                  border-2 border-dashed rounded-xl p-8 flex flex-col items-center justify-center gap-3 cursor-pointer transition-all duration-200
                  ${dragActive 
                    ? 'border-cyan-400 bg-cyan-500/5' 
                    : 'border-gray-700 hover:border-gray-600 bg-gray-900/40 hover:bg-gray-900/60'
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
                <UploadCloud className={`w-12 h-12 ${dragActive ? 'text-cyan-400 animate-bounce' : 'text-gray-500'}`} />
                <div className="text-center">
                  <p className="text-sm font-medium text-gray-200">Arraste e solte um ou mais arquivos .torrent aqui</p>
                  <p className="text-xs text-gray-500 mt-1">ou clique para navegar no seu computador</p>
                </div>
              </div>

              <div className="relative flex py-2 items-center">
                <div className="flex-grow border-t border-gray-700"></div>
                <span className="flex-shrink mx-4 text-gray-500 text-xs uppercase tracking-wider font-semibold">ou cole links magnet</span>
                <div className="flex-grow border-t border-gray-700"></div>
              </div>

              {/* Paste Magnets Area */}
              <div className="space-y-2">
                <textarea
                  value={magnets}
                  onChange={e => setMagnets(e.target.value)}
                  placeholder="Cole os links Magnet aqui (um por linha)&#10;ex: magnet:?xt=urn:btih:..."
                  className="w-full h-36 bg-gray-900 border border-gray-700 rounded-xl px-3.5 py-2.5 text-sm text-gray-200 placeholder-gray-600 focus:outline-none focus:border-cyan-500 transition-colors font-mono resize-none"
                />
                <p className="text-[10px] text-gray-500">
                  Suporta múltiplos links Magnet (um em cada linha). O sistema carregará os metadados de cada um para você escolher o que baixar.
                </p>
              </div>
            </>
          ) : (
            // Configure list stage
            <div className="space-y-4">
              {/* Destination inputs */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 bg-gray-900/60 p-4 rounded-xl border border-gray-700/50">
                <div>
                  <label className="block text-xs font-medium text-gray-400 mb-1.5 uppercase tracking-wider">
                    Destino do download
                  </label>
                  <select
                    value={selectedClientId}
                    onChange={(e) => setSelectedClientId(e.target.value)}
                    className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 text-gray-100 text-sm focus:outline-none focus:border-cyan-500 transition-colors cursor-pointer"
                  >
                    <option value={INTERNAL_ID}>JackUI (servidor — assistir aqui)</option>
                    {clients.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name} ({c.type})
                      </option>
                    ))}
                  </select>
                  {selectedClientId === INTERNAL_ID && (
                    <p className="text-[10px] text-gray-500 mt-1 flex items-center gap-1">
                      <Server className="w-3 h-3 flex-shrink-0" />
                      Baixa no servidor e aparece em Downloads.
                    </p>
                  )}

                </div>

                <div className={`relative ${selectedClientId === INTERNAL_ID ? 'opacity-50 pointer-events-none' : ''}`}>
                  <label className="block text-xs font-medium text-gray-400 mb-1.5 uppercase tracking-wider">
                    Pasta de Destino <span className="text-gray-500 font-normal">(opcional)</span>
                  </label>
                  <div className="relative">
                    <input
                      ref={pathInputRef}
                      type="text"
                      value={savePath}
                      disabled={selectedClientId === INTERNAL_ID}
                      onChange={(e) => setSavePath(e.target.value)}
                      onFocus={() => setShowRecent(recentPaths.length > 0)}
                      onBlur={() => setTimeout(() => setShowRecent(false), 150)}
                      placeholder="/downloads/filmes"
                      className="w-full bg-gray-900 border border-gray-700 rounded-lg px-3 py-2 pr-10 text-gray-100 text-sm focus:outline-none focus:border-cyan-500 transition-colors"
                    />
                    {recentPaths.length > 0 && selectedClientId !== INTERNAL_ID && (
                      <button
                        type="button"
                        onMouseDown={(e) => { e.preventDefault(); setShowRecent(s => !s) }}
                        className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-gray-300"
                        title="Pastas recentes"
                      >
                        <Clock className="w-4 h-4" />
                      </button>
                    )}
                  </div>

                  {showRecent && recentPaths.length > 0 && (
                    <div className="absolute z-10 left-0 right-0 mt-1 bg-gray-900 border border-gray-700 rounded-lg shadow-xl max-h-48 overflow-y-auto">
                      {recentPaths.map((p) => (
                        <button
                          key={p}
                          type="button"
                          onMouseDown={(e) => { e.preventDefault(); pickRecentPath(p) }}
                          className="w-full text-left px-3 py-2 text-sm text-gray-300 hover:bg-gray-800 transition-colors truncate"
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
                <label className="block text-xs font-semibold text-gray-400 uppercase tracking-wider">
                  Torrents na fila para carregar ({items.length})
                </label>
                <div className="space-y-3 max-h-[40vh] overflow-y-auto pr-1">
                  {items.map(item => (
                    <div 
                      key={item.id} 
                      className={`
                        border rounded-xl bg-gray-900/40 overflow-hidden transition-all duration-200
                        ${item.error ? 'border-red-500/20' : 'border-gray-750'}
                      `}
                    >
                      <div className="p-3.5 flex items-start justify-between gap-3">
                        <div className="min-w-0 flex-1">
                          <h4 className="text-sm font-medium text-gray-200 truncate" title={item.name}>
                            {item.name}
                          </h4>
                          <div className="flex items-center gap-2.5 mt-1">
                            {item.loading ? (
                              <span className="text-xs text-gray-500 flex items-center gap-1.5">
                                <Loader2 className="w-3 h-3 animate-spin text-cyan-400" />
                                Buscando metadados...
                              </span>
                            ) : item.error ? (
                              <span className="text-xs text-red-400 flex items-center gap-1.5">
                                <AlertCircle className="w-3.5 h-3.5" />
                                {item.error}
                              </span>
                            ) : (
                              <span className="text-xs text-gray-400 flex items-center gap-1.5">
                                <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" />
                                {formatBytes(item.totalSize || 0)}
                                {item.files && (
                                  <>
                                    <span className="text-gray-600">•</span>
                                    <span>{item.files.length} arquivos ({item.selectedFiles.size} selecionados)</span>
                                  </>
                                )}
                              </span>
                            )}
                          </div>
                        </div>

                        <div className="flex items-center gap-2">
                          {(item.files?.length ?? 0) > 0 && selectedClientId === INTERNAL_ID && (
                            <button
                              onClick={() => toggleExpandItem(item.id)}
                              className="text-gray-400 hover:text-gray-200 p-1 rounded-lg hover:bg-gray-800 transition-colors"
                              title="Configurar arquivos individuais"
                            >
                              {item.expanded ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
                            </button>
                          )}
                          <button
                            onClick={() => handleRemoveItem(item.id)}
                            className="text-gray-500 hover:text-red-400 p-1 rounded-lg hover:bg-gray-800 transition-colors"
                            title="Remover torrent"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </div>
                      </div>

                      {/* Expandable files checklist (only for internal downloads) */}
                      {item.expanded && (item.files?.length ?? 0) > 0 && selectedClientId === INTERNAL_ID && (
                        <div className="border-t border-gray-800 bg-gray-950/60 p-3 space-y-2">
                          <div className="flex items-center justify-between text-xs px-1">
                            <span className="text-gray-400 font-medium">Lista de Arquivos</span>
                            <div className="flex gap-2">
                              <button
                                type="button"
                                onClick={() => handleSelectAllFiles(item.id, item.files!.map(f => f.index))}
                                className="text-cyan-400 hover:text-cyan-300 font-semibold"
                              >
                                Todos
                              </button>
                              <span className="text-gray-700">•</span>
                              <button
                                type="button"
                                onClick={() => handleSelectNoFiles(item.id)}
                                className="text-gray-500 hover:text-gray-300 font-semibold"
                              >
                                Nenhum
                              </button>
                            </div>
                          </div>
                          
                          <ul className="border border-gray-800 rounded-lg max-h-44 overflow-y-auto divide-y divide-gray-850 bg-gray-900/60">
                            {(item.files ?? []).map(f => {
                              const isChecked = item.selectedFiles.has(f.index)
                              const toggle = () => handleFileToggle(item.id, f.index)
                              return (
                                <li 
                                  key={f.index} 
                                  className="px-2.5 py-1.5 hover:bg-gray-800/40 text-xs"
                                >
                                  <label className="flex items-center gap-2 cursor-pointer">
                                  <input
                                    type="checkbox"
                                    checked={isChecked}
                                    onChange={toggle}
                                    className="accent-cyan-500 flex-shrink-0"
                                  />
                                  {fileIcon(f)}
                                  <span className="flex-1 text-gray-300 truncate" title={f.path}>
                                    {f.path}
                                  </span>
                                  <span className="text-gray-500 flex-shrink-0 font-mono">
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

        <footer className="p-4 border-t border-gray-700 flex items-center justify-between bg-gray-850">
          <div>
            {view === 'configure' && (
              <button
                onClick={() => setView('drop_paste')}
                className="px-4 py-2 rounded-xl text-sm font-medium text-gray-400 hover:text-gray-200 transition-colors"
              >
                Voltar
              </button>
            )}
          </div>
          
          <div className="flex items-center gap-3">
            <button
              onClick={onClose}
              disabled={loading}
              className="px-4 py-2 rounded-xl text-sm font-medium text-gray-400 hover:text-gray-200 transition-colors"
            >
              Fechar
            </button>
            
            {view === 'drop_paste' ? (
              <button
                onClick={handleAddMagnets}
                disabled={magnets.trim().length === 0}
                className="flex items-center gap-1.5 bg-cyan-500 hover:bg-cyan-600 disabled:opacity-50 text-gray-900 px-5 py-2 rounded-xl text-sm font-semibold transition-all duration-200 shadow-lg shadow-cyan-500/10"
              >
                Configurar Magnet
              </button>
            ) : (
              <button
                onClick={handleConfirmDownloads}
                disabled={loading || items.filter(i => !i.loading && !i.error).length === 0 || success}
                className="flex items-center gap-1.5 bg-emerald-500 hover:bg-emerald-600 disabled:opacity-50 text-gray-900 px-5 py-2 rounded-xl text-sm font-semibold transition-all duration-200 shadow-lg shadow-emerald-500/10"
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
        </footer>
      </div>
    </dialog>
  )
}
