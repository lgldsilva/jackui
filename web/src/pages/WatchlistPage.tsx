import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import axios from 'axios'
import { Bell, CalendarClock, Loader2, Plus, Trash2, Save, Copy, Clock, Play, Search, Wand2, X } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import Thumbnail from '../components/Thumbnail'
import SeedBadge from '../components/SeedBadge'
import TorrentContentsModal from '../components/TorrentContentsModal'
import PullToRefreshIndicator from '../components/PullToRefreshIndicator'
import { usePullToRefresh } from '../lib/usePullToRefresh'
import { useConfirm } from '../components/ConfirmDialog'
import {
  SchedKind, Watchlist, WatchlistHit, SearchResult,
  watchlistsList, watchlistsCreate, watchlistsUpdate, watchlistsDelete, watchlistsHits,
  watchlistsParseSchedule,
} from '../api/client'
import { formatBytes } from '../lib/format'
import { usePlayer } from '../components/PlayerProvider'

type DraftWatchlist = {
  query: string
  category: string
  minSeeders: number
  ntfyTopic: string
  schedKind: SchedKind
  schedMinutes: number
  schedWeekday: number
  schedHour: number
  schedMinute: number
}

const EMPTY_DRAFT: DraftWatchlist = {
  query: '', category: '', minSeeders: 1, ntfyTopic: '',
  schedKind: 'interval', schedMinutes: 15, schedWeekday: 0, schedHour: 8, schedMinute: 0,
}

const pad2 = (n: number) => String(n).padStart(2, '0')

// Translate function shape (kept local so we don't depend on i18next types).
type Tr = (key: string, opts?: Record<string, unknown>) => string

type SchedFields = Pick<Watchlist, 'schedKind' | 'schedMinutes' | 'schedWeekday' | 'schedHour' | 'schedMinute'>

// schedSummary renders a schedule as a short human-readable phrase — shared by
// the watchlist cards AND the AI confirmation line in the ScheduleEditor.
function schedSummary(t: Tr, w: SchedFields): string {
  const time = `${pad2(w.schedHour)}:${pad2(w.schedMinute)}`
  if (w.schedKind === 'daily') return t('watchlist.summary_daily', { time })
  if (w.schedKind === 'weekly') {
    return t('watchlist.summary_weekly', { weekday: t(`watchlist.weekdays.${w.schedWeekday}`), time })
  }
  if (w.schedMinutes > 0) return t('watchlist.summary_interval', { minutes: w.schedMinutes })
  return t('watchlist.server_default')
}

// aiParseUnavailable flips after the first 503 (AI disabled on the server) so
// the free-text field hides for the rest of the session instead of failing again.
let aiParseUnavailable = false

