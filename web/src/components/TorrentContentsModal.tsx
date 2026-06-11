import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { FolderOpen, Loader2, Play, ListPlus, FileVideo, FileAudio, File as FileIcon, AlertCircle, Copy, Check, Server, Tag, Users, Calendar, Hash, Zap, Activity, ArrowDownWideNarrow, ArrowUpWideNarrow, Download } from 'lucide-react'
import { SearchResult, TorrentInfo, streamAdd, pickTorrentSource, StreamFile, streamThumbnailURL, queueAllTorrentFiles } from '../api/client'
import { useConfirm } from './ConfirmDialog'
import { formatRate } from '../lib/format'
import { usePersistedState } from '../lib/storage'
import { Sheet } from './Sheet'
import { useHoverThumb } from './FileThumbHover'

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
  return `${Number.parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
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
  return <FileIcon className="w-4 h-4 text-text-muted flex-shrink-0" />
}

function isPlayableFile(f: StreamFile): boolean {
  return f.isVideo || AUDIO_EXT.test(f.path) || VIDEO_EXT.test(f.path)
}

type FileType = 'all' | 'video' | 'audio' | 'other'

// fileType buckets a file for the type filter. Mirrors fileTypeIcon: video
// (backend flag or extension) → audio (extension) → everything else.
function fileType(f: StreamFile): Exclude<FileType, 'all'> {
  if (f.isVideo || VIDEO_EXT.test(f.path)) return 'video'
  if (AUDIO_EXT.test(f.path)) return 'audio'
  return 'other'
}

function compareBySize(a: StreamFile, b: StreamFile, desc: boolean): number {
  // Equal sizes fall back to alphabetic so the order stays stable.
  if (a.size !== b.size) return desc ? b.size - a.size : a.size - b.size
  return a.path.localeCompare(b.path)
}

// Default order: playable files first (video > audio > other), then alphabetic.
function compareDefault(a: StreamFile, b: StreamFile): number {
  const aP = isPlayableFile(a) ? 0 : 1
  const bP = isPlayableFile(b) ? 0 : 1
  if (aP !== bP) return aP - bP
  return a.path.localeCompare(b.path)
}

type FileView = { typeCounts: { video: number; audio: number; other: number }; sortedFiles: StreamFile[] }

function computeFileView(
  files: readonly StreamFile[],
  filter: string,
  typeFilter: FileType,
  sortBySize: boolean,
  sizeDesc: boolean,
): FileView {
  const typeCounts = { video: 0, audio: 0, other: 0 }
  for (const f of files) typeCounts[fileType(f)]++

  const lower = filter.toLowerCase()
  const filtered = files.filter(f =>
    (!filter || f.path.toLowerCase().includes(lower)) &&
    (typeFilter === 'all' || fileType(f) === typeFilter),
  )
  const sortedFiles = [...filtered].sort((a, b) =>
    sortBySize ? compareBySize(a, b, sizeDesc) : compareDefault(a, b),
  )
  return { typeCounts, sortedFiles }
}

/**
 * Shows the list of files inside a torrent BEFORE committing to play.
 * Lets the user pick a specific file (an episode, a single song) and either
 * play it OR add it as a single playlist item.
 */
// DetailRow renders one labelled fact in the details grid, only when it has a
// value — so synthetic results (favorites/library, which lack tracker/category)
// simply show fewer rows instead of a wall of "—".
function DetailRow({ icon, label, value }: { readonly icon: React.ReactNode; readonly label: string; readonly value?: React.ReactNode }) {
  if (value === undefined || value === null || value === '' || value === 0) return null
  return (
    <div className="flex items-center gap-2 min-w-0">
      <span className="text-text-muted flex-shrink-0">{icon}</span>
      <span className="text-text-muted flex-shrink-0">{label}:</span>
      <span className="text-text-primary truncate min-w-0">{value}</span>
    </div>
  )
}

// DownloadAllButton queues the WHOLE torrent as ONE download item (whole-torrent
// sentinel — the worker aggregates the progress of every file). Own component:
// keeps the async confirm/queue flow out of the main modal function (Sonar
// S3776 cognitive-complexity gate).
function DownloadAllButton({ info, result }: { readonly info: TorrentInfo; readonly result: SearchResult }) {
  const [busy, setBusy] = useState(false)
  const confirm = useConfirm()
  const { t } = useTranslation()
  const run = async () => {
    const ok = await confirm({
      title: 'Baixar torrent completo',
      message: `Enfileirar ${info.files.length} arquivo${info.files.length === 1 ? '' : 's'} (${formatSize(info.totalSize)})?`,
      confirmLabel: 'Baixar todos',
      destructive: false,
    })
    if (!ok) return
    setBusy(true)
    try {
      await queueAllTorrentFiles(info, pickTorrentSource(result), result.title, result.tracker || undefined, result.category || undefined)
      alert(t('downloads.whole_torrent_queued', { count: info.files.length, size: formatSize(info.totalSize) }))
    } catch (err: any) {
      alert(`Falha ao enfileirar: ${err?.response?.data?.error || err.message || err}`)
    } finally {
      setBusy(false)
    }
  }
  return (
    <button
      onClick={run}
      disabled={busy}
      title="Baixar torrent completo (todos os arquivos)"
      className="flex items-center gap-1 px-2 py-1 rounded-lg bg-blue-500/15 hover:bg-blue-500/25 text-blue-700 dark:text-blue-300 border border-blue-500/30 transition-colors disabled:opacity-50"
    >
      {busy ? <Loader2 className="w-3 h-3 animate-spin" /> : <Download className="w-3 h-3" />}
      Baixar todos
    </button>
  )
}

export default function TorrentContentsModal({ result, onClose, onPlayFile, onAddFileToPlaylist }: Props) {
  const [info, setInfo] = useState<TorrentInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')
  const [typeFilter, setTypeFilter] = useState<FileType>('all')
  // Size sort is shared (persisted) with PlayerModal so the order chosen here
  // carries into the player when you hit play — same localStorage key.
  const [sortBySize, setSortBySize] = usePersistedState('fileview.sortBySize', false)
  const [sizeDesc, setSizeDesc] = usePersistedState('fileview.sizeDesc', true)
  const [copied, setCopied] = useState(false)
  const hoverThumb = useHoverThumb()

  useEffect(() => {
    hoverThumb.hide()
  }, [result, hoverThumb])

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
    // Guard against a slow streamAdd from a PREVIOUS result resolving after the
    // user switched torrents — without it the old torrent's file list clobbers
    // the new one. Flipped by the cleanup below.
    let cancelled = false
    setLoading(true)
    setError('')
    setFilter('')
    setTypeFilter('all')
    // NOTE: sortBySize/sizeDesc are intentionally NOT reset — they persist
    // (shared with the player) so the chosen order sticks across torrents.
    streamAdd(pickTorrentSource(result))
      .then(t => { if (!cancelled) setInfo(t) })
      .catch(err => { if (!cancelled) setError(err?.response?.data?.error || err.message || 'Falha ao carregar conteúdo') })
      .finally(() => { if (!cancelled) setLoading(false) })
    // NOTE: we don't streamDrop here — the torrent stays in the cache so a follow-up
    // Play action starts streaming instantly without re-fetching metadata.
    return () => { cancelled = true }
  }, [result])

  if (!result) return null

  // typeCounts drives the pills (only present types, with counts); sortedFiles
  // applies name+type filter and the chosen sort. Extracted to keep this
  // component's cognitive complexity low.
  const { typeCounts, sortedFiles } = computeFileView(info?.files ?? [], filter, typeFilter, sortBySize, sizeDesc)

  // O backdrop fixo (inset-0) continua sendo um <div> — quem captura o clique-fora
  // e o Escape. O painel interno é o <dialog> semântico: o `w-full` anula a UA
  // `width: fit-content` (que sozinha estourava o viewport de ~390px no mobile),
  // e `p-0 m-0` neutralizam padding/margin default do user-agent.
  return (
    <>
      <Sheet
        open
        onClose={onClose}
        size="2xl"
        title="Conteúdo do torrent"
        icon={<FolderOpen className="w-4 h-4 text-blue-400 flex-shrink-0" />}
      >
        {/* Title bar — cola no topo do corpo (compensa o p-4 do Sheet) e rola
            junto com a lista, como no layout original. */}
        <div className="-mx-4 -mt-4 mb-3 px-4 py-3 border-b border-default bg-surface/50">
          {/* Tracker's release title (matches the search result card) */}
          <p className="text-sm text-text-primary line-clamp-2" title={result.title}>
            {result.title}
          </p>
          {/* Real torrent name from metadata, only when it actually differs from
              the tracker title. Trackers often translate/rename releases
              (e.g. cyrillic title for a US film), so showing both makes it
              obvious that the underlying content is what the user expects. */}
          {info?.name && info.name !== result.title && (
            <p className="text-[11px] text-text-muted mt-0.5 truncate font-mono" title={info.name}>
              {info.name}
            </p>
          )}
          {info && (
            <div className="text-xs text-text-muted mt-1 flex items-center gap-2 flex-wrap">
              <span>
                {info.files.length} arquivo{info.files.length === 1 ? '' : 's'} · {formatSize(info.totalSize)}
              </span>
              <DownloadAllButton info={info} result={result} />
            </div>
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
                  {info.peers} peer{info.peers === 1 ? '' : 's'} · {info.seeders ?? 0} seed{(info.seeders ?? 0) === 1 ? 'er' : 'ers'}
                </span>
              )}
              {(info.progress ?? 0) > 0 && (info.progress ?? 0) < 1 && (
                <span className="text-text-secondary">{((info.progress ?? 0) * 100).toFixed(1)}% baixado</span>
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
                <span className="text-text-muted flex-shrink-0"><Hash className="w-3.5 h-3.5" /></span>
                <span className="text-text-muted flex-shrink-0">Hash:</span>
                <span className="text-text-secondary font-mono truncate min-w-0" title={result.infoHash}>{result.infoHash}</span>
                <button
                  onClick={() => copyHash(result.infoHash)}
                  title="Copiar info hash"
                  className="flex-shrink-0 text-text-muted hover:text-text-primary transition-colors"
                >
                  {copied ? <Check className="w-3.5 h-3.5 text-green-400" /> : <Copy className="w-3.5 h-3.5" />}
                </button>
              </div>
            )}
          </div>
        </div>

        {/* Body — o Sheet já provê o container rolável (flex-1 overflow-y-auto p-4) */}
        <>
          {loading && (
            <div className="flex flex-col items-center justify-center py-12 text-text-secondary">
              <Loader2 className="w-8 h-8 animate-spin mb-3" />
              <p>Carregando metadados do torrent...</p>
              <p className="text-xs text-text-muted mt-1">Pode levar até 60s pra novos torrents</p>
            </div>
          )}

          {error && (
            <div className="bg-red-500/10 border border-red-500/30 rounded-xl p-4">
              <p className="flex items-center gap-2 text-red-400 font-medium">
                <AlertCircle className="w-4 h-4" /> Erro
              </p>
              <p className="text-sm text-red-700 dark:text-red-300 mt-1">{error}</p>
            </div>
          )}

          {(info?.files?.length ?? 0) > 0 && (
            <>
              {(info!.files?.length ?? 0) > 1 && (
                <div className="mb-3 flex flex-col gap-2">
                  {(info!.files?.length ?? 0) > 5 && (
                    <input
                      type="text"
                      value={filter}
                      onChange={e => setFilter(e.target.value)}
                      placeholder="Filtrar arquivos..."
                      className="input-field text-sm"
                      autoFocus
                    />
                  )}
                  <div className="flex items-center gap-1.5 flex-wrap">
                    {([
                      { key: 'all' as const, label: 'Todos', count: info!.files.length },
                      { key: 'video' as const, label: 'Vídeo', count: typeCounts.video },
                      { key: 'audio' as const, label: 'Áudio', count: typeCounts.audio },
                      { key: 'other' as const, label: 'Outros', count: typeCounts.other },
                    ])
                      .filter(o => o.key === 'all' || o.count > 0)
                      .map(o => (
                        <button
                          key={o.key}
                          onClick={() => setTypeFilter(o.key)}
                          className={`px-2 py-1 rounded-lg text-xs border transition-colors ${
                            typeFilter === o.key
                              ? 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/40'
                              : 'bg-surface text-text-secondary border-default hover:bg-surface-tertiary/60'
                          }`}
                        >
                          {o.label} <span className="tabular-nums opacity-70">{o.count}</span>
                        </button>
                      ))}
                    <div className="flex-1" />
                    <button
                      onClick={() => {
                        // Cicla: Padrão → Tamanho (maior) → Tamanho (menor) → Padrão
                        if (!sortBySize) setSortBySize(true)
                        else if (sizeDesc) setSizeDesc(false)
                        else { setSortBySize(false); setSizeDesc(true) }
                      }}
                      title="Ordenar por tamanho"
                      className={`flex items-center gap-1 px-2 py-1 rounded-lg text-xs border transition-colors ${
                        sortBySize
                          ? 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/40'
                          : 'bg-surface text-text-secondary border-default hover:bg-surface-tertiary/60'
                      }`}
                    >
                      {sortBySize && !sizeDesc
                        ? <ArrowUpWideNarrow className="w-3.5 h-3.5" />
                        : <ArrowDownWideNarrow className={`w-3.5 h-3.5 ${sortBySize ? '' : 'opacity-50'}`} />}
                      Tamanho
                    </button>
                  </div>
                </div>
              )}

              <div className="flex flex-col gap-1">
                {sortedFiles.length === 0 ? (
                  <p className="text-sm text-text-muted text-center py-6">Nenhum arquivo casa com o filtro</p>
                ) : (
                  sortedFiles.map(f => {
                    const ep = parseEpisode(f.path)
                    const playable = isPlayableFile(f)
                    const filePct = f.size > 0 && (f.downloaded ?? 0) > 0
                      ? Math.min(100, ((f.downloaded ?? 0) / f.size) * 100)
                      : null
                    // Hover preview only for video files (frame capture). thumbHash
                    // falls back to the search result's hash for synthetic torrents.
                    const thumbHash = info?.infoHash || result.infoHash
                    const thumbUrl = fileType(f) === 'video' && thumbHash
                      ? streamThumbnailURL(thumbHash, f.index, 10)
                      : null
                    return (
                      <div
                        key={f.index}
                        onMouseEnter={e => hoverThumb.show(thumbUrl, e, f.path)}
                        onMouseMove={hoverThumb.move}
                        onMouseLeave={hoverThumb.hide}
                        className={`flex flex-col px-3 py-2 rounded-lg group transition-colors ${
                          playable ? 'hover:bg-surface/70' : 'opacity-50 hover:opacity-75'
                        }`}
                      >
                        <div className="flex items-center gap-2">
                          {/* A área ícone+nome é tocável: no mobile o alvo de play
                              vira a linha inteira (não só o botãozinho verde à
                              direita, difícil de mirar). min-w-0 mantém o truncate. */}
                          <button
                            type="button"
                            onClick={() => {
                              if (playable) {
                                hoverThumb.hide()
                                onPlayFile(result, f.index)
                              }
                            }}
                            disabled={!playable}
                            title={playable ? 'Reproduzir esse arquivo' : undefined}
                            className={`flex items-center gap-2 flex-1 min-w-0 text-left ${playable ? 'cursor-pointer' : 'cursor-default'}`}
                          >
                            {fileTypeIcon(f)}
                            {ep && (
                              <span className="text-[10px] font-mono bg-blue-500/15 text-blue-700 dark:text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0">
                                {ep}
                              </span>
                            )}
                            <span className="text-sm text-text-primary truncate flex-1 min-w-0" title={f.path}>
                              {f.path}
                            </span>
                          </button>
                          <span className="text-xs text-text-muted flex-shrink-0 ml-2">{formatSize(f.size)}</span>

                        {playable && (
                          <div className="flex items-center gap-1 ml-2 flex-shrink-0">
                            <button
                              onClick={() => {
                                hoverThumb.hide()
                                onPlayFile(result, f.index)
                              }}
                              title="Reproduzir esse arquivo"
                              className="p-1.5 rounded-lg text-green-400 hover:bg-green-500/15 transition-colors"
                            >
                              <Play className="w-4 h-4 fill-current" />
                            </button>
                            {onAddFileToPlaylist && (
                              <button
                                onClick={() => {
                                  hoverThumb.hide()
                                  onAddFileToPlaylist(result, f.index, f.path)
                                }}
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
                            <div className="h-1 bg-surface-tertiary rounded-full overflow-hidden">
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
            <p className="text-sm text-text-muted text-center py-6">
              Esse torrent está vazio ou ainda não tem metadados disponíveis
            </p>
          )}
        </>
      </Sheet>
      {hoverThumb.popover}
    </>
  )
}
