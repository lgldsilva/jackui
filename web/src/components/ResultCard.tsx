import { memo, useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Magnet, Users, TrendingDown, Clock, HardDrive, Tag, Check, FileDown, Clipboard, ExternalLink, Play, Globe, Heart, ListPlus, FolderOpen, RefreshCw, HardDriveDownload, Loader2 } from 'lucide-react'
import MoreActionsMenu, { type MoreActionItem } from './MoreActionsMenu'
import { SearchResult, TmdbMatch, favoriteAdd, favoriteRemove, tmdbMatch, convertTorrentToMagnet, downloadTorrentForResult } from '../api/client'
import { buildFavoritePayload } from '../lib/favoritePayload'
import i18n from '../lib/i18n'
import { playHref, newTabProps, swallowClick } from '../lib/cardNav'
import { formatBytes } from '../lib/format'
import { notify, notifyError } from './Toast'
import QualityBadges from './QualityBadges'
import SeedBadge from './SeedBadge'


// Backwards-compat no-op shim. Antes da onda 3, SearchPage seedava o estado
// de "favorito" via cache module-scope; agora o backend já entrega
// `result.isFavorited` em cada SearchResult, então esta função existe só
// pra não quebrar o import enquanto a chamada estiver no fluxo.
export function refreshFavoritesCache(_entries: unknown): void { /* no-op backwards compat */ }

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

async function copyToClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const el = document.createElement('textarea')
    el.value = text
    document.body.appendChild(el)
    el.select()
    // execCommand é depreciado, mas é o único fallback de cópia quando
    // navigator.clipboard falha (contexto não-HTTPS / browser antigo).
    document.execCommand('copy') // NOSONAR
    el.remove()
  }
}

async function resolveMagnetIfNeeded(
  result: SearchResult,
  setResolving: (r: boolean) => void
): Promise<string | undefined> {
  let magnet = result.magnetUri
  if (!magnet && result.link) {
    setResolving(true)
    try {
      const conv = await convertTorrentToMagnet(result.link)
      result.magnetUri = conv.magnet
      result.infoHash = conv.infoHash
      magnet = conv.magnet
    } catch (err: unknown) {
      notifyError(err)
    } finally {
      setResolving(false)
    }
  }
  return magnet
}

function useTmdbMatch(title: string) {
  const [tmdb, setTmdb] = useState<TmdbMatch | null>(null)
  // HTMLElement: the card wrapper is a focusable/clickable <div>.
  const cardRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!cardRef.current) return
    const obs = new IntersectionObserver((entries, observer) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        observer.disconnect()
        tmdbMatch(title).then(m => { if (m) setTmdb(m) })
        return
      }
    }, { rootMargin: '120px' })
    obs.observe(cardRef.current)
    return () => obs.disconnect()
  }, [title])

  return { tmdb, cardRef }
}

async function handleToggleFavorite(
  result: SearchResult,
  isFavorited: boolean,
  setFavOpt: (fav: boolean | null) => void,
  setFavResolving: (r: boolean) => void,
) {
  const wasFavorited = isFavorited
  setFavOpt(!wasFavorited)
  try {
    if (wasFavorited) {
      await favoriteRemove(result.title)
      return
    }
    // Quick-favorite must link magnet/infoHash like the full open-card flow,
    // or the favorite is inert on FavoritesPage (Play/Download need fav.magnet).
    // History rows from private trackers often carry only the .torrent link, so
    // this may hit the backend converter — hence the spinner on the heart.
    setFavResolving(true)
    let payload
    try {
      payload = await buildFavoritePayload(result, convertTorrentToMagnet)
    } finally {
      setFavResolving(false)
    }
    if (payload.source === 'none') {
      // Nothing to link (no magnet, no infoHash, no .torrent link): saving would
      // create an inert favorite that can never Play. Revert + tell the user
      // instead of silently persisting a dead row.
      setFavOpt(wasFavorited)
      notify(i18n.t('favorites.resolve_failed'), 'error')
      return
    }
    if (payload.source === 'link' && payload.magnet.startsWith('magnet:')) {
      // Backfill the resolved magnet so Play/Magnet/.torrent on this card reuse it
      // (same mutation resolveMagnetIfNeeded already does). Skip when the payload
      // kept the raw .torrent link — result.link already drives Play in that case.
      result.magnetUri = payload.magnet
      result.infoHash = payload.infoHash
    }
    await favoriteAdd(result.title, payload.infoHash, payload.magnet, 'manual')
  } catch {
    setFavOpt(wasFavorited)
  }
}