// ScheduleEditor — picks how often the server re-checks this watchlist:
// fixed interval (every N minutes), daily at HH:MM or weekly on a weekday.
// The optional free-text field below asks the server's AI to interpret a phrase
// like "toda segunda às 9h"; on success it fills the selects and shows the
// summary as confirmation — the user still saves manually.
function ScheduleEditor({ value, onChange }: Readonly<{ value: DraftWatchlist; onChange: (v: DraftWatchlist) => void }>) {
  const { t } = useTranslation()
  const [aiText, setAiText] = useState('')
  const [aiBusy, setAiBusy] = useState(false)
  const [aiError, setAiError] = useState('')
  const [aiApplied, setAiApplied] = useState(false)
  const [aiAvailable, setAiAvailable] = useState(!aiParseUnavailable)
  const timeValue = `${pad2(value.schedHour)}:${pad2(value.schedMinute)}`
  const setTime = (s: string) => {
    const [h, m] = s.split(':').map(part => Number.parseInt(part, 10))
    if (!Number.isNaN(h) && !Number.isNaN(m)) onChange({ ...value, schedHour: h, schedMinute: m })
  }
  const interpret = async () => {
    const text = aiText.trim()
    if (!text || aiBusy) return
    setAiBusy(true)
    setAiError('')
    setAiApplied(false)
    try {
      const parsed = await watchlistsParseSchedule(text)
      onChange({ ...value, ...parsed })
      setAiApplied(true)
    } catch (err) {
      const status = axios.isAxiosError(err) ? err.response?.status : undefined
      if (status === 503) {
        aiParseUnavailable = true
        setAiAvailable(false)
      } else if (status === 422) {
        setAiError(t('watchlist.ai_unclear'))
      } else {
        setAiError(t('watchlist.ai_error'))
      }
    } finally {
      setAiBusy(false)
    }
  }
  return (
    <div className="flex flex-col gap-2">
    <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
      <label className="flex flex-col gap-1 text-xs text-text-muted">
        {t('watchlist.sched_label')}
        <select
          className="input-field text-base sm:text-sm"
          value={value.schedKind}
          onChange={e => onChange({ ...value, schedKind: e.target.value as SchedKind })}
        >
          <option value="interval">{t('watchlist.kind_interval')}</option>
          <option value="daily">{t('watchlist.kind_daily')}</option>
          <option value="weekly">{t('watchlist.kind_weekly')}</option>
        </select>
      </label>
      {value.schedKind === 'interval' && (
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          {t('watchlist.every_minutes')}
          <input
            type="number" min={1} className="input-field text-base sm:text-sm"
            value={value.schedMinutes}
            onChange={e => onChange({ ...value, schedMinutes: Number.parseInt(e.target.value || '0', 10) })}
          />
        </label>
      )}
      {value.schedKind === 'weekly' && (
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          {t('watchlist.weekday_label')}
          <select
            className="input-field text-base sm:text-sm"
            value={value.schedWeekday}
            onChange={e => onChange({ ...value, schedWeekday: Number.parseInt(e.target.value, 10) })}
          >
            {[0, 1, 2, 3, 4, 5, 6].map(d => (
              <option key={d} value={d}>{t(`watchlist.weekdays.${d}`)}</option>
            ))}
          </select>
        </label>
      )}
      {(value.schedKind === 'daily' || value.schedKind === 'weekly') && (
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          {t('watchlist.time_label')}
          <input
            type="time" className="input-field text-base sm:text-sm"
            value={timeValue} onChange={e => setTime(e.target.value)}
          />
        </label>
      )}
    </div>
    {aiAvailable && (
      <div className="flex flex-col gap-1">
        <div className="flex gap-2">
          <input
            className="input-field text-base sm:text-sm flex-1"
            placeholder={t('watchlist.ai_placeholder')}
            value={aiText}
            onChange={e => { setAiText(e.target.value); setAiError(''); setAiApplied(false) }}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); interpret() } }}
          />
          <button
            type="button"
            onClick={interpret}
            disabled={aiBusy || !aiText.trim()}
            className="btn-secondary flex items-center gap-1.5 text-xs disabled:opacity-50 disabled:cursor-not-allowed"
            title={t('watchlist.ai_button')}
          >
            {aiBusy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Wand2 className="w-4 h-4 text-cyan-400" />}
            <span className="hidden sm:inline">{t('watchlist.ai_button')}</span>
          </button>
        </div>
        {aiError && <p className="text-xs text-red-400">{aiError}</p>}
        {aiApplied && !aiError && (
          <p className="text-xs text-emerald-400">{t('watchlist.ai_applied', { summary: schedSummary(t, value) })}</p>
        )}
      </div>
    )}
    </div>
  )
}

