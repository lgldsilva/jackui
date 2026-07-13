import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Play, Library as LibraryIcon, CheckCircle2, Clock, X, Trash2, Info, Download as DownloadIcon, MoreVertical } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { usePlayer } from '../components/PlayerProvider'
import { libraryList, libraryDelete, libraryDeleteAll, LibraryEntry, streamArtURL, resolveArt, SearchResult } from '../api/client'
import { useRevealHidden } from '../lib/reveal'
import { AsyncState } from '../components/AsyncState'
import TorrentContentsModal from '../components/TorrentContentsModal'
import DownloadModal from '../components/DownloadModal'
import SeedBadge from '../components/SeedBadge'
import { Sheet } from '../components/Sheet'
import { useConfirm } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { useLongPress } from '../lib/useLongPress'
import { useIsMobile } from '../lib/useMediaQuery'
import { formatDuration } from '../lib/format'
import { useThumbnail } from '../lib/useThumbnail'
import { usePersistedState } from '../lib/storage'
import { useScrollRestoration } from '../lib/useScrollRestoration'
import { newTabProps, playHref } from '../lib/cardNav'

type Filter = 'recent' | 'unfinished' | 'finished'

export default function LibraryPage() {
  const [entries, setEntries] = useState<LibraryEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  useScrollRestoration(!loading)
  const [filter, setFilter] = usePersistedState<Filter>('library.filter', 'recent')
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  // Item enviado ao modal de download (destino + seleção). `indices` pré-seleciona
  // o arquivo que estava sendo assistido.
  const [dl, setDl] = useState<{ result: SearchResult; indices?: number[] } | null>(null)
  const { playSingle } = usePlayer()
  const confirm = useConfirm()
  const { notify } = useToast()
  const { t } = useTranslation()

  const [revealHidden] = useRevealHidden()

  const reload = () => {
    setLoading(true)
    libraryList({ limit: 50 })
      .then(entries => {
        setEntries(entries)
        setError(null)
      })
      .catch(err => setError(err instanceof Error ? err.message : 'Failed to load library'))
      .finally(() => setLoading(false))
  }
  // Re-fetch when the hidden curtain flips: hidden-folder titles drop in/out of
  // Continue Watching (the backend filters by the X-JackUI-Reveal-Hidden header).
  useEffect(() => { reload() }, [revealHidden])

  const handleRemoveOne = async (e: LibraryEntry) => {
    const ok = await confirm({ title: t('library.remove'), message: t('library.removeMessage', { name: e.name }), confirmLabel: t('library.remove'), destructive: true })
    if (!ok) return
    // Optimistic: drop locally, rollback if server says no
    const prev = entries
    setEntries(entries.filter(x => x.id !== e.id))
    try { await libraryDelete(e.id) } catch { setEntries(prev); notify(t('library.removeFailed'), 'error') }
  }
  const handleClearAll = async () => {
    const ok = await confirm({ title: t('library.clearAll'), message: t('library.clearAllMessage', { count: entries.length }), confirmLabel: t('library.clearAllConfirm'), destructive: true })
    if (!ok) return
    const prev = entries
    setEntries([])
    try { await libraryDeleteAll() } catch { setEntries(prev); notify(t('library.clearFailed'), 'error') }
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

  // Abre o modal unificado (destino + seleção), pré-selecionando o arquivo que
  // estava sendo assistido. Antes enfileirava direto e redirecionava p/ /downloads.
  const handleDownload = (e: LibraryEntry) => {
    const fileIndex = e.lastFileIndex >= 0 ? e.lastFileIndex : e.primaryFileIndex
    setDl({ result: entryToResult(e), indices: fileIndex >= 0 ? [fileIndex] : undefined })
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
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        <div className="flex items-center justify-between flex-wrap gap-2">
          <h1 className="text-xl font-semibold text-text-primary flex items-center gap-2">
            <LibraryIcon className="w-5 h-5 text-purple-400" /> {t('library.title')}
          </h1>
          <div className="flex items-center gap-1 text-xs flex-wrap">
            <button
              onClick={() => setFilter('recent')}
              className={filter === 'recent' ? 'btn-primary' : 'btn-secondary'}
            >{t('library.filterRecent')}</button>
            <button
              onClick={() => setFilter('unfinished')}
              className={filter === 'unfinished' ? 'btn-primary' : 'btn-secondary'}
            >{t('library.filterUnfinished')}</button>
            <button
              onClick={() => setFilter('finished')}
              className={filter === 'finished' ? 'btn-primary' : 'btn-secondary'}
            >{t('library.filterFinished')}</button>
            {entries.length > 0 && (
              <button
                onClick={handleClearAll}
                className="btn-secondary !text-red-400 hover:!bg-red-900/30 flex items-center gap-1 ml-2"
                title={t('library.clearAllTooltip')}
              >
                <Trash2 className="w-3.5 h-3.5" /> {t('library.clearAll')}
              </button>
            )}
          </div>
        </div>

        <AsyncState
          loading={loading}
          error={error}
          empty={!loading && !error && filtered.length === 0}
          onRetry={reload}
          emptyConfig={{
            icon: <LibraryIcon className="w-16 h-16 opacity-30" />,
            title: t('library.emptyTitle'),
            description: t('library.emptyHint'),
          }}
        >
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
        </AsyncState>
      </main>

      <TorrentContentsModal
        result={contentsTarget}
        onClose={() => setContentsTarget(null)}
        onPlayFile={(r, fileIdx) => { setContentsTarget(null); playSingle(r, fileIdx) }}
        onDownload={(r) => { setContentsTarget(null); setDl({ result: r }) }}
      />

      <DownloadModal result={dl?.result ?? null} initialFileIndices={dl?.indices} onClose={() => setDl(null)} />
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
  const { t } = useTranslation()
  const { ref, match } = useThumbnail<HTMLDivElement>(entry.name)
  const [artFailed, setArtFailed] = useState(false)
  const isMobile = useIsMobile()
  // Mobile context menu: a ⋮ button + long-press open a Sheet with the actions
  // (Arquivos / Download / Apagar). On desktop the hover buttons stay.
  const [menuOpen, setMenuOpen] = useState(false)
  const longPress = useLongPress(() => setMenuOpen(true), { enabled: isMobile })
  // bust forces the art <img> to refetch after a proactive resolve persists one.
  const [bust, setBust] = useState(0)
  const resolvedRef = useRef(false)
  const showArt = !!entry.infoHash && !artFailed

  // New-tab deep-link mirrors handlePlay's file pick (lastFileIndex → positive
  // primaryFileIndex → let the server decide) plus the resume position, so a
  // middle/ctrl-click reopens the same episode at the same spot.
  const playFileIdx = entry.lastFileIndex >= 0 ? entry.lastFileIndex : entry.primaryFileIndex
  const href = playHref(entry.infoHash, playFileIdx, entry.resumeSeconds)

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
    <>
    <button
      type="button"
      className="card flex flex-col gap-2 hover:bg-surface-secondary/80 transition-colors text-left p-3 relative group cursor-pointer"
      {...newTabProps(href, onPlay)}
      {...longPress}
    >
      {/* Mobile context-menu trigger (⋮) — abre o Sheet de ações. Alvo >=44px.
          No desktop fica oculto: as ações de hover abaixo bastam. */}
      <button
        onClick={(ev) => { ev.stopPropagation(); setMenuOpen(true) }}
        title={t('library.actions')}
        aria-label={t('library.actions')}
        className="sm:hidden absolute top-1 right-1 z-20 flex items-center justify-center min-w-[44px] min-h-[44px] rounded-full text-text-primary hover:bg-surface/60 transition-colors"
      >
        <MoreVertical className="w-5 h-5" />
      </button>
      {/* Per-card delete — desktop only (mobile usa o menu de contexto). Stops
          click propagation so it doesn't start playback. */}
      <button
        onClick={(ev) => { ev.stopPropagation(); onRemove() }}
        title={t('library.removeTooltip')}
        className="hidden sm:block absolute -top-2.5 -right-2.5 z-20 p-1 rounded-full bg-surface-tertiary text-text-secondary hover:text-red-400 hover:bg-surface-secondary border border-strong shadow transition-colors"
      >
        <X className="w-3.5 h-3.5" />
      </button>
      {/* Files/details — desktop only; no mobile estão no menu de contexto. */}
      <div className="hidden sm:flex absolute top-1.5 left-1.5 z-10 items-center gap-1">
        <button
          onClick={(ev) => { ev.stopPropagation(); onDetails() }}
          title={t('library.filesTooltip')}
          className="flex items-center gap-1 px-1.5 py-1 rounded-full bg-surface/85 text-text-primary hover:bg-surface text-[10px] transition-colors"
        >
          <Info className="w-3.5 h-3.5" /> {t('library.files')}
        </button>
        <button
          onClick={(ev) => { ev.stopPropagation(); onDownload() }}
          title={t('library.downloadTooltip')}
          className="flex items-center gap-1 px-1.5 py-1 rounded-full bg-surface/85 text-cyan-700 dark:text-cyan-300 hover:bg-surface text-[10px] transition-colors"
        >
          <DownloadIcon className="w-3.5 h-3.5" />
        </button>
      </div>
      <div
        ref={ref}
        className="aspect-video bg-surface rounded-lg flex items-center justify-center relative overflow-hidden"
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
          <LibraryIcon className="w-10 h-10 text-text-muted" />
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
      <p className="text-xs text-text-primary line-clamp-2" title={entry.name}>{entry.name}</p>
      <SeedBadge infoHash={entry.infoHash} magnet={entry.magnet} />
      {entry.durationSeconds > 0 && (
        <>
          <div className="h-1 bg-surface-tertiary rounded-full overflow-hidden">
            <div
              className={isDone ? 'h-full bg-green-500' : 'h-full bg-purple-500'}
              style={{ width: `${ratio * 100}%` }}
            />
          </div>
          <p className="text-[10px] text-text-muted flex items-center gap-1">
            <Clock className="w-3 h-3" />
            {isDone ? t('library.done') : t('library.remaining', { time: formatDuration(remaining) })}
          </p>
        </>
      )}
    </button>

    {/* Menu de contexto mobile (⋮ / long-press). Fica FORA do <button> do card
        pra não aninhar botões (HTML inválido). O Sheet é um overlay fixed. */}
    <Sheet
      open={menuOpen}
      onClose={() => setMenuOpen(false)}
      title={entry.name}
      size="sm"
    >
      <div className="flex flex-col gap-1">
        <button
          onClick={() => { setMenuOpen(false); onPlay() }}
          className="flex items-center gap-3 px-3 min-h-[44px] rounded-lg text-sm text-text-primary hover:bg-surface-tertiary transition-colors"
        >
          <Play className="w-4 h-4 text-green-400 flex-shrink-0" /> {t('library.play')}
        </button>
        <button
          onClick={() => { setMenuOpen(false); onDetails() }}
          className="flex items-center gap-3 px-3 min-h-[44px] rounded-lg text-sm text-text-primary hover:bg-surface-tertiary transition-colors"
        >
          <Info className="w-4 h-4 flex-shrink-0" /> {t('library.filesAndDetails')}
        </button>
        <button
          onClick={() => { setMenuOpen(false); onDownload() }}
          className="flex items-center gap-3 px-3 min-h-[44px] rounded-lg text-sm text-cyan-700 dark:text-cyan-300 hover:bg-surface-tertiary transition-colors"
        >
          <DownloadIcon className="w-4 h-4 flex-shrink-0" /> {t('library.downloadBackground')}
        </button>
        <button
          onClick={() => { setMenuOpen(false); onRemove() }}
          className="flex items-center gap-3 px-3 min-h-[44px] rounded-lg text-sm text-red-400 hover:bg-red-900/30 transition-colors"
        >
          <Trash2 className="w-4 h-4 flex-shrink-0" /> {t('library.delete')}
        </button>
      </div>
    </Sheet>
    </>
  )
}
