import { useState, useEffect, useRef } from 'react'
import { Magnet, Users, TrendingDown, Clock, HardDrive, Tag, Check, FileDown, Clipboard, ExternalLink, Play, Globe, Heart, ListPlus, FolderOpen, RefreshCw, HardDriveDownload, Loader2 } from 'lucide-react'
import { SearchResult, TmdbMatch, favoriteAdd, favoriteRemove, tmdbMatch, convertTorrentToMagnet, convertMagnetToTorrentUrl } from '../api/client'
import QualityBadges from './QualityBadges'


// Backwards-compat no-op shim. Antes da onda 3, SearchPage seedava o estado
// de "favorito" via cache module-scope; agora o backend já entrega
// `result.isFavorited` em cada SearchResult, então esta função existe só
// pra não quebrar o import enquanto a chamada estiver no fluxo.
export function refreshFavoritesCache(_entries: unknown): void {}

type ResultCardProps = {
  readonly result: SearchResult
  readonly onDownload: (result: SearchResult) => void
  readonly onPlay?: (result: SearchResult) => void
  readonly onAddToPlaylist?: (result: SearchResult) => void
  readonly onExploreContents?: (result: SearchResult) => void
  readonly onRefresh?: (result: SearchResult) => Promise<void> | void
  readonly refreshing?: boolean
  readonly refreshedAt?: string | null
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

type ActionsBarProps = {
  readonly canPlay: boolean
  readonly onPlay: ((result: SearchResult) => void) | undefined
  readonly result: SearchResult
  readonly hasSource: boolean
  readonly onExploreContents: ((result: SearchResult) => void) | undefined
  readonly onAddToPlaylist: ((result: SearchResult) => void) | undefined
  readonly handleOpenMagnet: () => Promise<void>
  readonly resolvingMagnet: boolean
  readonly handleCopyMagnet: () => Promise<void>
  readonly copied: boolean
  readonly handleTorrentDownload: () => Promise<void>
  readonly resolvingTorrent: boolean
  readonly canDownload: boolean
  readonly onDownload: (result: SearchResult) => void
}

function ActionsBar(props: ActionsBarProps) {
  const {
    canPlay, onPlay, result,
    hasSource, onExploreContents, onAddToPlaylist,
    handleOpenMagnet, resolvingMagnet, handleCopyMagnet, copied,
    handleTorrentDownload, resolvingTorrent, canDownload, onDownload,
  } = props
  const playBtn = canPlay && onPlay ? (
    <button
      onClick={(e) => { e.stopPropagation(); onPlay(result) }}
      title="Reproduzir no browser via stream"
      className="flex items-center gap-1 text-xs bg-purple-500/20 hover:bg-purple-500/30 text-purple-300 border border-purple-500/30 px-2.5 py-1.5 rounded-lg transition-colors"
    >
      <Play className="w-3.5 h-3.5 fill-current" />
      Play
    </button>
  ) : null
  const exploreBtn = hasSource && onExploreContents ? (
    <button
      onClick={(e) => { e.stopPropagation(); onExploreContents(result) }}
      title="Ver arquivos dentro do torrent"
      className="flex items-center gap-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2.5 py-1.5 rounded-lg transition-colors"
    >
      <FolderOpen className="w-3.5 h-3.5" />
    </button>
  ) : null
  const playlistBtn = hasSource && onAddToPlaylist ? (
    <button
      onClick={(e) => { e.stopPropagation(); onAddToPlaylist(result) }}
      title="Adicionar a uma playlist"
      className="flex items-center gap-1 text-xs bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 border border-blue-500/30 px-2.5 py-1.5 rounded-lg transition-colors"
    >
      <ListPlus className="w-3.5 h-3.5" />
    </button>
  ) : null
  const magnetGroup = hasSource ? (
    <div className="flex items-center gap-0.5">
      <button
        onClick={(e) => { e.stopPropagation(); handleOpenMagnet() }}
        disabled={resolvingMagnet}
        title="Abrir com app associado (qBittorrent, etc.)"
        className="flex items-center gap-1 text-xs bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-300 pl-2.5 pr-2 py-1.5 rounded-l-lg transition-colors border-r border-gray-600"
      >
        {resolvingMagnet ? (
          <Loader2 className="w-3.5 h-3.5 animate-spin text-cyan-400" />
        ) : (
          <Magnet className="w-3.5 h-3.5" />
        )}
        Magnet
      </button>
      <button
        onClick={(e) => { e.stopPropagation(); handleCopyMagnet() }}
        disabled={resolvingMagnet}
        title="Copiar link magnet"
        className={`flex items-center px-2 py-1.5 rounded-r-lg transition-colors text-xs ${
          copied
            ? 'bg-green-500/20 text-green-400'
            : 'bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-400'
        }`}
      >
        {copied ? (
          <Check className="w-3.5 h-3.5" />
        ) : resolvingMagnet ? (
          <Loader2 className="w-3.5 h-3.5 animate-spin text-cyan-400" />
        ) : (
          <Clipboard className="w-3.5 h-3.5" />
        )}
      </button>
    </div>
  ) : null
  const torrentBtn = hasSource ? (
    <button
      onClick={(e) => { e.stopPropagation(); handleTorrentDownload() }}
      disabled={resolvingTorrent}
      title="Baixar arquivo .torrent"
      className="flex items-center gap-1 text-xs bg-gray-700 hover:bg-gray-600 disabled:opacity-50 text-gray-300 px-2.5 py-1.5 rounded-lg transition-colors"
    >
      {resolvingTorrent ? (
        <Loader2 className="w-3.5 h-3.5 animate-spin text-cyan-400" />
      ) : (
        <FileDown className="w-3.5 h-3.5" />
      )}
      .torrent
    </button>
  ) : null
  const clientBtn = canDownload ? (
    <button
      onClick={(e) => { e.stopPropagation(); onDownload(result) }}
      title="Enviar para cliente de download (qBittorrent/Deluge)"
      className="flex items-center gap-1 text-xs btn-primary py-1.5 px-2.5 flex-1 justify-center min-w-[80px]"
    >
      <ExternalLink className="w-3.5 h-3.5" />
      Cliente
    </button>
  ) : null
  return (
    <div className="flex gap-1.5 mt-auto pt-1 border-t border-gray-700 flex-wrap">
      {playBtn}
      {exploreBtn}
      {playlistBtn}
      {magnetGroup}
      {torrentBtn}
      {clientBtn}
    </div>
  )
}

function renderRatingBadge(tmdb: TmdbMatch) {
  if (tmdb.imdbRating && tmdb.imdbRating > 0) {
    return tmdb.imdbId ? (
      <a
        href={`https://www.imdb.com/title/${tmdb.imdbId}`}
        target="_blank"
        rel="noopener noreferrer"
        onClick={e => e.stopPropagation()}
        className="text-amber-400 ml-1 hover:underline"
        title="Abrir no IMDb"
      >★ {tmdb.imdbRating.toFixed(1)} IMDb</a>
    ) : (
      <span className="text-amber-400 ml-1">★ {tmdb.imdbRating.toFixed(1)} IMDb</span>
    )
  }
  if (tmdb.voteAverage > 0) {
    return <span className="text-amber-400 ml-1" title="Nota TMDB">★ {tmdb.voteAverage.toFixed(1)} TMDB</span>
  }
  return null
}

export default function ResultCard(props: ResultCardProps) {
  const { result, onDownload, onPlay, onAddToPlaylist, onExploreContents, onRefresh, refreshing, refreshedAt } = props
  const [copied, setCopied] = useState(false)
  const [resolvingMagnet, setResolvingMagnet] = useState(false)
  const [resolvingTorrent, setResolvingTorrent] = useState(false)
  // Optimistic favorite toggle. null = exibe o valor canônico do backend

  // (result.isFavorited); true/false sobrescreve até o request voltar.
  // Em failure restauramos pra null pra cair de volta no canônico.
  const [favOpt, setFavOpt] = useState<boolean | null>(null)
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

  // Canônico vem do backend; favOpt sobrescreve enquanto o toggle estiver em
  // voo (otimismo na UI). Backend resolve favorited por infoHash (preciso) ou
  // por name (fallback p/ entradas legacy sem hash) — sem matching ambíguo
  // entre torrents com mesmo título.
  const isFavorited = favOpt ?? result.isFavorited ?? false

  const toggleFavorite = async (e: React.MouseEvent) => {
    e.stopPropagation()
    const wasFavorited = isFavorited
    setFavOpt(!wasFavorited)
    try {
      if (wasFavorited) await favoriteRemove(result.title)
      else await favoriteAdd(result.title, result.infoHash, result.magnetUri, 'manual')
    } catch {
      setFavOpt(wasFavorited) // revert
    }
  }

  const handleCopyMagnet = async () => {
    let magnet = result.magnetUri
    if (!magnet && result.link) {
      setResolvingMagnet(true)
      try {
        const conv = await convertTorrentToMagnet(result.link)
        magnet = conv.magnet
        result.magnetUri = conv.magnet
        result.infoHash = conv.infoHash
      } catch (err: any) {
        alert(`Erro ao obter magnet do torrent: ${err.message || err}`)
        setResolvingMagnet(false)
        return
      }
      setResolvingMagnet(false)
    }

    if (!magnet) return
    await copyToClipboard(magnet)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  // Opens the magnet link in the OS-registered handler (qBittorrent, Transmission, etc.)
  const handleOpenMagnet = async () => {
    let magnet = result.magnetUri
    if (!magnet && result.link) {
      setResolvingMagnet(true)
      try {
        const conv = await convertTorrentToMagnet(result.link)
        magnet = conv.magnet
        result.magnetUri = conv.magnet
        result.infoHash = conv.infoHash
      } catch (err: any) {
        alert(`Erro ao obter magnet do torrent: ${err.message || err}`)
        setResolvingMagnet(false)
        return
      }
      setResolvingMagnet(false)
    }

    if (magnet) {
      window.location.href = magnet
    }
  }

  const handleTorrentDownload = async () => {
    if (result.link) {
      window.location.href = `/api/proxy/torrent?url=${encodeURIComponent(result.link)}`
      return
    }

    if (result.magnetUri) {
      setResolvingTorrent(true)
      try {
        const downloadUrl = convertMagnetToTorrentUrl(result.magnetUri)
        window.location.href = downloadUrl
      } catch (err: any) {
        alert(`Erro ao converter magnet para torrent: ${err.message || err}`)
      } finally {
        setTimeout(() => {
          setResolvingTorrent(false)
        }, 4000)
      }
    }
  }

  const hasMagnet = Boolean(result.magnetUri)
  const hasTorrent = Boolean(result.link)
  const hasSource = hasMagnet || hasTorrent  // either is enough — streamer.Add accepts both
  const canDownload = hasMagnet || hasTorrent
  // result.playable vem do backend. Fallback `true` mantém comportamento legacy
  // para syntheticResult / deep links que constroem SearchResult sem o campo.
  const canPlay = !!(hasSource && onPlay && (result.playable ?? true))

  // Card-wide click → opens contents. Action buttons stopPropagation to not double-trigger.
  const cardClickable = hasSource && !!onExploreContents
  const handleCardClick = cardClickable ? () => onExploreContents!(result) : undefined

  const titleAttr = tmdb
    ? `${tmdb.title}${tmdb.year ? ` (${tmdb.year})` : ''} — ${tmdb.overview}`
    : result.title

  const cardClickProps = cardClickable ? {
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        handleCardClick?.()
      }
    },
    role: 'button' as const,
    tabIndex: 0,
    className: 'card flex flex-col gap-3 cursor-pointer hover:border-green-500/40 hover:bg-gray-800/80 active:bg-gray-800/60 transition-all focus-visible:ring-2 focus-visible:ring-green-500 focus:outline-none',
    style: { WebkitTapHighlightColor: 'rgba(16, 185, 129, 0.15)' } as React.CSSProperties,
    title: 'Toque pra ver arquivos no torrent',
  } : {
    onKeyDown: undefined as unknown as React.KeyboardEventHandler<HTMLDivElement>,
    role: undefined as unknown as string,
    tabIndex: undefined as unknown as number,
    className: 'card flex flex-col gap-3',
    style: undefined as unknown as React.CSSProperties,
    title: undefined as unknown as string,
  }

  return (
    <div
      ref={cardRef}
      onClick={handleCardClick}
      onKeyDown={cardClickProps.onKeyDown}
      role={cardClickProps.role}
      tabIndex={cardClickProps.tabIndex}
      className={cardClickProps.className}
      style={cardClickProps.style}
      title={cardClickProps.title}
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
          title={titleAttr}
        >
          {result.title}
              {tmdb && (
            <span className="block text-[11px] font-normal text-gray-400 mt-0.5 line-clamp-2">
              {tmdb.kind === 'tv' ? '📺' : '🎬'} {tmdb.title}{tmdb.year ? ` (${tmdb.year})` : ''}
              {renderRatingBadge(tmdb)}
            </span>
          )}
        </h3>
        <div className="flex flex-col items-end gap-1 flex-shrink-0">
          <div className="flex items-center gap-1.5">
            <button
              onClick={(e) => { e.stopPropagation(); toggleFavorite(e) }}
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
          {result.isDownloaded && (
            <span className="text-xs bg-cyan-500/20 text-cyan-300 border border-cyan-500/30 px-2 py-0.5 rounded-full whitespace-nowrap flex items-center gap-1">
              <HardDriveDownload className="w-3 h-3" />
              Baixado
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
          {/* Refresh button: only renders when the host wired up onRefresh AND
              the row carries an id. We avoid stopPropagation here because the
              card itself doesn't have a click handler at this nesting level. */}
          {onRefresh && result.id !== undefined && (
            <button
              onClick={(e) => { e.stopPropagation(); void onRefresh(result) }}
              disabled={!!refreshing}
              title={refreshedAt ? `Atualizado em ${refreshedAt}` : 'Atualizar seeders/leechers'}
              className="ml-1 inline-flex items-center text-gray-500 hover:text-cyan-400 disabled:opacity-50 transition-colors"
            >
              <RefreshCw className={`w-3 h-3 ${refreshing ? 'animate-spin text-cyan-400' : ''}`} />
            </button>
          )}
          {refreshedAt && !refreshing && (
            <span className="text-[10px] text-cyan-500/70 ml-1">{refreshedAt}</span>
          )}
        </div>
      </div>

      <ActionsBar
        canPlay={canPlay}
        onPlay={onPlay}
        result={result}
        hasSource={hasSource}
        onExploreContents={onExploreContents}
        onAddToPlaylist={onAddToPlaylist}
        handleOpenMagnet={handleOpenMagnet}
        resolvingMagnet={resolvingMagnet}
        handleCopyMagnet={handleCopyMagnet}
        copied={copied}
        handleTorrentDownload={handleTorrentDownload}
        resolvingTorrent={resolvingTorrent}
        canDownload={canDownload}
        onDownload={onDownload}
      />
    </div>
  )
}
