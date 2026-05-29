import { useState, useEffect } from 'react'
import { X, FolderOpen, Loader2, Play, ListPlus, FileVideo, FileAudio, File as FileIcon, AlertCircle, Copy, Check, Server, Tag, Users, Calendar, Hash, Zap, Activity } from 'lucide-react'
import { SearchResult, TorrentInfo, streamAdd, pickTorrentSource, StreamFile } from '../api/client'
import { formatRate } from '../lib/format'
import { useScrollLock } from '../lib/useScrollLock'

type Props = {
  readonly result: SearchResult | null
  readonly onClose: () => void
  readonly onPlayFile: (result: SearchResult, fileIndex: number) => void
  readonly onAddFileToPlaylist?: (result: SearchResult, fileIndex: number, fileTitle: string) => void
}

function formatSize(bytes: number): string {
  if (!bytes) return '—'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

function parseEpisode(path: string): string | null {
  const m = /[Ss](\d{1,2})[ ._-]?[Ee](\d{1,3})/.exec(path)
  if (m) return `S${m[1].padStart(2, '0')}E${m[2].padStart(2, '0')}`
  return null
}

const AUDIO_EXT = /\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i
const VIDEO_EXT = /\.(mp4|mkv|avi|mov|webm|m4v|wmv|flv|ts|m2ts|vob)$/i

function fileTypeIcon(f: StreamFile) {
  if (f.isVideo) return <FileVideo className="w-4 h-4 text-blue-400 flex-shrink-0" />
  if (AUDIO_EXT.test(f.path)) return <FileAudio className="w-4 h-4 text-purple-400 flex-shrink-0" />
  return <FileIcon className="w-4 h-4 text-gray-500 flex-shrink-0" />
}

function isPlayableFile(f: StreamFile): boolean {
  return f.isVideo || AUDIO_EXT.test(f.path) || VIDEO_EXT.test(f.path)
}

/**
 * Shows the list of files inside a torrent BEFORE committing to play.
 * Lets the user pick a specific file (an episode, a single song) and either
 * play it OR add it as a single playlist item.
 */
// DetailRow renders one labelled fact in the details grid, only when it has a
// value — so synthetic results (favorites/library, which lack tracker/category)
// simply show fewer rows instead of a wall of "—".
function DetailRow({ icon, label, value }: { icon: React.ReactNode; label: string; value?: React.ReactNode }) {
  if (value === undefined || value === null || value === '' || value === 0) return null
  return (
    <div className="flex items-center gap-2 min-w-0">
      <span className="text-gray-500 flex-shrink-0">{icon}</span>
      <span className="text-gray-500 flex-shrink-0">{label}:</span>
      <span className="text-gray-300 truncate min-w-0">{value}</span>
    </div>
  )
}

export default function TorrentContentsModal({ result, onClose, onPlayFile, onAddFileToPlaylist }: Props) {
  useScrollLock(!!result)
  const [info, setInfo] = useState<TorrentInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')
  const [copied, setCopied] = useState(false)

  const copyHash = (hash: string) => {
    navigator.clipboard?.writeText(hash).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }).catch(() => {})
  }

  useEffect(() => {
    if (!result || !pickTorrentSource(result)) {
      setInfo(null)
      return
    }
    setLoading(true)
    setError('')
    setFilter('')
    streamAdd(pickTorrentSource(result))
      .then(setInfo)
      .catch(err => setError(err?.response?.data?.error || err.message || 'Falha ao carregar conteúdo'))
      .finally(() => setLoading(false))
    // NOTE: we don't streamDrop here — the torrent stays in the cache so a follow-up
    // Play action starts streaming instantly without re-fetching metadata.
  }, [result])

  if (!result) return null

  const filteredFiles = info?.files.filter(f =>
    !filter || f.path.toLowerCase().includes(filter.toLowerCase()),
  ) ?? []

  // Sort: playable files first (video > audio > other), then alphabetic
  const sortedFiles = [...filteredFiles].sort((a, b) => {
    const aP = isPlayableFile(a) ? 0 : 1
    const bP = isPlayableFile(b) ? 0 : 1
    if (aP !== bP) return aP - bP
    return a.path.localeCompare(b.path)
  })

  return (
    <dialog
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4 open:flex"
      onClick={e => e.target === e.currentTarget && onClose()}
      onKeyDown={e => e.key === 'Escape' && onClose()}
      onFocus={() => {}}
      onClose={onClose}
      open
    >
      <div className="bg-gray-800 rounded-2xl border border-gray-700 w-full max-w-2xl shadow-2xl max-h-[90vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-gray-700">
          <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2 min-w-0">
            <FolderOpen className="w-4 h-4 text-blue-400 flex-shrink-0" />
            <span className="truncate">Conteúdo do torrent</span>
          </h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-200">
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Title bar */}
        <div className="px-4 py-3 border-b border-gray-700 bg-gray-900/50">
          {/* Tracker's release title (matches the search result card) */}
          <p className="text-sm text-gray-200 line-clamp-2" title={result.title}>
            {result.title}
          </p>
          {/* Real torrent name from metadata, only when it actually differs from
              the tracker title. Trackers often translate/rename releases
              (e.g. cyrillic title for a US film), so showing both makes it
              obvious that the underlying content is what the user expects. */}
          {info?.name && info.name !== result.title && (
            <p className="text-[11px] text-gray-500 mt-0.5 truncate font-mono" title={info.name}>
              {info.name}
            </p>
          )}
          {info && (
            <p className="text-xs text-gray-500 mt-1">
              {info.files.length} arquivo{info.files.length !== 1 ? 's' : ''} · {formatSize(info.totalSize)}
            </p>
          )}

          {/* Live activity row — only when the torrent is actively downloading */}
          {info && (info.downRate > 0 || info.peers > 0) && (
            <div className="mt-1.5 flex items-center gap-3 text-xs flex-wrap">
              {info.downRate > 0 && (
                <span className="flex items-center gap-1 text-emerald-400">
                  <Zap className="w-3 h-3" />
                  {formatRate(info.downRate)}
                </span>
              )}
              {info.peers > 0 && (
                <span className="flex items-center gap-1 text-blue-400">
                  <Activity className="w-3 h-3" />
                  {info.peers} peer{info.peers !== 1 ? 's' : ''} · {info.seeders ?? 0} seed{(info.seeders ?? 0) !== 1 ? 'ers' : 'er'}
                </span>
              )}
              {(info.progress ?? 0) > 0 && (info.progress ?? 0) < 1 && (
                <span className="text-gray-400">{((info.progress ?? 0) * 100).toFixed(1)}% baixado</span>
              )}
            </div>
          )}

          {/* Details — visible without playing. Only rows with data render, so
              synthetic results (favorites/library) just show fewer. */}
          <div className="mt-2 grid grid-cols-1 sm:grid-cols-2 gap-x-4 gap-y-1 text-xs">
            <DetailRow icon={<Tag className="w-3.5 h-3.5" />} label="Categoria" value={result.category} />
            <DetailRow icon={<Server className="w-3.5 h-3.5" />} label="Tracker" value={result.tracker} />
            <DetailRow
              icon={<Users className="w-3.5 h-3.5" />}
              label="Seeds/Leech"
              value={(result.seeders || info?.seeders) ? `${info?.seeders ?? result.seeders} / ${result.leechers ?? 0}` : undefined}
            />
            <DetailRow
              icon={<Calendar className="w-3.5 h-3.5" />}
              label="Publicado"
              value={result.publishDate ? new Date(result.publishDate).toLocaleDateString() : result.age}
            />
            {result.infoHash && (
              <div className="flex items-center gap-2 min-w-0 sm:col-span-2">
                <span className="text-gray-500 flex-shrink-0"><Hash className="w-3.5 h-3.5" /></span>
                <span className="text-gray-500 flex-shrink-0">Hash:</span>
                <span className="text-gray-400 font-mono truncate min-w-0" title={result.infoHash}>{result.infoHash}</span>
                <button
                  onClick={() => copyHash(result.infoHash)}
                  title="Copiar info hash"
                  className="flex-shrink-0 text-gray-500 hover:text-gray-200 transition-colors"
                >
                  {copied ? <Check className="w-3.5 h-3.5 text-green-400" /> : <Copy className="w-3.5 h-3.5" />}
                </button>
              </div>
            )}
          </div>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto p-4">
          {loading && (
            <div className="flex flex-col items-center justify-center py-12 text-gray-400">
              <Loader2 className="w-8 h-8 animate-spin mb-3" />
              <p>Carregando metadados do torrent...</p>
              <p className="text-xs text-gray-500 mt-1">Pode levar até 60s pra novos torrents</p>
            </div>
          )}

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 rounded-xl p-4">
              <p className="flex items-center gap-2 text-red-400 font-medium">
                <AlertCircle className="w-4 h-4" /> Erro
              </p>
              <p className="text-sm text-red-300 mt-1">{error}</p>
            </div>
          )}

          {(info?.files?.length ?? 0) > 0 && (
            <>
              {(info!.files?.length ?? 0) > 5 && (
                <input
                  type="text"
                  value={filter}
                  onChange={e => setFilter(e.target.value)}
                  placeholder="Filtrar arquivos..."
                  className="input-field mb-3 text-sm"
                  autoFocus
                />
              )}

              <div className="flex flex-col gap-1">
                {sortedFiles.length === 0 ? (
                  <p className="text-sm text-gray-500 text-center py-6">Nenhum arquivo casa com o filtro</p>
                ) : (
                  sortedFiles.map(f => {
                    const ep = parseEpisode(f.path)
                    const playable = isPlayableFile(f)
                    const filePct = f.size > 0 && (f.downloaded ?? 0) > 0
                      ? Math.min(100, ((f.downloaded ?? 0) / f.size) * 100)
                      : null
                    return (
                      <div
                        key={f.index}
                        className={`flex flex-col px-3 py-2 rounded-lg group transition-colors ${
                          playable ? 'hover:bg-gray-900/70' : 'opacity-50 hover:opacity-75'
                        }`}
                      >
                        <div className="flex items-center gap-2">
                          {fileTypeIcon(f)}
                          {ep && (
                            <span className="text-[10px] font-mono bg-blue-500/15 text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0">
                              {ep}
                            </span>
                          )}
                          <span className="text-sm text-gray-200 truncate flex-1" title={f.path}>
                            {f.path}
                          </span>
                          <span className="text-xs text-gray-500 flex-shrink-0 ml-2">{formatSize(f.size)}</span>

                        {playable && (
                          <div className="flex items-center gap-1 ml-2 flex-shrink-0">
                            <button
                              onClick={() => onPlayFile(result, f.index)}
                              title="Reproduzir esse arquivo"
                              className="p-1.5 rounded-lg text-green-400 hover:bg-green-500/15 transition-colors"
                            >
                              <Play className="w-4 h-4 fill-current" />
                            </button>
                            {onAddFileToPlaylist && (
                              <button
                                onClick={() => onAddFileToPlaylist(result, f.index, f.path)}
                                title="Adicionar esse arquivo a uma playlist"
                                className="p-1.5 rounded-lg text-blue-400 hover:bg-blue-500/15 transition-colors max-sm:opacity-100 opacity-0 group-hover:opacity-100"
                              >
                                <ListPlus className="w-4 h-4" />
                              </button>
                            )}
                          </div>
                          )}
                        </div>
                        {filePct !== null && (
                          <div className="mt-1 ml-6">
                            <div className="h-1 bg-gray-700 rounded-full overflow-hidden">
                              <div
                                className={`h-full rounded-full transition-all ${filePct >= 100 ? 'bg-green-500' : 'bg-emerald-500'}`}
                                style={{ width: `${filePct.toFixed(1)}%` }}
                              />
                            </div>
                          </div>
                        )}
                      </div>
                    )
                  })
                )}
              </div>
            </>
          )}

          {info?.files?.length === 0 && (
            <p className="text-sm text-gray-500 text-center py-6">
              Esse torrent está vazio ou ainda não tem metadados disponíveis
            </p>
          )}
        </div>
      </div>
    </dialog>
  )
}
