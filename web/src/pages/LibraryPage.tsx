import { useEffect, useState } from 'react'
import { Loader2, Play, Library as LibraryIcon, CheckCircle2, Clock, X, Trash2 } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { usePlayer } from '../components/PlayerProvider'
import { libraryList, libraryDelete, libraryDeleteAll, LibraryEntry } from '../api/client'
import { formatDuration } from '../lib/format'
import { useThumbnail } from '../lib/useThumbnail'

type Filter = 'recent' | 'unfinished' | 'finished'

export default function LibraryPage() {
  const [entries, setEntries] = useState<LibraryEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<Filter>('recent')
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

  const handlePlay = (e: LibraryEntry) => {
    playSingle(
      {
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
      },
      // 0 in the DB is ambiguous — it can be either "user explicitly chose file 0" OR
      // the column default (NOT NULL DEFAULT 0) from an older session where pickPrimaryFile
      // hadn't been computed yet. Treating > 0 as "real choice" pushes the decision to
      // the server's pickPrimaryFile (which detects featurettes/extras), preventing the
      // Breaking Bad-style bug where a stale 0 made the player target a featurette
      // instead of S01E01.
      e.primaryFileIndex > 0 ? e.primaryFileIndex : undefined,
    )
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

        {loading ? (
          <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-gray-500" /></div>
        ) : filtered.length === 0 ? (
          <div className="text-center py-20 text-gray-500">
            <LibraryIcon className="w-16 h-16 mx-auto mb-4 opacity-30" />
            <p>Nada por aqui ainda</p>
            <p className="text-xs mt-2">Assista algo no player — vai aparecer aqui pra continuar depois.</p>
          </div>
        ) : (
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
                />
              )
            })}
          </div>
        )}
      </main>
    </div>
  )
}

// LibraryCard is an inner component so each tile can hook into useThumbnail
// without lifting state into the parent. The hook needs a stable ref + per-card
// title input, which is awkward inside .map() without a component boundary.
interface LibraryCardProps {
  entry: LibraryEntry
  ratio: number
  remaining: number
  isDone: boolean
  onPlay: () => void
  onRemove: () => void
}

function LibraryCard({ entry, ratio, remaining, isDone, onPlay, onRemove }: LibraryCardProps) {
  const { ref, match } = useThumbnail<HTMLDivElement>(entry.name)
  return (
    <div
      className="card flex flex-col gap-2 hover:bg-gray-800/80 transition-colors text-left p-3 relative group cursor-pointer"
      onClick={onPlay}
    >
      {/* Per-card delete — stops click propagation so it doesn't start playback */}
      <button
        onClick={(ev) => { ev.stopPropagation(); onRemove() }}
        title="Remover do Continuar Assistindo"
        className="absolute top-1.5 right-1.5 z-10 p-1 rounded-full bg-gray-900/80 text-gray-400 hover:text-red-400 hover:bg-gray-900 opacity-0 group-hover:opacity-100 transition-opacity"
      >
        <X className="w-3.5 h-3.5" />
      </button>
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
        <div className="absolute inset-0 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity bg-black/40 z-20">
          <Play className="w-10 h-10 text-green-400" />
        </div>
        {isDone && (
          <CheckCircle2 className="w-5 h-5 text-green-400 absolute top-1 right-1 z-20" />
        )}
      </div>
      <p className="text-xs text-gray-200 line-clamp-2" title={entry.name}>{entry.name}</p>
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
