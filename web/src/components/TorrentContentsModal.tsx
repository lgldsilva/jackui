import { useState, useEffect } from 'react'
import { X, FolderOpen, Loader2, Play, ListPlus, FileVideo, FileAudio, File as FileIcon, AlertCircle } from 'lucide-react'
import { SearchResult, TorrentInfo, streamAdd, pickTorrentSource, StreamFile } from '../api/client'
import { useScrollLock } from '../lib/useScrollLock'

interface Props {
  /** When non-null, the modal opens and fetches contents. */
  result: SearchResult | null
  onClose: () => void
  /** Callback when user picks a specific file to play. */
  onPlayFile: (result: SearchResult, fileIndex: number) => void
  /** Callback when user wants to add a specific file to a playlist. */
  onAddFileToPlaylist?: (result: SearchResult, fileIndex: number, fileTitle: string) => void
}

function formatSize(bytes: number): string {
  if (!bytes) return '—'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

function parseEpisode(path: string): string | null {
  const m = path.match(/[Ss](\d{1,2})[ ._-]?[Ee](\d{1,3})/)
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
export default function TorrentContentsModal({ result, onClose, onPlayFile, onAddFileToPlaylist }: Props) {
  useScrollLock(!!result)
  const [info, setInfo] = useState<TorrentInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')

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
    <div
      className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4"
      onClick={e => e.target === e.currentTarget && onClose()}
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
          <p className="text-sm text-gray-200 line-clamp-2" title={info?.name || result.title}>
            {info?.name || result.title}
          </p>
          {info && (
            <p className="text-xs text-gray-500 mt-1">
              {info.files.length} arquivo{info.files.length !== 1 ? 's' : ''} · {formatSize(info.totalSize)}
              {info.seeders > 0 && ` · ${info.seeders} seeders`}
            </p>
          )}
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

          {info && info.files.length > 0 && (
            <>
              {info.files.length > 5 && (
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
                    return (
                      <div
                        key={f.index}
                        className={`flex items-center gap-2 px-3 py-2 rounded-lg group transition-colors ${
                          playable ? 'hover:bg-gray-900/70' : 'opacity-50 hover:opacity-75'
                        }`}
                      >
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
                    )
                  })
                )}
              </div>
            </>
          )}

          {info && info.files.length === 0 && (
            <p className="text-sm text-gray-500 text-center py-6">
              Esse torrent está vazio ou ainda não tem metadados disponíveis
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