async function startTorrentDownload(
  result: SearchResult,
  setResolvingTorrent: (r: boolean) => void
) {
  if (!result.link && !result.magnetUri) return
  setResolvingTorrent(true)
  try {
    await downloadTorrentForResult(result)
  } catch (err: unknown) {
    notifyError(err)
  } finally {
    setResolvingTorrent(false)
  }
}


function RatingBadge({ tmdb }: { readonly tmdb: TmdbMatch | null }): React.ReactNode {
  if (!tmdb) return null
  if (tmdb.imdbRating && tmdb.imdbRating > 0) {
    if (tmdb.imdbId) {
      return (
        <a
          href={`https://www.imdb.com/title/${tmdb.imdbId}`}
          target="_blank"
          rel="noopener noreferrer"
          onClick={e => e.stopPropagation()}
          className="text-amber-400 ml-1 hover:underline"
          title={i18n.t('search.open_imdb')}
        >★ {tmdb.imdbRating.toFixed(1)} IMDb</a>
      )
    }
    return <span className="text-amber-400 ml-1">★ {tmdb.imdbRating.toFixed(1)} IMDb</span>
  }
  if (tmdb.voteAverage > 0) {
    return <span className="text-amber-400 ml-1" title={i18n.t('search.tmdb_rating')}>★ {tmdb.voteAverage.toFixed(1)} TMDB</span>
  }
  return null
}

function renderFavoriteIcon(favResolving: boolean, isFavorited: boolean): JSX.Element {
  if (favResolving) return <Loader2 className="w-3.5 h-3.5 animate-spin text-pink-400" />
  return <Heart className={`w-3.5 h-3.5 ${isFavorited ? 'fill-current' : ''}`} />
}

function renderArtSection(tmdb: TmdbMatch | null): React.ReactNode {
  if (!tmdb?.posterUrl) return null
  return (
    <img src={tmdb.posterUrl} alt={tmdb.title} loading="lazy" className="w-12 h-[72px] sm:w-14 sm:h-[84px] rounded object-cover flex-shrink-0 border border-default bg-surface" />
  )
}

function renderCardTitle(
  tmdb: TmdbMatch | null,
  result: SearchResult,
  isFavorited: boolean,
  cardClickable: boolean,
  titleAttr: string,
  toggleFavorite: (e: React.MouseEvent) => void,
  favResolving: boolean,
): React.ReactNode {
  return (
    <div className="flex items-start justify-between gap-2">
      {renderArtSection(tmdb)}
      <h3 className={`text-sm font-medium text-text-primary line-clamp-2 flex-1 min-w-0 break-words ${cardClickable ? 'hover:text-green-400' : ''}`} title={titleAttr}>
        {result.title}
        {tmdb && (
          <span className="block text-[11px] font-normal text-text-secondary mt-0.5 line-clamp-2">
              {tmdb.kind === 'tv' ? '📺' : '🎬'} {tmdb.title}{tmdb.year ? ` (${tmdb.year})` : ''}
            {RatingBadge({ tmdb })}
          </span>
        )}
      </h3>
      <div className="flex flex-col items-end gap-1 min-w-0 max-w-[45%] overflow-hidden">
        <div className="flex items-center gap-1.5 min-w-0 max-w-full">
          {/* p-2/-m-2 widens the touch target (~30px) for the finger without
              shifting the compact header layout — the negative margin cancels
              the padding so neighbours stay put. */}
          <button onClick={toggleFavorite} disabled={favResolving} title={isFavorited ? i18n.t('search.unfavorite') : i18n.t('search.favorite')} className={`p-1.5 flex-shrink-0 transition-colors ${isFavorited ? 'text-pink-400 hover:text-pink-500 dark:hover:text-pink-300' : 'text-text-muted hover:text-pink-400'}`}>
            {renderFavoriteIcon(favResolving, isFavorited)}
          </button>
          <span title={result.tracker} className="text-xs bg-green-500/20 text-green-400 border border-green-500/30 px-2 py-0.5 rounded-full truncate min-w-0 max-w-[5.5rem]">{result.tracker}</span>
        </div>
        {result.cached && <span className="text-xs bg-blue-500/20 text-blue-400 border border-blue-500/30 px-2 py-0.5 rounded-full whitespace-nowrap">cache</span>}
        {result.isDownloaded && (
          <span className="text-xs bg-cyan-500/20 text-cyan-700 dark:text-cyan-300 border border-cyan-500/30 px-2 py-0.5 rounded-full whitespace-nowrap flex items-center gap-1">
            <HardDriveDownload className="w-3 h-3" />{i18n.t('search.downloaded')}
          </span>
        )}
      </div>
    </div>
  )
}

