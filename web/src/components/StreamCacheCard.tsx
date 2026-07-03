import { useState, useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { HardDrive, Trash2, Loader2, Play, Clock, RefreshCw, Heart, Search, ArrowDownWideNarrow, ArrowUpWideNarrow } from 'lucide-react'
import { streamCacheStats, streamCacheClear, StreamCacheStats, CacheEntry } from '../api/client'
import { usePlayer } from './PlayerProvider'
import { syntheticResult } from '../lib/playable'
import { usePersistedState } from '../lib/storage'
import { errMessage } from '../lib/errMessage'
import { formatBytes, formatDate } from '../lib/format'
import { useConfirm } from './ConfirmDialog'

type CacheSort = 'name' | 'size' | 'date'

// viewCacheEntries applies the name filter then the chosen sort. Pure → keeps
// the component body lean and is trivial to reason about.
function viewCacheEntries(entries: readonly CacheEntry[], filter: string, sortBy: CacheSort, desc: boolean): CacheEntry[] {
  const f = filter.trim().toLowerCase()
  const out = entries.filter(e => !f || e.path.toLowerCase().includes(f))
  out.sort((a, b) => {
    let cmp: number
    if (sortBy === 'size') cmp = a.size - b.size
    else if (sortBy === 'date') cmp = new Date(a.modTime).getTime() - new Date(b.modTime).getTime()
    else cmp = a.path.localeCompare(b.path)
    if (cmp === 0) cmp = a.path.localeCompare(b.path) // stable tiebreak
    return desc ? -cmp : cmp
  })
  return out
}

export default function StreamCacheCard() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [stats, setStats] = useState<StreamCacheStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')
  const [sortBy, setSortBy] = usePersistedState<CacheSort>('streamcache.sortBy', 'size')
  const [sortDesc, setSortDesc] = usePersistedState('streamcache.sortDesc', true)
  const { playSingle } = usePlayer()

  const visibleEntries = useMemo(
    () => viewCacheEntries(stats?.entries ?? [], filter, sortBy, sortDesc),
    [stats, filter, sortBy, sortDesc],
  )

  // Opens the global player on a cached entry. We have only (name, infoHash)
  // here — no full SearchResult — so we synthesize one with a bare magnet
  // ("magnet:?xt=urn:btih:HASH"). The PlayerModal calls streamAdd() on mount,
  // which transparently re-activates a torrent that was Drop'd by the idle GC
  // (same code path as the /api/stream/hls handler when files are on-disk but
  // no longer in streamer.active).
  const handlePlay = (hash: string, name: string) => {
    if (!hash) return
    const magnet = `magnet:?xt=urn:btih:${hash}`
    playSingle(syntheticResult(hash, name, magnet))
  }

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const s = await streamCacheStats()
      setStats(s)
    } catch (e: unknown) {
      const m = errMessage(e)
      setError(m)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const handleClearAll = async () => {
    const ok = await confirm({ title: t('stream.cache.clear_all_title'), message: t('stream.cache.clear_all_message'), confirmLabel: t('stream.cache.clear_all_confirm'), destructive: true })
    if (!ok) return
    setBusy(true)
    try {
      await streamCacheClear()
      await load()
    } finally {
      setBusy(false)
    }
  }

  const handleClearEntry = async (path: string, isActive: boolean) => {
    const message = isActive
      ? t('stream.cache.delete_active_message', { path })
      : t('stream.cache.delete_message', { path })
    const ok = await confirm({ title: t('stream.cache.delete_title'), message, confirmLabel: t('stream.cache.delete_confirm'), destructive: true })
    if (!ok) return
    setBusy(true)
    try {
      await streamCacheClear(path)
      await load()
    } finally {
      setBusy(false)
    }
  }

  if (loading && !stats) {
    return (
      <div className="card flex items-center gap-3 text-text-secondary">
        <Loader2 className="w-4 h-4 animate-spin" />
        {t('stream.cache.loading')}
      </div>
    )
  }

  if (error) {
    return (
      <div className="card text-red-400 text-sm">
        {t('stream.cache.unavailable', { error })}
      </div>
    )
  }

  if (!stats) return null

  const usagePct = stats.maxSize > 0 ? (stats.totalSize / stats.maxSize) * 100 : 0
  const overLimit = stats.maxSize > 0 && stats.totalSize > stats.maxSize

  let barClass: string
  if (overLimit) {
    barClass = 'bg-yellow-500'
  } else if (usagePct > 80) {
    barClass = 'bg-orange-500'
  } else {
    barClass = 'bg-green-500'
  }

  return (
    <div className="card flex flex-col gap-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <HardDrive className="w-5 h-5 text-green-500" />
          <h2 className="text-lg font-semibold text-text-primary">{t('stream.cache.title')}</h2>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={load}
            disabled={busy}
            title={t('stream.cache.reload')}
            className="text-text-secondary hover:text-text-primary disabled:opacity-50 transition-colors"
          >
            <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
          </button>
          {stats.entries.length > 0 && (
            <button
              onClick={handleClearAll}
              disabled={busy}
              className="flex items-center gap-1.5 text-xs text-red-400 hover:text-red-500 dark:hover:text-red-300 transition-colors disabled:opacity-50"
            >
              <Trash2 className="w-3.5 h-3.5" />
              {t('stream.cache.clear_all_confirm')}
            </button>
          )}
        </div>
      </div>

      {/* Usage summary */}
      <div className="flex flex-col gap-2">
        <div className="flex items-baseline justify-between text-sm">
          <span className="text-text-secondary">
            <span className={`font-medium ${overLimit ? 'text-yellow-400' : 'text-text-primary'}`}>
              {formatBytes(stats.totalSize)}
            </span>
            {stats.maxSize > 0
              ? <span className="text-text-muted"> {t('stream.cache.of')} {formatBytes(stats.maxSize)}</span>
              : <span className="text-text-muted"> {t('stream.cache.used_no_limit')}</span>
            }
          </span>
          <span className="text-xs text-text-muted">
            {stats.entries.length} {stats.entries.length === 1 ? t('stream.cache.file') : t('stream.cache.files')}
            {stats.numActive > 0 && <span className="ml-1 text-green-400">• {stats.numActive} {stats.numActive === 1 ? t('stream.cache.active') : t('stream.cache.active_plural')}</span>}
          </span>
        </div>
        {stats.maxSize > 0 && (
          <div className="bg-surface rounded-full h-2 overflow-hidden">
            <div
              className={`h-full transition-all ${barClass}`}
              style={{ width: `${Math.min(100, usagePct)}%` }}
            />
          </div>
        )}
        <p className="text-xs text-text-muted">
          {t('stream.cache.folder_label')} <code className="text-text-secondary">{stats.dataDir}</code>
          {stats.maxSize > 0 && (
            <span className="ml-2">{t('stream.cache.limit_note')}</span>
          )}
        </p>
        {stats.diskTotal > 0 && (
          <p className="text-xs text-text-muted">
            {t('stream.cache.disk_label')} <span className="text-text-secondary">{formatBytes(stats.diskFree)} {t('stream.cache.free')}</span> {t('stream.cache.of')} {formatBytes(stats.diskTotal)}
          </p>
        )}
        {stats.evictedCount > 0 && (
          <p className="text-xs text-text-muted">
            {t('stream.cache.lru_recycled')}{' '}
            <span className="text-text-secondary">
              {stats.evictedCount} {stats.evictedCount === 1 ? t('stream.cache.item') : t('stream.cache.items')} ({formatBytes(stats.evictedBytes)})
            </span>
            {stats.lastEvictionAt && <span>{t('stream.cache.last_eviction', { date: formatDate(stats.lastEvictionAt) })}</span>}
          </p>
        )}
      </div>

      {/* Sort + filter controls (only worth showing with a few entries) */}
      {stats.entries.length > 1 && (
        <div className="flex items-center gap-2 flex-wrap">
          <div className="relative flex-1 min-w-[140px]">
            <Search className="w-3.5 h-3.5 text-text-muted absolute left-2.5 top-1/2 -translate-y-1/2" />
            <input
              type="text"
              value={filter}
              onChange={e => setFilter(e.target.value)}
              placeholder={t('stream.cache.filter_placeholder')}
              className="w-full bg-surface border border-default rounded-lg pl-8 pr-3 py-1.5 text-sm focus:outline-none focus:border-cyan-500 text-text-primary"
            />
          </div>
          {(['size', 'name', 'date'] as CacheSort[]).map(col => (
            <button
              key={col}
              onClick={() => {
                if (sortBy === col) setSortDesc(d => !d)
                else { setSortBy(col); setSortDesc(col !== 'name') }
              }}
              className={`flex items-center gap-1 px-2 py-1.5 rounded-lg text-xs border transition-colors ${
                sortBy === col
                  ? 'bg-cyan-500/20 text-cyan-700 dark:text-cyan-300 border-cyan-500/40'
                  : 'bg-surface text-text-secondary border-default hover:bg-surface-tertiary'
              }`}
            >
              {col === 'size' ? t('stream.cache.sort_size') : col === 'name' ? t('stream.cache.sort_name') : t('stream.cache.sort_date')}
              {sortBy === col && (sortDesc ? <ArrowDownWideNarrow className="w-3 h-3" /> : <ArrowUpWideNarrow className="w-3 h-3" />)}
            </button>
          ))}
        </div>
      )}

      {/* Entries list */}
      {stats.entries.length === 0 ? (
        <p className="text-sm text-text-muted italic text-center py-4">{t('stream.cache.empty')}</p>
      ) : visibleEntries.length === 0 ? (
        <p className="text-sm text-text-muted italic text-center py-4">{t('stream.cache.no_match')}</p>
      ) : (
        <div className="flex flex-col gap-1 max-h-64 overflow-y-auto">
          {visibleEntries.map((e) => (
            <div
              key={e.path}
              className="flex items-center justify-between gap-2 px-3 py-2 bg-surface/50 rounded-lg group hover:bg-surface"
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  {e.isActive && (
                    <span title={t('stream.cache.in_use')}>
                      <Play className="w-3 h-3 text-green-400 fill-current flex-shrink-0" />
                    </span>
                  )}
                  {e.isFavorite && (
                    <span title={t('stream.cache.favorite_protected')}>
                      <Heart className="w-3 h-3 text-pink-400 fill-current flex-shrink-0" />
                    </span>
                  )}
                  <span className="text-sm text-text-primary truncate" title={e.path}>
                    {e.path}
                  </span>
                </div>
                <div className="flex items-center gap-3 mt-0.5 text-xs text-text-muted">
                  <span>{formatBytes(e.size)}</span>
                  <span className="flex items-center gap-1">
                    <Clock className="w-2.5 h-2.5" />{formatDate(e.modTime)}
                  </span>
                </div>
              </div>
              <div className="flex items-center gap-1 flex-shrink-0">
                {e.infoHash && (
                  <button
                    onClick={() => handlePlay(e.infoHash!, e.path)}
                    disabled={busy}
                    title={t('stream.cache.play_title')}
                    className="flex items-center gap-1 text-xs bg-purple-500/20 hover:bg-purple-500/30 text-purple-700 dark:text-purple-300 border border-purple-500/30 px-2 py-1 rounded-md transition-colors disabled:opacity-50"
                  >
                    <Play className="w-3.5 h-3.5 fill-current" />
                    <span className="hidden sm:inline">{t('stream.cache.play')}</span>
                  </button>
                )}
                <button
                  onClick={() => handleClearEntry(e.path, e.isActive)}
                  disabled={busy || e.isFavorite}
                  className={`transition-all ${
                    e.isFavorite
                      ? 'opacity-0 cursor-not-allowed'
                      : 'max-sm:opacity-100 opacity-0 group-hover:opacity-100 text-text-muted hover:text-red-400 disabled:opacity-50'
                  }`}
                  title={e.isFavorite ? t('stream.cache.favorite_no_remove') : t('stream.cache.remove_entry')}
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
