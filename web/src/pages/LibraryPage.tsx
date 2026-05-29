import { useEffect, useRef, useState } from 'react'
import { Loader2, Play, Library as LibraryIcon, CheckCircle2, Clock, X, Trash2, Info, Download as DownloadIcon } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { usePlayer } from '../components/PlayerProvider'
import { libraryList, libraryDelete, libraryDeleteAll, LibraryEntry, streamArtURL, resolveArt, SearchResult, downloadCreate } from '../api/client'
import TorrentContentsModal from '../components/TorrentContentsModal'
import SeedBadge from '../components/SeedBadge'
import { formatDuration } from '../lib/format'
import { useThumbnail } from '../lib/useThumbnail'
import { usePersistedState } from '../lib/storage'

type Filter = 'recent' | 'unfinished' | 'finished'

export default function LibraryPage() {
  const [entries, setEntries] = useState<LibraryEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = usePersistedState<Filter>('library.filter', 'recent')
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  const { playSingle } = usePlayer()

  const reload = () => {
    setLoading(true)
    libraryList({ limit: 50 }).then(setEntries).catch(() => {}).finally(() => setLoading(false))
  }
  useEffect(() => { reload() }, [])

  const handleRemoveOne = async (e: LibraryEntry) => {
    if (!confirm(`Remover "${e.name}" do Continuar Assistindo?`)) return
    // Optimistic: drop locally, rollback if server says no
    const prev = entries
    setEntries(entries.filter(x => x.id !== e.id))
    try { await libraryDelete(e.id) } catch { setEntries(prev); alert('Falha ao remover') }
  }
  const handleClearAll = async () => {
    if (!confirm(`Apagar TODOS os ${entries.length} itens do Continuar Assistindo? Posições salvas serão perdidas.`)) return
    const prev = entries
    setEntries([])
    try { await libraryDeleteAll() } catch { setEntries(prev); alert('Falha ao limpar') }
  }

  const filtered = entries.filter(e => {
    if (filter === 'recent') return true
    const ratio = e.durationSeconds > 0 ? e.resumeSeconds / e.durationSeconds : 0
    if (filter === 'finished') return ratio >= 0.95
    return ratio < 0.95 && e.resumeSeconds > 0
  })

  const entryToResult = (e: LibraryEntry): SearchResult => ({
    title: e.name,
    tracker: '',
    categoryId: 0,
    category: '',
    size: 0,
    seeders: 0,
    leechers: 0,
    age: '',
    magnetUri: e.magnet,
    link: '',
    infoHash: e.infoHash,
    publishDate: '',
  })

  const handleDownload = async (e: LibraryEntry) => {
    const fileIndex = e.lastFileIndex >= 0 ? e.lastFileIndex : e.primaryFileIndex
    try {
      await downloadCreate({
        infoHash: e.infoHash,
        fileIndex,
        magnet: e.magnet,
        name: e.name,
        filePath: e.name,
        fileSize: e.totalSize,
      })
      // Navigate to downloads page so the user sees the queue
      globalThis.location.href = '/downloads'
    } catch {
      alert('Falha ao enfileirar o download')
    }
  }

  const handlePlay = (e: LibraryEntry) => {
    // Prefer the actually-watched file (tracked per resume) so reopening a
    // season pack continues the same episode. -1 = never tracked → fall back to
    // primaryFileIndex. 0 there is ambiguous (column default vs real choice), so
    // only a positive primary counts; otherwise let the server's pickPrimaryFile
    // decide (it skips featurettes/extras — the Breaking Bad bug).
    let fileIdx: number | undefined
    if (e.lastFileIndex >= 0) {
      fileIdx = e.lastFileIndex
    } else if (e.primaryFileIndex > 0) {
      fileIdx = e.primaryFileIndex
    }
    playSingle(entryToResult(e), fileIdx)
  }

  return (
    <div className="min-h-screen bg-gray-900 flex flex-col">
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        <div className="flex items-center justify-between flex-wrap gap-2">
          <h1 className="text-xl font-semibold text-gray-100 flex items-center gap-2">
            <LibraryIcon className="w-5 h-5 text-purple-400" /> Continue Assistindo
          </h1>
          <div className="flex items-center gap-1 text-xs flex-wrap">
            <button
              onClick={() => setFilter('recent')}
              className={filter === 'recent' ? 'btn-primary' : 'btn-secondary'}
            >Recentes</button>
            <button
              onClick={() => setFilter('unfinished')}
              className={filter === 'unfinished' ? 'btn-primary' : 'btn-secondary'}
            >Não terminados</button>
            <button
              onClick={() => setFilter('finished')}
              className={filter === 'finished' ? 'btn-primary' : 'btn-secondary'}
            >Concluídos</button>
            {entries.length > 0 && (
              <button
                onClick={handleClearAll}
                className="btn-secondary !text-red-400 hover:!bg-red-900/30 flex items-center gap-1 ml-2"
                title="Apagar todos os itens do Continuar Assistindo"
              >
                <Trash2 className="w-3.5 h-3.5" /> Limpar tudo
              </button>
            )}
          </div>
        </div>

        {(() => {
          if (loading) return <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-gray-500" /></div>
          if (filtered.length === 0) return <div className="text-center py-20 text-gray-500"><LibraryIcon className="w-16 h-16 mx-auto mb-4 opacity-30" /><p>Nada por aqui ainda</p><p className="text-xs mt-2">Assista algo no player — vai aparecer aqui pra continuar depois.</p></div>
          return (
          <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 gap-3">
            {filtered.map(e => {
              const ratio = e.durationSeconds > 0 ? Math.min(1, e.resumeSeconds / e.durationSeconds) : 0
              const remaining = Math.max(0, e.durationSeconds - e.resumeSeconds)
              const isDone = ratio >= 0.95
              return (
                <LibraryCard
                  key={e.id}
                  entry={e}
                  ratio={ratio}
                  remaining={remaining}
                  isDone={isDone}
                  onPlay={() => handlePlay(e)}
                  onRemove={() => handleRemoveOne(e)}
                  onDetails={() => setContentsTarget(entryToResult(e))}
                  onDownload={() => handleDownload(e)}
                />
              )
            })}
          </div>
        )})()}
      </main>

      <TorrentContentsModal
        result={contentsTarget}
        onClose={() => setContentsTarget(null)}
        onPlayFile={(r, fileIdx) => { setContentsTarget(null); playSingle(r, fileIdx) }}
      />
    </div>
  )
}