function renderCategoryBadges(result: SearchResult): React.ReactNode {
  return (
    <div className="flex items-center gap-2 text-xs text-text-secondary flex-wrap">
      {result.category && <span className="flex items-center gap-1"><Tag className="w-3 h-3" />{result.category}</span>}
      {result.alsoIn && result.alsoIn.length > 0 && (
        <span className="flex items-center gap-1 text-indigo-400" title={i18n.t('search.same_torrent_in', { trackers: result.alsoIn.join(', ') })}>
          <Globe className="w-3 h-3" />+{result.alsoIn.length} tracker{result.alsoIn.length === 1 ? '' : 's'}
        </span>
      )}
    </div>
  )
}

function renderCardStats(
  result: SearchResult,
  onRefresh: ((result: SearchResult) => Promise<void> | void) | undefined,
  refreshing: boolean | undefined,
  refreshedAt: string | null | undefined,
): React.ReactNode {
  return (
    <div className="grid grid-cols-2 gap-2 text-xs">
      <div className="flex items-center gap-1 text-text-secondary"><HardDrive className="w-3.5 h-3.5" /><span>{formatBytes(result.size)}</span></div>
      <div className="flex items-center gap-1 text-text-secondary"><Clock className="w-3.5 h-3.5" /><span>{result.age}</span></div>
      <div className="flex items-center gap-1 text-green-400">
        <Users className="w-3.5 h-3.5" /><span>{result.seeders} seed</span>
        {/* Real swarm size via tracker scrape — the Jackett count above is the
            indexer's, which over/under-reports. Click to verify against the tracker. */}
        {result.infoHash && <SeedBadge infoHash={result.infoHash} magnet={result.magnetUri} autoProbe className="ml-1" />}
      </div>
      <div className="flex items-center gap-1 text-red-400">
        <TrendingDown className="w-3.5 h-3.5" /><span>{result.leechers} leech</span>
        {onRefresh && result.id !== undefined && (
          <button onClick={(e) => { swallowClick(e); void onRefresh(result) }} aria-label={i18n.t('search.refresh_stats')} disabled={!!refreshing} title={refreshedAt ? i18n.t('search.updated_at', { time: refreshedAt }) : i18n.t('search.refresh_stats')} className="ml-1 inline-flex items-center text-text-muted hover:text-cyan-400 disabled:opacity-50 transition-colors">
            <RefreshCw className={`w-3 h-3 ${refreshing ? 'animate-spin text-cyan-400' : ''}`} />
          </button>
        )}
        {refreshedAt && !refreshing && <span className="text-[10px] text-cyan-500/70 ml-1">{refreshedAt}</span>}
      </div>
    </div>
  )
}

type RenderCardActionsProps = {
  canPlay: boolean
  playLinkHref: string | null
  hasSource: boolean
  canDownload: boolean
  onPlay: ((result: SearchResult) => void) | undefined
  onExploreContents: ((result: SearchResult) => void) | undefined
  onAddToPlaylist: ((result: SearchResult) => void) | undefined
  onDownload: (result: SearchResult) => void
  result: SearchResult
  handleOpenMagnet: () => void
  handleCopyMagnet: () => void
  handleTorrentDownload: () => void
  resolvingMagnet: boolean
  resolvingTorrent: boolean
  copied: boolean
}

