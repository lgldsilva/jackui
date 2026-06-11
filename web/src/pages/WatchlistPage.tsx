import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Bell, CalendarClock, DownloadCloud, Loader2, Plus, Trash2, Copy, Clock, Play, Search, X } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import Thumbnail from '../components/Thumbnail'
import SeedBadge from '../components/SeedBadge'
import TorrentContentsModal from '../components/TorrentContentsModal'
import WatchlistForm from '../components/WatchlistForm'
import { schedSummary } from '../components/ScheduleEditor'
import PullToRefreshIndicator from '../components/PullToRefreshIndicator'
import { usePullToRefresh } from '../lib/usePullToRefresh'
import { useConfirm } from '../components/ConfirmDialog'
import {
  Watchlist, WatchlistHit, WatchlistInput, SearchResult,
  watchlistsList, watchlistsCreate, watchlistsUpdate, watchlistsDelete, watchlistsHits,
} from '../api/client'
import { formatBytes } from '../lib/format'
import { autoFilterSummary } from '../lib/watchlist'
import { usePlayer } from '../components/PlayerProvider'

export default function WatchlistPage() {
  const [lists, setLists] = useState<Watchlist[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [editingID, setEditingID] = useState<number | null>(null)
  const [hitsFor, setHitsFor] = useState<number | null>(null)
  const [hits, setHits] = useState<WatchlistHit[]>([])
  const [hitFilter, setHitFilter] = useState('')
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  const { playSingle } = usePlayer()
  const confirm = useConfirm()
  const { t, i18n } = useTranslation()

  const load = async () => {
    setLoading(true)
    try {
      setLists(await watchlistsList())
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { load() }, [])

  const ptr = usePullToRefresh({ onRefresh: load, disabled: loading })

  const create = async (input: WatchlistInput) => {
    await watchlistsCreate(input)
    setCreating(false)
    await load()
  }

  const saveEdit = async (input: WatchlistInput) => {
    if (editingID === null) return
    await watchlistsUpdate(editingID, input)
    setEditingID(null)
    await load()
  }
  const removeOne = async (id: number) => {
    const ok = await confirm({
      title: 'Apagar watchlist',
      message: 'Apagar essa watchlist? Todos os registros de "já visto" serão perdidos.',
      confirmLabel: 'Apagar',
      destructive: true,
    })
    if (!ok) return
    await watchlistsDelete(id)
    if (hitsFor === id) { setHitsFor(null); setHits([]) }
    await load()
  }
  const toggleHits = async (id: number) => {
    if (hitsFor === id) { setHitsFor(null); setHits([]); setHitFilter(''); return }
    setHitsFor(id)
    setHitFilter('')
    setHits(await watchlistsHits(id))
  }

  const hitToResult = (h: WatchlistHit): SearchResult => ({
    title: h.title,
    tracker: '', categoryId: 0, category: '', size: h.size,
    seeders: h.seeders, leechers: 0, age: '', magnetUri: h.magnet,
    link: '', infoHash: h.infoHash, publishDate: '',
  })
  const playHit = (h: WatchlistHit) => playSingle(hitToResult(h))
  const openContents = (h: WatchlistHit) => setContentsTarget(hitToResult(h))
  const copyMagnet = (magnet: string) => { navigator.clipboard?.writeText(magnet) }
  const renderHitItem = (h: WatchlistHit) => (
    <div key={h.infoHash} className="flex items-center gap-2 text-xs p-1.5 hover:bg-surface/50 rounded">
      <button
        onClick={() => playHit(h)}
        className="flex items-center justify-center w-11 h-11 sm:w-auto sm:h-auto sm:p-1 flex-shrink-0 rounded-lg text-green-400 hover:text-green-500 dark:hover:text-green-300 hover:bg-green-500/10 sm:hover:bg-transparent transition-colors"
        title="Reproduzir"
      >
        <Play className="w-4 h-4" />
      </button>
      <Thumbnail title={h.title} size="sm" infoHash={h.infoHash} />
      <div className="flex-1 min-w-0">
        <button
          onClick={() => openContents(h)}
          className="text-text-primary truncate block w-full text-left hover:text-green-400 transition-colors"
          title="Ver conteúdo e detalhes"
        >
          {h.title}
        </button>
        <p className="text-text-muted flex items-center gap-x-2 gap-y-1 flex-wrap">
          <SeedBadge infoHash={h.infoHash} magnet={h.magnet} />
          {h.autoDownloaded && (
            <span className="inline-flex items-center gap-0.5 text-amber-400" title={t('watchlist.auto_downloaded_hit')}>
              <DownloadCloud className="w-3 h-3" /> {t('watchlist.auto_badge')}
            </span>
          )}
          <span>{formatBytes(h.size)} · {new Date(h.seenAt).toLocaleString('pt-BR')}</span>
        </p>
      </div>
      <button
        onClick={() => copyMagnet(h.magnet)}
        className="flex items-center justify-center w-11 h-11 sm:w-auto sm:h-auto sm:p-1 flex-shrink-0 rounded-lg text-text-muted hover:text-text-primary hover:bg-surface-tertiary/40 sm:hover:bg-transparent transition-colors"
        title="Copiar magnet"
      >
        <Copy className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
      </button>
    </div>
  )

  const filteredHits = hitFilter.trim()
    ? hits.filter(h => h.title.toLowerCase().includes(hitFilter.trim().toLowerCase()))
    : hits

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <PullToRefreshIndicator pull={ptr.pull} progress={ptr.progress} refreshing={ptr.refreshing} />
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        <div className="flex items-center justify-between">
          <h1 className="text-xl font-semibold text-text-primary flex items-center gap-2">
            <Bell className="w-5 h-5 text-amber-400" /> Watchlists
          </h1>
          <button onClick={() => setCreating(true)} className="btn-primary flex items-center gap-1.5 text-sm">
            <Plus className="w-4 h-4" /> Nova
          </button>
        </div>

        <p className="text-xs text-text-muted -mt-2">
          {t('watchlist.intro')}
        </p>

        {creating && (
          <div className="card">
            <WatchlistForm onSave={create} onCancel={() => setCreating(false)} />
          </div>
        )}

{(() => {
          if (loading) return <div className="flex justify-center py-20"><Loader2 className="w-8 h-8 animate-spin text-text-muted" /></div>
          if (lists.length === 0) return <div className="text-center py-20 text-text-muted"><Bell className="w-16 h-16 mx-auto mb-4 opacity-30" /><p>Nenhuma watchlist ainda</p><p className="text-xs mt-2">Crie uma para receber push quando novos torrents aparecerem.</p></div>
          return (
          <div className="flex flex-col gap-2">
            {lists.map(w => (
              <div key={w.id} className="card flex flex-col gap-2">
                {editingID === w.id ? (
                  <WatchlistForm initial={w} onSave={saveEdit} onCancel={() => setEditingID(null)} />
                ) : (
                  <>
                    <div className="flex items-start justify-between gap-2 flex-wrap">
                      <div className="min-w-0 flex-1">
                        <p className="text-base font-semibold text-text-primary truncate" title={w.query}>{w.query}</p>
                        <p className="text-xs text-text-muted flex flex-wrap items-center gap-x-3 gap-y-1 mt-1">
                          {w.autoDownload && (
                            <span className="inline-flex items-center gap-1 text-amber-400 font-medium" title={t('watchlist.auto_download')}>
                              <DownloadCloud className="w-3.5 h-3.5" /> {autoFilterSummary(w, t('watchlist.auto_badge'))}
                            </span>
                          )}
                          {w.category && <span>Categoria: <span className="text-text-primary font-mono">{w.category}</span></span>}
                          <span>Mín. seeders: <span className="text-text-primary">{w.minSeeders}</span></span>
                          <span>Topic: <span className="text-text-primary font-mono">{w.ntfyTopic || '(padrão)'}</span></span>
                          <span className="flex items-center gap-1 text-amber-400/90">
                            <CalendarClock className="w-3 h-3" /> {schedSummary(t, w)}
                          </span>
                          {w.lastChecked && !w.lastChecked.startsWith('0001-') && (
                            <span className="flex items-center gap-1"><Clock className="w-3 h-3" /> {new Date(w.lastChecked).toLocaleString('pt-BR')}</span>
                          )}
                          {w.nextCheckAt && !w.nextCheckAt.startsWith('0001-') && (
                            <span className="flex items-center gap-1">
                              {t('watchlist.next_check', { time: new Date(w.nextCheckAt).toLocaleString(i18n.language) })}
                            </span>
                          )}
                        </p>
                      </div>
                      <div className="flex items-center gap-1">
                        <button onClick={() => toggleHits(w.id)} className="btn-secondary text-xs min-h-[44px] sm:min-h-0 px-3 sm:px-2.5">
                          {w.hitCount || 0} hits
                        </button>
                        <button onClick={() => setEditingID(w.id)} className="text-xs text-text-secondary hover:text-text-primary min-h-[44px] sm:min-h-0 px-3 py-1 flex items-center">Editar</button>
                        <button onClick={() => removeOne(w.id)} className="flex items-center justify-center text-text-muted hover:text-red-400 w-11 h-11 sm:w-auto sm:h-auto sm:p-1"><Trash2 className="w-4 h-4" /></button>
                      </div>
                    </div>

                    {hitsFor === w.id && (
                      <div className="border-t border-default pt-2 flex flex-col gap-2">
                        {hits.length > 0 && (
                          <div className="relative">
                            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted" />
                            <input
                              type="text"
                              value={hitFilter}
                              onChange={e => setHitFilter(e.target.value)}
                              placeholder="Filtrar por título..."
                              className="w-full bg-surface-secondary/80 border border-default rounded-lg pl-9 pr-9 py-2 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-amber-500/50 transition-colors"
                            />
                            {hitFilter && (
                              <button onClick={() => setHitFilter('')} className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center justify-center w-7 h-7 text-text-muted hover:text-text-primary" title="Limpar">
                                <X className="w-3.5 h-3.5" />
                              </button>
                            )}
                          </div>
                        )}
                        <div className="flex flex-col gap-1 max-h-80 overflow-y-auto">
                          {hits.length === 0 && (
                            <p className="text-xs text-text-muted text-center py-3">{t('watchlist.hits_empty')}</p>
                          )}
                          {hits.length > 0 && filteredHits.length === 0 && (
                            <p className="text-xs text-text-muted text-center py-3">Nenhum hit corresponde ao filtro.</p>
                          )}
                          {filteredHits.map(h => renderHitItem(h))}
                        </div>
                      </div>
                    )}
                  </>
                )}
              </div>
            ))}
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
