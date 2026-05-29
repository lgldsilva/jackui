import { useState, useEffect, useRef } from 'react'
import { X, Download, Loader2, Clock, Server, FileVideo, FileAudio, FileText } from 'lucide-react'
import {
  SearchResult, DownloadClient, getClients, downloadTorrent, downloadCreate,
  streamAdd, streamMetadata, StreamFile, TorrentInfo,
} from '../api/client'
import { useScrollLock } from '../lib/useScrollLock'
import { load, save, pushMRU } from '../lib/storage'
import { formatBytes } from '../lib/format'

// Sentinel client id for "download inside JackUI itself" (anacrolix → /data),
// as opposed to handing the torrent to an external qBittorrent/Transmission.
const INTERNAL_ID = '__internal__'

// Pull the 40-hex btih out of a magnet URI. The internal download queue keys
// on info hash; search results sometimes only carry the magnet.
function hashFromMagnet(magnet: string): string {
  const m = magnet.match(/btih:([a-fA-F0-9]{40})/i)
  return m ? m[1].toLowerCase() : ''
}

// Heurística pro default de "marcar pra baixar" no picker. Antes o modal
// hardcodava fileIndex=0 e enfileirava só esse — quebrava em torrents onde
// o primeiro arquivo é lixo (.nfo, .url do site, .txt) em vez do .mp4 que
// o usuário quer. Agora vem da lista e o user pode desmarcar.
//
// Regra: arquivo de vídeo (isVideo do backend) sempre marcado; outros só
// se forem grandes (>= 10MB) pra cobrir áudios, MKVs sem ext detectada,
// rars, etc. Lixo abaixo de 10MB (nfo/txt/url/jpg pequenas) fica desmarcado.
const DEFAULT_MIN_BYTES = 10 * 1024 * 1024
function defaultSelected(files: StreamFile[]): Set<number> {
  const sel = new Set<number>()
  for (const f of files) {
    if (f.isVideo || f.size >= DEFAULT_MIN_BYTES) sel.add(f.index)
  }
  // Se a heurística não marcou nenhum (torrent só de lixo OU só arquivos
  // muito pequenos), cai no maior — melhor baixar UM arquivo do que travar.
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

interface DownloadModalProps {
  result: SearchResult | null
  onClose: () => void
}

const KEY_CLIENT = 'lastClientId'
const KEY_PATH = 'lastSavePath'
const KEY_RECENT_PATHS = 'recentSavePaths'

export default function DownloadModal({ result, onClose }: DownloadModalProps) {
  useScrollLock(!!result)
  const [clients, setClients] = useState<DownloadClient[]>([])
  const [selectedClientId, setSelectedClientId] = useState('')
  const [savePath, setSavePath] = useState('')
  const [recentPaths, setRecentPaths] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState(false)
  const [showRecent, setShowRecent] = useState(false)
  // Files preview do torrent — quando destino = JackUI interno, picker mostra
  // a lista pro user marcar/desmarcar o que baixar. null = ainda carregando
  // ou destino externo (cliente externo baixa tudo, não tem como filtrar).
  const [files, setFiles] = useState<StreamFile[] | null>(null)
  const [filesLoading, setFilesLoading] = useState(false)
  const [filesError, setFilesError] = useState('')
  const [selectedFiles, setSelectedFiles] = useState<Set<number>>(new Set())
  const pathInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!result) return

    setError('')
    setSuccess(false)
    setFiles(null)
    setFilesError('')
    setSelectedFiles(new Set())
    setRecentPaths(load<string[]>(KEY_RECENT_PATHS, []))
    setSavePath(load<string>(KEY_PATH, ''))

    getClients()
      .then((data) => {
        setClients(data)
        // Priority: last-used > default external > internal (always available)
        const lastId = load<string>(KEY_CLIENT, '')
        const lastValid = lastId === INTERNAL_ID || data.some((c) => c.id === lastId)
        const fallback = data.find((c) => c.default)?.id || INTERNAL_ID
        setSelectedClientId(lastValid ? lastId : fallback)
      })
      .catch(() => {
        // Even with no external clients, internal download is always possible.
        setClients([])
        setSelectedClientId(INTERNAL_ID)
      })
  }, [result])

  // Carrega lista de arquivos quando o destino vira interno. Tenta cache de
  // metadata primeiro (instantâneo se o torrent já foi tocado/baixado antes);
  // se vazio, faz streamAdd p/ resolver. Falhas viram aviso amigável — o user
  // ainda pode clicar Confirmar e o worker tenta resolver no backend.
  useEffect(() => {
    if (!result || selectedClientId !== INTERNAL_ID) {
      setFiles(null)
      return
    }
    let cancelled = false
    setFilesLoading(true)
    setFilesError('')
    const magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
    const infoHash = result.infoHash || hashFromMagnet(magnet)
    const sourceForAdd = magnet || result.link || ''
    ;(async () => {
      let info: TorrentInfo | null = null
      if (infoHash) info = await streamMetadata(infoHash)
      if (!info && sourceForAdd) {
        try { info = await streamAdd(sourceForAdd) } catch (e) {
          if (!cancelled) setFilesError((e as Error)?.message || 'falha ao resolver metadata')
        }
      }
      if (cancelled) return
      if (info && info.files && info.files.length > 0) {
        setFiles(info.files)
        setSelectedFiles(defaultSelected(info.files))
      } else if (!filesError) {
        setFilesError('Metadata não disponível ainda — o worker vai resolver depois.')
      }
      setFilesLoading(false)
    })()
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result, selectedClientId])

  const handleDownload = async () => {
    if (!result) return

    setLoading(true)
    setError('')

    try {
      if (selectedClientId === INTERNAL_ID) {
        // Download inside JackUI: enqueue on the background worker. It resolves
        // file path/size from metadata; we just need a hash + a source the
        // streamer can add (magnet OR a .torrent URL).
        let magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
        let infoHash = result.infoHash || hashFromMagnet(magnet)
        // Some indexers only expose a .torrent link (no magnet/hash). The streamer
        // can resolve that URL — add it once to learn the infoHash, then enqueue
        // with the link as the source (the worker's EnsureActive handles URLs).
        if ((!infoHash || !magnet) && result.link) {
          const info = await streamAdd(result.link)
          infoHash = info.infoHash
          magnet = result.link
        }
        if (!infoHash || !magnet) {
          throw new Error('Sem magnet/infoHash — não dá pra baixar internamente')
        }
        // Quando temos lista de arquivos resolvida, cria N rows (uma por
        // arquivo marcado). Sem lista (metadata não chegou), fallback pra
        // file_index=0 — o worker descobre o nome real e atualiza via
        // UpdateMetadata. Esse fallback dá o bug do .nfo antigo, mas só
        // acontece quando o user clica Confirmar antes do picker carregar.
        if (files && files.length > 0) {
          const picks = files.filter(f => selectedFiles.has(f.index))
          if (picks.length === 0) throw new Error('Selecione ao menos um arquivo')
          const results = await Promise.allSettled(picks.map(f =>
            downloadCreate({
              infoHash,
              fileIndex: f.index,
              magnet,
              name: result.title,
              filePath: f.path,
              fileSize: f.size,
              tracker: result.tracker || undefined,
              category: result.category || undefined,
            }),
          ))
          const failures = results.filter(r => r.status === 'rejected')
          if (failures.length === picks.length) {
            throw new Error('Todos os downloads falharam')
          }
          if (failures.length > 0) {
            // Sucesso parcial: avisa mas considera ok (rows que passaram já estão na fila).
            setError(`${picks.length - failures.length}/${picks.length} enfileirados; ${failures.length} falharam`)
          }
        } else {
          await downloadCreate({ infoHash, fileIndex: 0, magnet, name: result.title, filePath: '', fileSize: 0, tracker: result.tracker || undefined, category: result.category || undefined })
        }
      } else {
        await downloadTorrent(
          selectedClientId,
          result.magnetUri || '',
          result.link || '',
          savePath || undefined,
        )
      }
      // Persist what worked for next time
      save(KEY_CLIENT, selectedClientId)
      if (savePath.trim()) {
        save(KEY_PATH, savePath.trim())
        pushMRU(KEY_RECENT_PATHS, savePath.trim())
      }
      setSuccess(true)
      setTimeout(onClose, 1200)
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 'Erro ao enviar para o cliente'
      setError(errorMessage)
    } finally {
      setLoading(false)
    }
  }

  const pickRecentPath = (p: string) => {
    setSavePath(p)
    setShowRecent(false)
    pathInputRef.current?.focus()
  }

  if (!result) return null

  return (
    <dialog
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4 open:flex"
      onClick={(e) => e.target === e.currentTarget && onClose()}
      onClose={onClose}
      open
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-lg shadow-2xl max-h-[90vh] flex flex-col">
        <div className="flex items-center justify-between p-5 border-b border-gray-700">
          <h2 className="text-lg font-semibold text-gray-100 flex items-center gap-2">
            <Download className="w-5 h-5 text-green-500" />
            Enviar para Download
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-200 transition-colors">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="p-5 flex flex-col gap-4 overflow-y-auto flex-1">
          <div className="bg-gray-900 rounded-lg p-3">
            <p className="text-sm text-gray-300 line-clamp-2">{result.title}</p>
            <p className="text-xs text-gray-500 mt-1">{result.tracker}</p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">
              Destino do download
            </label>
            <select
              value={selectedClientId}
              onChange={(e) => setSelectedClientId(e.target.value)}
              className="input-field"
            >
              <option value={INTERNAL_ID}>JackUI (servidor — assistir aqui)</option>
              {clients.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name} ({c.type})
                </option>
              ))}
            </select>
            {selectedClientId === INTERNAL_ID && (
              <p className="text-[11px] text-gray-500 mt-1 flex items-center gap-1">
                <Server className="w-3 h-3" />
                Baixa no servidor e aparece em Downloads — pronto pra assistir sem re-baixar.
              </p>
            )}
          </div>

          {/* File picker — só pro destino interno. Externo manda o torrent
              inteiro pro cliente, sem como filtrar arquivos. */}
          {selectedClientId === INTERNAL_ID && (
            <div>
              <div className="flex items-center justify-between mb-1.5">
                <label className="block text-sm font-medium text-gray-300">
                  Arquivos pra baixar
                  {files && (
                    <span className="text-xs text-gray-500 font-normal ml-2">
                      {selectedFiles.size}/{files.length} selecionados
                    </span>
                  )}
                </label>
                {files && files.length > 1 && (
                  <div className="flex gap-2 text-xs">
                    <button
                      type="button"
                      onClick={() => setSelectedFiles(new Set(files.map(f => f.index)))}
                      className="text-cyan-400 hover:text-cyan-300"
                    >
                      Todos
                    </button>
                    <span className="text-gray-600">·</span>
                    <button
                      type="button"
                      onClick={() => setSelectedFiles(new Set())}
                      className="text-gray-400 hover:text-gray-200"
                    >
                      Nenhum
                    </button>
                  </div>
                )}
              </div>
              {filesLoading && (
                <div className="flex items-center gap-2 text-xs text-gray-500 py-2">
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                  Carregando metadata...
                </div>
              )}
              {!filesLoading && filesError && !files && (
                <p className="text-xs text-amber-400 bg-amber-500/10 border border-amber-500/20 rounded px-2 py-1.5">
                  {filesError}
                </p>
              )}
              {files && files.length > 0 && (
                <ul className="bg-gray-900 border border-gray-700 rounded-lg max-h-56 overflow-y-auto divide-y divide-gray-800">
                  {files.map(f => {
                    const checked = selectedFiles.has(f.index)
                    const toggle = () => {
                      const next = new Set(selectedFiles)
                      if (checked) next.delete(f.index); else next.add(f.index)
                      setSelectedFiles(next)
                    }
                    return (
                      <li key={f.index} className="px-3 py-2 hover:bg-gray-800/40">
                        <label className="flex items-center gap-2.5 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={toggle}
                          className="accent-cyan-500 flex-shrink-0"
                        />
                        {fileIcon(f)}
                        <span className="flex-1 text-sm text-gray-200 truncate" title={f.path}>
                          {f.path}
                        </span>
                        <span className="text-xs text-gray-500 flex-shrink-0">
                          {formatBytes(f.size)}
                        </span>
                        </label>
                      </li>
                    )
                  })}
                </ul>
              )}
            </div>
          )}

          <div className={`relative ${selectedClientId === INTERNAL_ID ? 'hidden' : ''}`}>
            <label className="block text-sm font-medium text-gray-300 mb-1.5">
              Pasta de Destino{' '}
              <span className="text-gray-500 font-normal">(opcional)</span>
            </label>
            <div className="relative">
              <input
                ref={pathInputRef}
                type="text"
                value={savePath}
                onChange={(e) => setSavePath(e.target.value)}
                onFocus={() => setShowRecent(recentPaths.length > 0)}
                onBlur={() => setTimeout(() => setShowRecent(false), 150)}
                placeholder="/downloads/filmes"
                className="input-field pr-10"
              />
              {recentPaths.length > 0 && (
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

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg p-3">
              {error}
            </div>
          )}
          {success && (
            <div className="bg-green-500/10 border border-green-500/30 text-green-400 text-sm rounded-lg p-3">
              Torrent enviado com sucesso!
            </div>
          )}
        </div>

        <div className="flex gap-3 p-5 border-t border-gray-700">
          <button onClick={onClose} className="btn-secondary flex-1">
            Cancelar
          </button>
          <button
            onClick={handleDownload}
            disabled={loading || !selectedClientId || success}
            className="btn-primary flex-1 flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {loading ? <Loader2 className="w-4 h-4 animate-spin" /> : <Download className="w-4 h-4" />}
            Confirmar
          </button>
        </div>
      </div>
    </dialog>
  )
}