function buildCardMoreItems(props: RenderCardActionsProps): MoreActionItem[] {
  const {
    hasSource, onExploreContents, onAddToPlaylist, result,
    handleOpenMagnet, handleCopyMagnet, handleTorrentDownload,
    resolvingMagnet, resolvingTorrent, copied,
  } = props
  const items: MoreActionItem[] = []
  if (hasSource && onExploreContents) {
    items.push({
      id: 'explore',
      label: i18n.t('search.explore_files'),
      icon: <FolderOpen className="w-3.5 h-3.5 flex-shrink-0" />,
      onClick: () => onExploreContents(result),
    })
  }
  if (hasSource && onAddToPlaylist) {
    items.push({
      id: 'playlist',
      label: i18n.t('search.add_to_playlist'),
      icon: <ListPlus className="w-3.5 h-3.5 flex-shrink-0" />,
      onClick: () => onAddToPlaylist(result),
    })
  }
  if (!hasSource) return items
  items.push(
    {
      id: 'magnet-open',
      label: i18n.t('search.open_magnet'),
      icon: resolvingMagnet ? undefined : <Magnet className="w-3.5 h-3.5 flex-shrink-0" />,
      disabled: resolvingMagnet,
      onClick: handleOpenMagnet,
    },
    {
      id: 'magnet-copy',
      label: copied ? i18n.t('search.copied') : i18n.t('search.copy_magnet'),
      icon: copied ? <Check className="w-3.5 h-3.5 flex-shrink-0 text-green-400" /> : <Clipboard className="w-3.5 h-3.5 flex-shrink-0" />,
      disabled: resolvingMagnet,
      onClick: handleCopyMagnet,
    },
    {
      id: 'torrent',
      label: i18n.t('search.download_torrent'),
      icon: resolvingTorrent ? undefined : <FileDown className="w-3.5 h-3.5 flex-shrink-0" />,
      disabled: resolvingTorrent,
      onClick: handleTorrentDownload,
    },
  )
  return items
}

function renderCardActions(props: RenderCardActionsProps): React.ReactNode {
  const { canPlay, playLinkHref, canDownload, onPlay, onDownload, result } = props
  const playNavProps = canPlay && playLinkHref ? newTabProps(playLinkHref, () => onPlay?.(result)) : null
  const moreItems = buildCardMoreItems(props)

  return (
    <div className="flex gap-1.5 mt-auto pt-1 border-t border-default flex-wrap items-center">
      {canPlay && (
          <button
            onClick={(e) => {
              swallowClick(e)
              if (playNavProps) {
                playNavProps.onClick(e)
                return
              }
              onPlay?.(result)
            }}
            onAuxClick={playNavProps ? (e) => {
              swallowClick(e)
              playNavProps.onAuxClick(e)
            } : undefined}
            title={i18n.t('search.play_stream')} className="flex items-center gap-1 text-xs bg-purple-500/20 hover:bg-purple-500/30 text-purple-700 dark:text-purple-300 border border-purple-500/30 px-2.5 py-1.5 rounded-lg transition-colors">
          <Play className="w-3.5 h-3.5 fill-current" />Play
        </button>
      )}
      {canDownload && (
        <button onClick={(e) => { swallowClick(e); onDownload(result) }} title={i18n.t('search.send_to_client')} className="flex items-center gap-1 text-xs btn-primary py-1.5 px-2.5 flex-1 justify-center min-w-[80px]">
          <ExternalLink className="w-3.5 h-3.5" />{i18n.t('search.client')}
        </button>
      )}
      {moreItems.length > 0 && <MoreActionsMenu items={moreItems} className="ml-auto" />}
    </div>
  )
}

type CardShell = {
  handleCardClick: (() => void) | undefined
  cardClickable: boolean
  titleAttr: string
  cardClass: string
  cardTapStyle: { WebkitTapHighlightColor: string } | undefined
  cardTitle: string | undefined
  interactiveProps: {
    role?: 'button'
    tabIndex?: number
    onClick?: () => void
    onKeyDown?: (e: React.KeyboardEvent<HTMLDivElement>) => void
  }
}

function buildCardShell(
  result: SearchResult,
  tmdb: TmdbMatch | null,
  canPlay: boolean,
  hasSource: boolean,
  onPlay: ((result: SearchResult) => void) | undefined,
  onExploreContents: ((result: SearchResult) => void) | undefined,
): CardShell {
  let handleCardClick: (() => void) | undefined
  if (canPlay) {
    handleCardClick = () => onPlay?.(result)
  } else if (hasSource && onExploreContents) {
    handleCardClick = () => onExploreContents(result)
  }
  const cardClickable = handleCardClick !== undefined
  const handleCardKeyDown = handleCardClick
    ? (e: React.KeyboardEvent<HTMLDivElement>) => {
        if (e.target !== e.currentTarget) return
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          handleCardClick()
        }
      }
    : undefined
  let titleAttr: string
  if (tmdb) {
    const yearStr = tmdb.year ? ` (${tmdb.year})` : ''
    titleAttr = `${tmdb.title}${yearStr} — ${tmdb.overview}`
  } else {
    titleAttr = result.title
  }
  const cardClass = `card flex flex-col gap-3 text-left ${
    cardClickable
      ? 'cursor-pointer hover:border-green-500/40 hover:bg-surface-secondary/80 active:bg-surface-secondary/60 transition-all focus-visible:ring-2 focus-visible:ring-green-500 focus:outline-none'
      : 'cursor-default'
  }`
  let cardTitle: string | undefined
  if (canPlay) {
    cardTitle = i18n.t('search.play_stream')
  } else if (cardClickable) {
    cardTitle = i18n.t('search.tap_explore')
  }
  return {
    handleCardClick,
    cardClickable,
    titleAttr,
    cardClass,
    cardTapStyle: cardClickable ? { WebkitTapHighlightColor: 'rgba(16, 185, 129, 0.15)' } : undefined,
    cardTitle,
    interactiveProps: cardClickable
      ? { role: 'button' as const, tabIndex: 0, onClick: handleCardClick, onKeyDown: handleCardKeyDown }
      : {},
  }
}

