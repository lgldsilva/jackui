import { useState, useEffect, useRef } from 'react'
import { Download, Loader2, Clock, Server, FileVideo, FileAudio, FileText, AlertCircle, Check } from 'lucide-react'
import {
  SearchResult, DownloadClient, getClients, downloadTorrent, downloadCreate,
  downloadBatchCreate, buildBatchFiles, isWholeTorrentSelection, WHOLE_TORRENT_FILE_INDEX,
  streamAdd, streamMetadata, StreamFile, TorrentInfo,
} from '../api/client'
import { Sheet } from './Sheet'
import { load, save, pushMRU } from '../lib/storage'
import { formatBytes } from '../lib/format'
import DownloadDestinationPicker from './DownloadDestinationPicker'

// Sentinel client id for "download inside JackUI itself" (anacrolix → /data),
// as opposed to handing the torrent to an external qBittorrent/Transmission.
const INTERNAL_ID = '__internal__'

// Pull the 40-hex btih out of a magnet URI. The internal download queue keys
// on info hash; search results sometimes only carry the magnet.
function hashFromMagnet(magnet: string): string {
  const m = /btih:([a-f0-9]{40})/i.exec(magnet)
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
  return <FileText className="w-4 h-4 text-text-muted flex-shrink-0" />
}

async function downloadInternal(
  result: SearchResult,
  files: StreamFile[] | null,
  selectedFiles: Set<number>,
  streamAdd: (source: string) => Promise<any>,
  downloadCreate: (opts: any) => Promise<any>,
  dest: { destBase: string; destSubdir: string } = { destBase: '', destSubdir: '' },
): Promise<string | null> {
  let magnet = result.magnetUri || (result.infoHash ? `magnet:?xt=urn:btih:${result.infoHash}` : '')
  let infoHash = result.infoHash || hashFromMagnet(magnet)
  if ((!infoHash || !magnet) && result.link) {
    const info = await streamAdd(result.link)
    infoHash = info.infoHash
    magnet = result.link
  }
  if (!infoHash || !magnet) throw new Error('Sem magnet/infoHash — não dá pra baixar internamente')

  if ((files?.length ?? 0) > 0) {
    const all = files ?? []
    const picks = all.filter(f => selectedFiles.has(f.index))
    if (picks.length === 0) throw new Error('Selecione ao menos um arquivo')
    // Todos os arquivos marcados → UMA linha "torrent inteiro" (fileIndex=-2):
    // o anacrolix baixa o torrent inteiro via file priorities, não N downloads.
    // Um pack de 778 arquivos vira 1 linha → acaba a explosão que inflava a
    // lista e fazia /api/downloads demorar. Subconjunto cai no batch (1 linha
    // por arquivo escolhido), preservando a granularidade.
    if (isWholeTorrentSelection(all, selectedFiles)) {
      await downloadCreate({
        infoHash, fileIndex: WHOLE_TORRENT_FILE_INDEX, magnet, name: result.title,
        filePath: '', fileSize: all.reduce((s, f) => s + (f.size || 0), 0),
        tracker: result.tracker || undefined, category: result.category || undefined,
        destBase: dest.destBase || undefined, destSubdir: dest.destSubdir || undefined,
      })
      return null
    }
    // UMA request batch (antes: 1 POST por arquivo). O backend insere tudo numa
    // transação tudo-ou-nada — não há mais sucesso parcial pra reportar.
    const res = await downloadBatchCreate({
      infoHash, magnet, name: result.title,
      tracker: result.tracker || undefined, category: result.category || undefined,
      destBase: dest.destBase || undefined, destSubdir: dest.destSubdir || undefined,
      files: buildBatchFiles(picks),
    })
    if (res.created.length !== picks.length) {
      return `${res.created.length}/${picks.length} enfileirados`
    }
  } else {
    await downloadCreate({ infoHash, fileIndex: 0, magnet, name: result.title, filePath: '', fileSize: 0, tracker: result.tracker || undefined, category: result.category || undefined, destBase: dest.destBase || undefined, destSubdir: dest.destSubdir || undefined })
  }
  return null
}

type DownloadModalProps = {
  readonly result: SearchResult | null
  readonly onClose: () => void
}

const KEY_CLIENT = 'lastClientId'
const KEY_PATH = 'lastSavePath'
const KEY_RECENT_PATHS = 'recentSavePaths'

// Toast discreto do auto-download (quando o único destino é o interno — sem modal de escolha).
function AutoDownloadToast({ error, success }: { readonly error: string; readonly success: boolean }) {
  let body
  if (error) body = <><AlertCircle className="w-4 h-4 text-red-400 flex-shrink-0" /><span>{error}</span></>
  else if (success) body = <><Check className="w-4 h-4 text-green-400 flex-shrink-0" /><span>Adicionado aos Downloads</span></>
  else body = <><Loader2 className="w-4 h-4 animate-spin text-green-400 flex-shrink-0" /><span>Enviando para Downloads…</span></>
  return (
    <div className="fixed bottom-4 right-4 z-[80] flex items-center gap-2 px-4 py-3 rounded-xl border border-default bg-surface-secondary text-sm text-text-primary shadow-lg">
      {body}
    </div>
  )
}

