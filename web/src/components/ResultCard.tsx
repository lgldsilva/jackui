import { useState, useEffect, useRef } from 'react'
import { Magnet, Users, TrendingDown, Clock, HardDrive, Tag, Check, FileDown, Clipboard, ExternalLink, Play, Globe, Heart, ListPlus, FolderOpen } from 'lucide-react'
import { SearchResult, TmdbMatch, favoriteAdd, favoriteRemove, tmdbMatch } from '../api/client'
import QualityBadges from './QualityBadges'
import { isPlayable } from '../lib/playable'

// Module-scoped cache of favorite identifiers. We store both `name` and
// `infoHash` so cards can match by hash first (precise — same content always
// returns the same hash) and fall back to title only when the result has no
// hash (some trackers don't expose it via Jackett).
//
// Why this got more careful: the old version stored only names → two cards
// with identical titles but different sources (different trackers) shared
// favorite state, so favoriting one filled the heart on the other too. Hash
// matching makes each torrent independent except for true dupes.
const favoriteSet = new Set<string>()      // names (legacy + fallback)
const favoriteHashSet = new Set<string>()  // info hashes (preferred)
const listeners = new Set<() => void>()

export interface FavoriteEntry {
  name: string
  infoHash?: string
}

export function refreshFavoritesCache(entries: FavoriteEntry[] | string[]) {
  favoriteSet.clear()
  favoriteHashSet.clear()
  for (const e of entries) {
    if (typeof e === 'string') {
      favoriteSet.add(e)
    } else {
      if (e.name) favoriteSet.add(e.name)
      if (e.infoHash) favoriteHashSet.add(e.infoHash)
    }
  }
  listeners.forEach(fn => fn())
}

export function isFavoriteResult(infoHash: string | undefined, name: string): boolean {
  // STRICT hash matching: if the card carries an info hash, ONLY a matching
  // hash counts as favorited. Don't fall back to name because two different
  // torrents (different hashes) can share titles across trackers — falling
  // back made favoriting card A visually fill card B's heart and, worse,
  // clicking heart on B then called favoriteRemove(title) which deleted A's
  // saved row (backend keys favorites on name).
  if (infoHash) return favoriteHashSet.has(infoHash)
  // No hash on the result — last-resort match by name (legacy entries +
  // sources that don't expose info hash).
  return favoriteSet.has(name)
}

interface ResultCardProps {
  result: SearchResult
  onDownload: (result: SearchResult) => void
  onPlay?: (result: SearchResult) => void
  onAddToPlaylist?: (result: SearchResult) => void
  onExploreContents?: (result: SearchResult) => void
}

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

async function copyToClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const el = document.createElement('textarea')
    el.value = text
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    document.body.removeChild(el)
  }
}