function useResultCardActions(result: SearchResult) {
  const [copied, setCopied] = useState(false)
  const [resolvingMagnet, setResolvingMagnet] = useState(false)
  const [resolvingTorrent, setResolvingTorrent] = useState(false)
  const [favOpt, setFavOpt] = useState<boolean | null>(null)
  const [favResolving, setFavResolving] = useState(false)
  const isFavorited = favOpt ?? (result.isFavorited ?? false)

  const toggleFavorite = (e: React.MouseEvent) => {
    swallowClick(e)
    if (favResolving) return
    handleToggleFavorite(result, isFavorited, setFavOpt, setFavResolving).catch(() => { /* optimistic UI reverts on error */ })
  }
  const handleCopyMagnet = async () => {
    const magnet = await resolveMagnetIfNeeded(result, setResolvingMagnet)
    if (!magnet) return
    await copyToClipboard(magnet)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }
  const handleOpenMagnet = async () => {
    const magnet = await resolveMagnetIfNeeded(result, setResolvingMagnet)
    if (magnet) globalThis.location.href = magnet
  }
  const handleTorrentDownload = () => {
    startTorrentDownload(result, setResolvingTorrent).catch(() => { /* notifyError inside */ })
  }
  return {
    isFavorited, favResolving, toggleFavorite,
    copied, resolvingMagnet, resolvingTorrent,
    handleCopyMagnet, handleOpenMagnet, handleTorrentDownload,
  }
}

export default memo(function ResultCard({ result, onDownload, onPlay, onAddToPlaylist, onExploreContents, onRefresh, refreshing, refreshedAt }: ResultCardProps) {
  // Subscribe to language changes so this card (and its i18n.t()-driven helpers)
  // re-renders when the user switches locale.
  useTranslation()
  const { tmdb, cardRef } = useTmdbMatch(result.title)
  const actions = useResultCardActions(result)

  const hasSource = Boolean(result.magnetUri || result.link)
  const canDownload = hasSource
  // result.playable vem do backend. Fallback `true` mantém comportamento legacy
  // para syntheticResult / deep links que constroem SearchResult sem o campo.
  const canPlay = !!(hasSource && onPlay && (result.playable ?? true))
  const playLinkHref = canPlay && result.infoHash ? playHref(result.infoHash) : null
  const shell = buildCardShell(result, tmdb, canPlay, hasSource, onPlay, onExploreContents)

  return (
    <div
      ref={(el) => { cardRef.current = el }}
      className={shell.cardClass}
      style={shell.cardTapStyle}
      title={shell.cardTitle}
      {...shell.interactiveProps}
    >
      {renderCardTitle(tmdb, result, actions.isFavorited, shell.cardClickable, shell.titleAttr, actions.toggleFavorite, actions.favResolving)}
      <QualityBadges quality={result.quality} />
      {renderCategoryBadges(result)}
      {renderCardStats(result, onRefresh, refreshing, refreshedAt)}
      {renderCardActions({
        canPlay, playLinkHref, hasSource, canDownload, onPlay, onExploreContents, onAddToPlaylist, onDownload, result,
        handleOpenMagnet: actions.handleOpenMagnet,
        handleCopyMagnet: actions.handleCopyMagnet,
        handleTorrentDownload: actions.handleTorrentDownload,
        resolvingMagnet: actions.resolvingMagnet,
        resolvingTorrent: actions.resolvingTorrent,
        copied: actions.copied,
      })}
    </div>
  )
})