export default function WatchlistPage() {
  const [lists, setLists] = useState<Watchlist[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [draft, setDraft] = useState<DraftWatchlist>(EMPTY_DRAFT)
  const [editingID, setEditingID] = useState<number | null>(null)
  const [editing, setEditing] = useState<DraftWatchlist>(EMPTY_DRAFT)
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

  const create = async () => {
    if (!draft.query.trim()) return
    await watchlistsCreate({ ...draft, query: draft.query.trim(), ntfyTopic: draft.ntfyTopic.trim() })
    setDraft(EMPTY_DRAFT)
    setCreating(false)
    await load()
  }

  const beginEdit = (w: Watchlist) => {
    setEditingID(w.id)
    setEditing({
      query: w.query, category: w.category, minSeeders: w.minSeeders, ntfyTopic: w.ntfyTopic,
      schedKind: w.schedKind || 'interval',
      schedMinutes: w.schedMinutes > 0 ? w.schedMinutes : 15,
      schedWeekday: w.schedWeekday || 0,
      schedHour: w.schedHour || 0,
      schedMinute: w.schedMinute || 0,
    })
  }
  const saveEdit = async () => {
    if (editingID === null) return
    await watchlistsUpdate(editingID, { ...editing, query: editing.query.trim(), ntfyTopic: editing.ntfyTopic.trim() })
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
          <div className="card flex flex-col gap-2">
            <input
              className="input-field text-base sm:text-sm" placeholder="Busca (ex: Breaking Bad S07 1080p)"
              value={draft.query} onChange={e => setDraft({ ...draft, query: e.target.value })} autoFocus
            />
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
              <input
                className="input-field text-base sm:text-sm" placeholder="Categoria opcional (ex: 5030)"
                value={draft.category} onChange={e => setDraft({ ...draft, category: e.target.value })}
              />
              <input
                type="number" min={0} className="input-field text-base sm:text-sm" placeholder="Mín. seeders"
                value={draft.minSeeders} onChange={e => setDraft({ ...draft, minSeeders: Number.parseInt(e.target.value || '0', 10) })}
              />
            </div>
            <input
              className="input-field text-base sm:text-sm" placeholder="Tópico ntfy.sh (em branco = usa o padrão do servidor)"
              value={draft.ntfyTopic} onChange={e => setDraft({ ...draft, ntfyTopic: e.target.value })}
            />
            <ScheduleEditor value={draft} onChange={setDraft} />
            <div className="flex gap-2">
              <button onClick={create} className="btn-primary flex items-center gap-1.5"><Save className="w-4 h-4" /> Salvar</button>
              <button onClick={() => { setCreating(false); setDraft(EMPTY_DRAFT) }} className="btn-secondary">Cancelar</button>
            </div>
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
                  <>
                    <input className="input-field text-base sm:text-sm" value={editing.query} onChange={e => setEditing({ ...editing, query: e.target.value })} />
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                      <input className="input-field text-base sm:text-sm" placeholder="Categoria" value={editing.category} onChange={e => setEditing({ ...editing, category: e.target.value })} />
                      <input type="number" min={0} className="input-field text-base sm:text-sm" placeholder="Mín. seeders" value={editing.minSeeders} onChange={e => setEditing({ ...editing, minSeeders: Number.parseInt(e.target.value || '0', 10) })} />
                    </div>
                    <input className="input-field text-base sm:text-sm" placeholder="ntfy topic" value={editing.ntfyTopic} onChange={e => setEditing({ ...editing, ntfyTopic: e.target.value })} />
                    <ScheduleEditor value={editing} onChange={setEditing} />
                    <div className="flex gap-2">
                      <button onClick={saveEdit} className="btn-primary flex items-center gap-1.5"><Save className="w-4 h-4" /> Salvar</button>
                      <button onClick={() => setEditingID(null)} className="btn-secondary">Cancelar</button>
                    </div>
                  </>
                ) : (
                  <>
                    <div className="flex items-start justify-between gap-2 flex-wrap">
                      <div className="min-w-0 flex-1">
                        <p className="text-base font-semibold text-text-primary truncate" title={w.query}>{w.query}</p>
                        <p className="text-xs text-text-muted flex flex-wrap items-center gap-x-3 gap-y-1 mt-1">
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
                        <button onClick={() => beginEdit(w)} className="text-xs text-text-secondary hover:text-text-primary min-h-[44px] sm:min-h-0 px-3 py-1 flex items-center">Editar</button>
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