// LibraryCard is an inner component so each tile can hook into useThumbnail
// without lifting state into the parent. The hook needs a stable ref + per-card
// title input, which is awkward inside .map() without a component boundary.
type LibraryCardProps = {
  readonly entry: LibraryEntry
  readonly ratio: number
  readonly remaining: number
  readonly isDone: boolean
  readonly onPlay: () => void
  readonly onRemove: () => void
  readonly onDetails: () => void
  readonly onDownload: () => void
}

function LibraryCard({ entry, ratio, remaining, isDone, onPlay, onRemove, onDetails, onDownload }: LibraryCardProps) {
  const { ref, match } = useThumbnail<HTMLDivElement>(entry.name)
  const [artFailed, setArtFailed] = useState(false)
  // bust forces the art <img> to refetch after a proactive resolve persists one.
  const [bust, setBust] = useState(0)
  const resolvedRef = useRef(false)
  const showArt = !!entry.infoHash && !artFailed

  // When the persisted art is missing (the <img> 204s → onError), proactively
  // run the resolution chain once (TMDB → web search; no frame, torrent's idle)
  // using the entry's name as the query. If it persists something, refetch;
  // otherwise fall through to the title-based poster / icon.
  const onArtError = () => {
    if (resolvedRef.current) { setArtFailed(true); return }
    resolvedRef.current = true
    resolveArt(entry.infoHash, -1, entry.name).then(src => {
      if (src) setBust(b => b + 1)
      else setArtFailed(true)
    })
  }
  const artURL = (() => {
    const base = streamArtURL(entry.infoHash)
    if (bust > 0) {
      const separator = base.includes('?') ? '&' : '?'
      return `${base}${separator}_=${bust}`
    }
    return base
  })()

  return (
    <div
      className="card flex flex-col gap-2 hover:bg-gray-800/80 transition-colors text-left p-3 relative group cursor-pointer"
      onClick={onPlay}
    >
      {/* Per-card delete — stops click propagation so it doesn't start playback */}
      <button
        onClick={(ev) => { ev.stopPropagation(); onRemove() }}
        title="Remover do Continuar Assistindo"
        className="absolute -top-2.5 -right-2.5 z-20 p-1 rounded-full bg-gray-700 text-gray-400 hover:text-red-400 hover:bg-gray-800 border border-gray-600 shadow transition-colors"
      >
        <X className="w-3.5 h-3.5" />
      </button>
      {/* Files/details — always visible (clicking the card resumes playback, so
          this is the only way to reach the file list without committing to play). */}
      <div className="absolute top-1.5 left-1.5 z-10 flex items-center gap-1">
        <button
          onClick={(ev) => { ev.stopPropagation(); onDetails() }}
          title="Ver arquivos e detalhes"
          className="flex items-center gap-1 px-1.5 py-1 rounded-full bg-gray-900/85 text-gray-200 hover:bg-gray-900 text-[10px] transition-colors"
        >
          <Info className="w-3.5 h-3.5" /> Arquivos
        </button>
        <button
          onClick={(ev) => { ev.stopPropagation(); onDownload() }}
          title="Baixar arquivo completo em background"
          className="flex items-center gap-1 px-1.5 py-1 rounded-full bg-gray-900/85 text-cyan-300 hover:bg-gray-900 text-[10px] transition-colors"
        >
          <DownloadIcon className="w-3.5 h-3.5" />
        </button>
      </div>
      <div
        ref={ref}
        className="aspect-video bg-gray-900 rounded-lg flex items-center justify-center relative overflow-hidden"
      >
        {match?.posterUrl ? (
          <>
            {/* Blurred backdrop fills the 16:9 box; centered portrait sits on top.
                TMDB only returns portrait posters, so we cheat by reusing the
                same image as a blurred backdrop instead of letterboxing. */}
            <img
              src={match.posterUrl}
              alt=""
              aria-hidden
              className="absolute inset-0 w-full h-full object-cover scale-110 blur-md opacity-50"
            />
            <img
              src={match.posterUrl}
              alt={match.title}
              loading="lazy"
              className="relative h-full w-auto max-w-full object-contain z-10"
            />
          </>
        ) : (
          <LibraryIcon className="w-10 h-10 text-gray-700" />
        )}
        {/* Per-torrent resolved art (captured frame is already 16:9, so
            object-cover fills the box cleanly). Sits above the TMDB poster but
            below the play overlay; a 204/404 reveals the poster underneath. */}
        {showArt && (
          <img
            src={artURL}
            alt={entry.name}
            loading="lazy"
            className="absolute inset-0 w-full h-full object-cover z-[15]"
            onError={onArtError}
          />
        )}
        <div className="absolute inset-0 flex items-center justify-center max-sm:opacity-100 opacity-0 group-hover:opacity-100 transition-opacity bg-black/40 z-20">
          <Play className="w-10 h-10 text-green-400" />
        </div>
        {isDone && (
          <CheckCircle2 className="w-5 h-5 text-green-400 absolute top-1 right-1 z-20" />
        )}
      </div>
      <p className="text-xs text-gray-200 line-clamp-2" title={entry.name}>{entry.name}</p>
      <SeedBadge infoHash={entry.infoHash} magnet={entry.magnet} />
      {entry.durationSeconds > 0 && (
        <>
          <div className="h-1 bg-gray-700 rounded-full overflow-hidden">
            <div
              className={isDone ? 'h-full bg-green-500' : 'h-full bg-purple-500'}
              style={{ width: `${ratio * 100}%` }}
            />
          </div>
          <p className="text-[10px] text-gray-500 flex items-center gap-1">
            <Clock className="w-3 h-3" />
            {isDone ? 'Concluído' : `Faltam ${formatDuration(remaining)}`}
          </p>
        </>
      )}
    </div>
  )
}