export default function DownloadModal({ result, onClose }: DownloadModalProps) {
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
  // Chosen destination for the internal download (#16); empty = default dir.
  const [dest, setDest] = useState<{ destBase: string; destSubdir: string }>({ destBase: '', destSubdir: '' })
  const pathInputRef = useRef<HTMLInputElement>(null)
  // Auto-skip: sem clientes externos, o único destino é o interno — não faz
  // sentido abrir o modal de escolha. Baixamos direto (torrent inteiro) e
  // mostramos só um toast. clientsLoaded evita o flash do modal enquanto carrega.
  const [clientsLoaded, setClientsLoaded] = useState(false)
  const autoStartedRef = useRef(false)

  useEffect(() => {
    if (!result) return

    setClientsLoaded(false)
    autoStartedRef.current = false
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
      .finally(() => setClientsLoaded(true))
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
      if ((info?.files?.length ?? 0) > 0) {
        setFiles(info!.files ?? [])
        setSelectedFiles(defaultSelected(info!.files ?? []))
      } else if (!filesError) {
        setFilesError('Metadata não disponível ainda — o worker vai resolver depois.')
      }
      setFilesLoading(false)
    })()
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result, selectedClientId])

  // Único destino = interno → baixa o torrent inteiro direto, sem abrir o modal.
  useEffect(() => {
    if (!result || !clientsLoaded || clients.length > 0 || autoStartedRef.current) return
    autoStartedRef.current = true
    void (async () => {
      setLoading(true)
      setError('')
      const err = await downloadInternal(result, null, new Set(), streamAdd, downloadCreate)
      setLoading(false)
      if (err) { setError(err); setTimeout(onClose, 5000); return }
      save(KEY_CLIENT, INTERNAL_ID)
      setSuccess(true)
      setTimeout(onClose, 1400)
    })()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result, clientsLoaded, clients.length])

  const handleDownload = async () => {
    if (!result) return

    setLoading(true)
    setError('')

    try {
      if (selectedClientId === INTERNAL_ID) {
        const error = await downloadInternal(result, files, selectedFiles, streamAdd, downloadCreate, dest)
        if (error) setError(error)
      } else {
        await downloadTorrent(selectedClientId, result.magnetUri || '', result.link || '', savePath || undefined)
      }
      save(KEY_CLIENT, selectedClientId)
      if (savePath.trim()) { save(KEY_PATH, savePath.trim()); pushMRU(KEY_RECENT_PATHS, savePath.trim()) }
      setSuccess(true)
      setTimeout(onClose, 1200)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Erro ao enviar para o cliente')
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
  // Sem clientes externos: nenhuma escolha a fazer — auto-download direto + toast, sem modal.
  if (clientsLoaded && clients.length === 0) return <AutoDownloadToast error={error} success={success} />
  // Ainda decidindo (carregando clientes) — não pisca o modal.
  if (!clientsLoaded) return null

  return (
    <Sheet
      open
      onClose={onClose}
      size="lg"
      title="Enviar para Download"
      icon={<Download className="w-4 h-4 text-green-500 flex-shrink-0" />}
      footer={
        <div className="flex gap-3">
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
      }
    >
      <div className="flex flex-col gap-4">
          <div className="bg-surface rounded-lg p-3">
            <p className="text-sm text-text-primary line-clamp-2">{result.title}</p>
            <p className="text-xs text-text-muted mt-1">{result.tracker}</p>
          </div>

          <div>
            <label htmlFor="download-client" className="block text-sm font-medium text-text-primary mb-1.5">
              Destino do download
            </label>
            <select
              id="download-client"
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
              <p className="text-[11px] text-text-muted mt-1 flex items-center gap-1">
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
                <label className="block text-sm font-medium text-text-primary">
                  Arquivos pra baixar
                  {files && (
                    <span className="text-xs text-text-muted font-normal ml-2">
                      {selectedFiles.size}/{files.length} selecionados
                    </span>
                  )}
                </label>
                {(files?.length ?? 0) > 1 && (
                  <div className="flex gap-2 text-xs">
                    <button
                      type="button"
                      onClick={() => setSelectedFiles(new Set((files ?? []).map(f => f.index)))}
                      className="text-cyan-400 hover:text-cyan-500 dark:hover:text-cyan-300"
                    >
                      Todos
                    </button>
                    <span className="text-text-muted">·</span>
                    <button
                      type="button"
                      onClick={() => setSelectedFiles(new Set())}
                      className="text-text-secondary hover:text-text-primary"
                    >
                      Nenhum
                    </button>
                  </div>
                )}
              </div>
              {filesLoading && (
                <div className="flex items-center gap-2 text-xs text-text-muted py-2">
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                  Carregando metadata...
                </div>
              )}
              {!filesLoading && filesError && !files && (
                <p className="text-xs text-amber-400 bg-amber-500/10 border border-amber-500/20 rounded px-2 py-1.5">
                  {filesError}
                </p>
              )}
              {(files?.length ?? 0) > 0 && (
                <ul className="bg-surface border border-default rounded-lg max-h-56 overflow-y-auto divide-y divide-default">
                  {(files ?? []).map(f => {
                    const checked = selectedFiles.has(f.index)
                    const toggle = () => {
                      const next = new Set(selectedFiles)
                      if (checked) next.delete(f.index); else next.add(f.index)
                      setSelectedFiles(next)
                    }
                    return (
                      <li key={f.index} className="px-3 py-2 hover:bg-surface-secondary/40">
                        <label className="flex items-center gap-2.5 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={toggle}
                          className="accent-cyan-500 flex-shrink-0"
                        />
                        {fileIcon(f)}
                        <span className="flex-1 min-w-0 text-sm text-text-primary truncate" title={f.path}>
                          {f.path}
                        </span>
                        <span className="text-xs text-text-muted flex-shrink-0">
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

          {/* Destination picker — internal download only (#16). */}
          {selectedClientId === INTERNAL_ID && (
            <DownloadDestinationPicker onChange={setDest} />
          )}

          <div className={`relative ${selectedClientId === INTERNAL_ID ? 'hidden' : ''}`}>
            <label className="block text-sm font-medium text-text-primary mb-1.5">
              Pasta de Destino{' '}
              <span className="text-text-muted font-normal">(opcional)</span>
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
    </Sheet>
  )
}
