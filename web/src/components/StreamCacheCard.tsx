import { useState, useEffect } from 'react'
import { HardDrive, Trash2, Loader2, Play, Clock, RefreshCw, Heart } from 'lucide-react'
import { streamCacheStats, streamCacheClear, StreamCacheStats } from '../api/client'
import { usePlayer } from './PlayerProvider'
import { syntheticResult } from '../lib/playable'

function formatSize(bytes: number): string {
  if (!bytes) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${Number.parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

function formatDate(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const diffH = (Date.now() - d.getTime()) / 3_600_000
  if (diffH < 1) return `${Math.floor(diffH * 60)}m atrás`
  if (diffH < 24) return `${Math.floor(diffH)}h atrás`
  if (diffH < 168) return `${Math.floor(diffH / 24)}d atrás`
  return d.toLocaleDateString('pt-BR', { day: '2-digit', month: 'short' })
}

export default function StreamCacheCard() {
  const [stats, setStats] = useState<StreamCacheStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { playSingle } = usePlayer()

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
      const m = e instanceof Error ? e.message : String(e)
      setError(m)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const handleClearAll = async () => {
    if (!confirm('Apagar TODOS os arquivos do cache de streaming? Isso interrompe qualquer torrent ativo.')) return
    setBusy(true)
    try {
      await streamCacheClear()
      await load()
    } finally {
      setBusy(false)
    }
  }

  const handleClearEntry = async (path: string, isActive: boolean) => {
    const msg = isActive
      ? `"${path}" está ativo. Apagar agora vai interromper o stream. Continuar?`
      : `Apagar "${path}" do cache?`
    if (!confirm(msg)) return
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
        Carregando estatísticas de cache...
      </div>
    )
  }

  if (error) {
    return (
      <div className="card text-red-400 text-sm">
        Streaming desabilitado ou indisponível: {error}
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
          <h2 className="text-lg font-semibold text-text-primary">Cache de Streaming</h2>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={load}
            disabled={busy}
            title="Recarregar"
            className="text-text-secondary hover:text-text-primary disabled:opacity-50 transition-colors"
          >
            <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
          </button>
          {stats.entries.length > 0 && (
            <button
              onClick={handleClearAll}
              disabled={busy}
              className="flex items-center gap-1.5 text-xs text-red-400 hover:text-red-300 transition-colors disabled:opacity-50"
            >
              <Trash2 className="w-3.5 h-3.5" />
              Limpar tudo
            </button>
          )}
        </div>
      </div>

      {/* Usage summary */}
      <div className="flex flex-col gap-2">
        <div className="flex items-baseline justify-between text-sm">
          <span className="text-text-secondary">
            <span className={`font-medium ${overLimit ? 'text-yellow-400' : 'text-text-primary'}`}>
              {formatSize(stats.totalSize)}
            </span>
            {stats.maxSize > 0
              ? <span className="text-text-muted"> de {formatSize(stats.maxSize)}</span>
              : <span className="text-text-muted"> usados (sem limite)</span>
            }
          </span>
          <span className="text-xs text-text-muted">
            {stats.entries.length} arquivo{stats.entries.length === 1 ? '' : 's'}
            {stats.numActive > 0 && <span className="ml-1 text-green-400">• {stats.numActive} ativo{stats.numActive === 1 ? '' : 's'}</span>}
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
          Pasta: <code className="text-text-secondary">{stats.dataDir}</code>
          {stats.maxSize > 0 && (
            <span className="ml-2">— quando ultrapassar o limite, entradas inativas mais antigas são removidas automaticamente.</span>
          )}
        </p>
      </div>

      {/* Entries list */}
      {stats.entries.length === 0 ? (
        <p className="text-sm text-text-muted italic text-center py-4">Cache vazio</p>
      ) : (
        <div className="flex flex-col gap-1 max-h-64 overflow-y-auto">
          {stats.entries.map((e) => (
            <div
              key={e.path}
              className="flex items-center justify-between gap-2 px-3 py-2 bg-surface/50 rounded-lg group hover:bg-surface"
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  {e.isActive && (
                    <span title="Em uso">
                      <Play className="w-3 h-3 text-green-400 fill-current flex-shrink-0" />
                    </span>
                  )}
                  {e.isFavorite && (
                    <span title="Favorito — protegido contra eviction">
                      <Heart className="w-3 h-3 text-pink-400 fill-current flex-shrink-0" />
                    </span>
                  )}
                  <span className="text-sm text-text-primary truncate" title={e.path}>
                    {e.path}
                  </span>
                </div>
                <div className="flex items-center gap-3 mt-0.5 text-xs text-text-muted">
                  <span>{formatSize(e.size)}</span>
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
                    title="Reproduzir — reativa o torrent se necessário"
                    className="flex items-center gap-1 text-xs bg-purple-500/20 hover:bg-purple-500/30 text-purple-300 border border-purple-500/30 px-2 py-1 rounded-md transition-colors disabled:opacity-50"
                  >
                    <Play className="w-3.5 h-3.5 fill-current" />
                    <span className="hidden sm:inline">Play</span>
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
                  title={e.isFavorite ? 'Favoritos não podem ser removidos — desfavorite primeiro no player' : 'Remover esta entrada'}
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