export default function ResultCard({ result, onDownload, onPlay, onAddToPlaylist, onExploreContents }: ResultCardProps) {
  const [copied, setCopied] = useState(false)
  const [_, force] = useState(0) // re-render when favoriteSet changes globally
  // TMDB lazy enrichment — only fires once the card has been visible.
  // Server returns 204 (no match) or 503 (disabled) without breaking.
  const [tmdb, setTmdb] = useState<TmdbMatch | null>(null)
  const cardRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!cardRef.current) return
    const obs = new IntersectionObserver((entries, observer) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        observer.disconnect()
        tmdbMatch(result.title).then(m => { if (m) setTmdb(m) })
        return
      }
    }, { rootMargin: '120px' /* trigger slightly before viewport entry */ })
    obs.observe(cardRef.current)
    return () => obs.disconnect()
  }, [result.title])

  // Subscribe to global favorites cache changes (so a heart click elsewhere updates this card)
  useEffect(() => {
    const listener = () => force(v => v + 1)
    listeners.add(listener)
    return () => { listeners.delete(listener) }
  }, [])

  // Match by infoHash first (precise — same hash means same content); fall back
  // to name only when the result has no hash. Prevents two visually-similar
  // cards from sharing favorite state when they have distinct hashes.
  const isFavorited = isFavoriteResult(result.infoHash, result.title)

  const toggleFavorite = async (e: React.MouseEvent) => {
    e.stopPropagation()
    const wasFavorited = isFavorited
    if (wasFavorited) {
      favoriteSet.delete(result.title)
      if (result.infoHash) favoriteHashSet.delete(result.infoHash)
    } else {
      favoriteSet.add(result.title)
      if (result.infoHash) favoriteHashSet.add(result.infoHash)
    }
    listeners.forEach(fn => fn())
    try {
      if (wasFavorited) await favoriteRemove(result.title)
      else await favoriteAdd(result.title, result.infoHash, result.magnetUri, 'manual')
    } catch {
      // Revert on failure
      if (wasFavorited) {
        favoriteSet.add(result.title)
        if (result.infoHash) favoriteHashSet.add(result.infoHash)
      } else {
        favoriteSet.delete(result.title)
        if (result.infoHash) favoriteHashSet.delete(result.infoHash)
      }
      listeners.forEach(fn => fn())
    }
  }

  const handleCopyMagnet = async () => {
    if (!result.magnetUri) return
    await copyToClipboard(result.magnetUri)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  // Opens the magnet link in the OS-registered handler (qBittorrent, Transmission, etc.)
  const handleOpenMagnet = () => {
    if (result.magnetUri) {
      window.location.href = result.magnetUri
    }
  }

  const handleTorrentDownload = () => {
    if (result.link) {
      window.open(result.link, '_blank')
    }
  }

  const hasMagnet = Boolean(result.magnetUri)
  const hasTorrent = Boolean(result.link)
  const hasSource = hasMagnet || hasTorrent  // either is enough — streamer.Add accepts both
  const canDownload = hasMagnet || hasTorrent
  const canPlay = hasSource && onPlay && isPlayable(result)

  // Card-wide click → opens contents. Action buttons stopPropagation to not double-trigger.
  const cardClickable = hasSource && !!onExploreContents
  const handleCardClick = cardClickable ? () => onExploreContents!(result) : undefined

  return (
    <div
      ref={cardRef}
      onClick={handleCardClick}
      onKeyDown={cardClickable ? (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          handleCardClick?.()
        }
      } : undefined}
      role={cardClickable ? 'button' : undefined}
      tabIndex={cardClickable ? 0 : undefined}
      className={`card flex flex-col gap-3 ${
        cardClickable
          ? 'cursor-pointer hover:border-green-500/40 hover:bg-gray-800/80 active:bg-gray-800/60 transition-all focus-visible:ring-2 focus-visible:ring-green-500 focus:outline-none'
          : ''
      }`}
      style={cardClickable ? { WebkitTapHighlightColor: 'rgba(16, 185, 129, 0.15)' } : undefined}
      title={cardClickable ? 'Toque pra ver arquivos no torrent' : undefined}
    >
      {/* Title (+ TMDB poster when matched) */}
      <div className="flex items-start justify-between gap-2">
        {tmdb?.posterUrl && (
          <img
            src={tmdb.posterUrl}
            alt={tmdb.title}
            loading="lazy"
            className="w-12 h-[72px] sm:w-14 sm:h-[84px] rounded object-cover flex-shrink-0 border border-gray-700 bg-gray-900"
          />
        )}
        <h3
          className={`text-sm font-medium text-gray-100 line-clamp-2 flex-1 ${cardClickable ? 'hover:text-green-400' : ''}`}
          title={tmdb ? `${tmdb.title}${tmdb.year ? ' (' + tmdb.year + ')' : ''} — ${tmdb.overview}` : result.title}
        >
          {result.title}
          {tmdb && (
            <span className="block text-[11px] font-normal text-gray-400 mt-0.5 line-clamp-2">
              {tmdb.kind === 'tv' ? '📺' : '🎬'} {tmdb.title}{tmdb.year ? ` (${tmdb.year})` : ''}
              {tmdb.voteAverage > 0 && <span className="text-amber-400 ml-1">★ {tmdb.voteAverage.toFixed(1)}</span>}
            </span>
          )}
        </h3>
        <div
          className="flex flex-col items-end gap-1 flex-shrink-0"
          onClick={(e) => e.stopPropagation()}
        >
          <div className="flex items-center gap-1.5">
            <button
              onClick={toggleFavorite}
              title={isFavorited ? 'Remover dos favoritos' : 'Marcar como favorito'}
              className={`transition-colors ${isFavorited ? 'text-pink-400 hover:text-pink-300' : 'text-gray-600 hover:text-pink-400'}`}
            >
              <Heart className={`w-3.5 h-3.5 ${isFavorited ? 'fill-current' : ''}`} />
            </button>
            <span className="text-xs bg-green-500/20 text-green-400 border border-green-500/30 px-2 py-0.5 rounded-full whitespace-nowrap">
              {result.tracker}
            </span>
          </div>
          {result.cached && (
            <span className="text-xs bg-blue-500/20 text-blue-400 border border-blue-500/30 px-2 py-0.5 rounded-full whitespace-nowrap">
              cache
            </span>
          )}
        </div>
      </div>

      {/* Quality badges */}
      <QualityBadges quality={result.quality} />

      {/* Category + multi-tracker badge */}
      <div className="flex items-center gap-2 text-xs text-gray-400 flex-wrap">
        {result.category && (
          <span className="flex items-center gap-1"><Tag className="w-3 h-3" />{result.category}</span>
        )}
        {result.alsoIn && result.alsoIn.length > 0 && (
          <span className="flex items-center gap-1 text-indigo-400" title={`Mesmo torrent em: ${result.alsoIn.join(', ')}`}>
            <Globe className="w-3 h-3" />
            +{result.alsoIn.length} tracker{result.alsoIn.length !== 1 ? 's' : ''}
          </span>
        )}
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 gap-2 text-xs">
        <div className="flex items-center gap-1 text-gray-400">
          <HardDrive className="w-3.5 h-3.5" />
          <span>{formatSize(result.size)}</span>
        </div>
        <div className="flex items-center gap-1 text-gray-400">
          <Clock className="w-3.5 h-3.5" />
          <span>{result.age}</span>
        </div>
        <div className="flex items-center gap-1 text-green-400">
          <Users className="w-3.5 h-3.5" />
          <span>{result.seeders} seed</span>
        </div>
        <div className="flex items-center gap-1 text-red-400">
          <TrendingDown className="w-3.5 h-3.5" />
          <span>{result.leechers} leech</span>
        </div>
      </div>

      {/* Actions — stopPropagation prevents the card-wide click handler from also firing */}
      <div
        className="flex gap-1.5 mt-auto pt-1 border-t border-gray-700 flex-wrap"
        onClick={(e) => e.stopPropagation()}
      >
        {canPlay && (
          <button
            onClick={() => onPlay!(result)}
            title="Reproduzir no browser via stream"
            className="flex items-center gap-1 text-xs bg-purple-500/20 hover:bg-purple-500/30 text-purple-300 border border-purple-500/30 px-2.5 py-1.5 rounded-lg transition-colors"
          >
            <Play className="w-3.5 h-3.5 fill-current" />
            Play
          </button>
        )}
        {hasSource && onExploreContents && (
          <button
            onClick={() => onExploreContents(result)}
            title="Ver arquivos dentro do torrent"
            className="flex items-center gap-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2.5 py-1.5 rounded-lg transition-colors"
          >
            <FolderOpen className="w-3.5 h-3.5" />
          </button>
        )}
        {hasSource && onAddToPlaylist && (
          <button
            onClick={() => onAddToPlaylist(result)}
            title="Adicionar a uma playlist"
            className="flex items-center gap-1 text-xs bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 border border-blue-500/30 px-2.5 py-1.5 rounded-lg transition-colors"
          >
            <ListPlus className="w-3.5 h-3.5" />
          </button>
        )}
        {hasMagnet && (
          <div className="flex items-center gap-0.5">
            {/* Open in local app */}
            <button
              onClick={handleOpenMagnet}
              title="Abrir com app associado (qBittorrent, etc.)"
              className="flex items-center gap-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 pl-2.5 pr-2 py-1.5 rounded-l-lg transition-colors border-r border-gray-600"
            >
              <Magnet className="w-3.5 h-3.5" />
              Magnet
            </button>
            {/* Copy to clipboard */}
            <button
              onClick={handleCopyMagnet}
              title="Copiar link magnet"
              className={`flex items-center px-2 py-1.5 rounded-r-lg transition-colors text-xs ${
                copied
                  ? 'bg-green-500/20 text-green-400'
                  : 'bg-gray-700 hover:bg-gray-600 text-gray-400'
              }`}
            >
              {copied ? <Check className="w-3.5 h-3.5" /> : <Clipboard className="w-3.5 h-3.5" />}
            </button>
          </div>
        )}
        {hasTorrent && (
          <button
            onClick={handleTorrentDownload}
            title="Baixar arquivo .torrent"
            className="flex items-center gap-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2.5 py-1.5 rounded-lg transition-colors"
          >
            <FileDown className="w-3.5 h-3.5" />
            .torrent
          </button>
        )}
        {canDownload && (
          <button
            onClick={() => onDownload(result)}
            title="Enviar para cliente de download (qBittorrent/Deluge)"
            className="flex items-center gap-1 text-xs btn-primary py-1.5 px-2.5 flex-1 justify-center min-w-[80px]"
          >
            <ExternalLink className="w-3.5 h-3.5" />
            Cliente
          </button>
        )}
      </div>
    </div>
  )
}
