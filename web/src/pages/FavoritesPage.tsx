import { useState, useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Heart, Loader2, Folder, ChevronDown, Download, X, Search, CheckSquare, Square, RefreshCw } from 'lucide-react'
import {
  favoritesList, favoriteRemove, favoriteRemoveBatch, StreamFavorite,
  FavoriteFolder, folderList, folderCreate, folderRename, folderDelete, folderSetHidden,
  favoriteSetFolder, favoriteSetFolderBatch,
  streamImport, SearchResult,
} from '../api/client'
import NavHeader from '../components/NavHeader'
import DownloadModal from '../components/DownloadModal'
import PullToRefreshIndicator from '../components/PullToRefreshIndicator'
import FavoritesSortControl from '../components/FavoritesSortControl'
import TorrentContentsModal from '../components/TorrentContentsModal'
import { useScrollRestoration } from '../lib/useScrollRestoration'
import { useConfirm } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { useAuth } from '../auth/AuthContext'
import { usePullToRefresh } from '../lib/usePullToRefresh'
import { usePlayer } from '../components/PlayerProvider'
import { useRevealHidden } from '../lib/reveal'
import { SortKey, SortDir, sortFavorites } from '../lib/favSort'
import { errMessage } from '../lib/errMessage'
import { buildTree, importTorrentB64, buildImportMsg } from '../lib/favoritesTree'
import FolderSidebar from '../components/favorites/FolderSidebar'
import FavoriteCard from '../components/favorites/FavoriteCard'
import ImportSheet from '../components/favorites/ImportSheet'
import MultiSelectBar from '../components/favorites/MultiSelectBar'
import MobileFolderSheet from '../components/favorites/MobileFolderSheet'
import { pageTitle, renderFavsContent } from '../components/favorites/favoritesHelpers'

export default function FavoritesPage() {
  const { t } = useTranslation()
  const { isAdmin } = useAuth()
  const [favs, setFavs] = useState<StreamFavorite[]>([])
  const [folders, setFolders] = useState<FavoriteFolder[]>([])
  const [loading, setLoading] = useState(true)
  useScrollRestoration(!loading)
  const [error, setError] = useState('')
  // The hidden curtain is now GLOBAL: the easter egg lives on the header logo
  // (7 taps) and reveals hidden folders/favourites here as well as Continue
  // Watching, downloads and local. Session-only — see lib/reveal.
  const [revealHidden] = useRevealHidden()
  const { playSingle } = usePlayer()
  const confirm = useConfirm()
  const { notify } = useToast()
  // Favorito sendo enviado ao modal de download (destino + seleção de arquivos).
  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)
  // Dropdown de pasta no mobile (a sidebar é hidden md:block — sem isto não dá
  // pra trocar de pasta no celular).
  const [folderSheetOpen, setFolderSheetOpen] = useState(false)

  // Tree UI state
  const [selectedFolderId, setSelectedFolderId] = useState<number | null>(null)
  const ALL_VIEW = -1 as number  // sentinel for "Todos" view (no filter)
  const [viewMode, setViewMode] = useState<number | null>(ALL_VIEW)
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const [editingId, setEditingId] = useState<number | null>(null)
  const [dropOnRoot, setDropOnRoot] = useState(false) // visual flag for drop indicator on "root" zone
  const [contentsTarget, setContentsTarget] = useState<SearchResult | null>(null)
  const newFolderInput = useRef<HTMLInputElement>(null)
  const [creatingRoot, setCreatingRoot] = useState(false)

  // Search
  const [searchQuery, setSearchQuery] = useState('')

  // Sort order (date/name/seeds/size + direction). Default mirrors the legacy
  // behaviour: most recently added first.
  const [sortBy, setSortBy] = useState<SortKey>('date')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  // Silent re-fetch (no loading spinner) — keeps scroll/selection. Used after a
  // bulk seed refresh so sorting by seeds picks up the freshly probed numbers.
  const reloadFavsQuiet = async () => {
    try {
      const favsList = await favoritesList(revealHidden)
      setFavs(favsList || [])
    } catch { /* keep the current list on a transient failure */ }
  }

  // "Atualizar seeds": bump this counter to make every SeedBadge in the current
  // view re-probe the swarm at once. The backend dedupes + caps to 3 concurrent
  // probes, so firing one per visible card is safe.
  const [seedRefresh, setSeedRefresh] = useState(0)
  const [seedRefreshing, setSeedRefreshing] = useState(false)
  const refreshSeeds = () => {
    setSeedRefresh(n => n + 1)
    setSeedRefreshing(true)
    // Visual ack only — each badge owns its own probing spinner afterwards.
    setTimeout(() => setSeedRefreshing(false), 1500)
    // Probes persist the snapshot to the metadata cache; re-pull a bit later so
    // a "sort by seeds" reflects the new counts without a manual refresh.
    setTimeout(() => { void reloadFavsQuiet() }, 11000)
  }

  // Filter favorites by current view AND search query, then sort.
  const filteredFavs = useMemo(() => {
    let list = favs
    if (viewMode !== ALL_VIEW) {
      if (viewMode === null) list = list.filter(f => f.folderId === null)
      else list = list.filter(f => f.folderId === viewMode)
    }
    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase()
      list = list.filter(f => f.name.toLowerCase().includes(q))
    }
    return sortFavorites(list, sortBy, sortDir)
  }, [favs, viewMode, searchQuery, sortBy, sortDir])

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [favsList, foldersList] = await Promise.all([favoritesList(revealHidden), folderList(revealHidden)])
      setFavs(favsList || [])
      setFolders(foldersList || [])
    } catch (e: unknown) {
      setError(errMessage(e))
    } finally {
      setLoading(false)
    }
  }

  // Infinite scroll
  const PAGE_SIZE = 40
  const [visible, setVisible] = useState(PAGE_SIZE)
  const sentinelRef = useRef<HTMLDivElement>(null)

  // IntersectionObserver for infinite scroll
  useEffect(() => {
    const el = sentinelRef.current
    if (!el) return
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) {
        setVisible(prev => Math.min(prev + PAGE_SIZE, filteredFavs.length))
      }
    }, { rootMargin: '200px' })
    observer.observe(el)
    return () => observer.disconnect()
  }, [filteredFavs.length])

  // Load on mount and whenever the hidden curtain is toggled (re-fetch with/without
  // the hidden folders + their favourites).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { load() }, [revealHidden])

  const handleToggleHidden = async (id: number, hidden: boolean) => {
    await folderSetHidden(id, hidden)
    // Quando esconde a pasta selecionada sem o modo revelado, volta pra raiz.
    if (hidden && !revealHidden && viewMode === id) { setSelectedFolderId(null); setViewMode(ALL_VIEW) }
    load()
  }

  const ptr = usePullToRefresh({ onRefresh: load, disabled: loading })

  const tree = useMemo(() => buildTree(folders), [folders])

  const handleCreateRoot = async () => {
    if (!newFolderInput.current) return
    const name = newFolderInput.current.value.trim()
    if (!name) { setCreatingRoot(false); return }
    const f = await folderCreate(name, null)
    setFolders([...folders, f])
    setCreatingRoot(false)
    if (newFolderInput.current) newFolderInput.current.value = ''
  }

  // ─── Import (magnet / .torrent) ──────────────────────────────────────────
  const [showImport, setShowImport] = useState(false)
  const [magnetInput, setMagnetInput] = useState('')
  const [importing, setImporting] = useState(false)
  const [importMsg, setImportMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null)
  const [dragOverDrop, setDragOverDrop] = useState(false)

  const importMagnets = async () => {
    const lines = magnetInput.split('\n').map(l => l.trim()).filter(Boolean)
    if (lines.length === 0) return
    setImporting(true)
    setImportMsg(null)
    let ok = 0
    const fails: string[] = []
    for (const magnet of lines) {
      try {
        await streamImport({ magnet, folderId: viewMode === ALL_VIEW ? null : viewMode })
        ok++
      } catch (e: unknown) {
        fails.push(errMessage(e))
      }
    }
    setImporting(false)
    setMagnetInput('')
    setImportMsg(buildImportMsg(ok, fails.length, fails[0], '', t))
    await load()
  }

  const importTorrentFiles = async (files: File[]) => {
    const torrents = files.filter(f => f.name.toLowerCase().endsWith('.torrent'))
    const skipped = files.length - torrents.length
    if (torrents.length === 0) {
      setImportMsg({ kind: 'err', text: t('favorites.selectAtLeastOne') })
      return
    }
    setImporting(true)
    setImportMsg(null)
    const { ok, fails } = await importTorrentB64(torrents, viewMode, ALL_VIEW)
    setImporting(false)
    const suffix = skipped > 0 ? t('favorites.importSkippedSuffix', { count: skipped }) : ''
    setImportMsg(buildImportMsg(ok, fails.length, fails[0], suffix, t))
    await load()
  }

  const handleRemove = async (name: string) => {
    const ok = await confirm({ title: t('favorites.removeTitle'), message: t('favorites.removeMessage', { name }), confirmLabel: t('favorites.removeConfirm'), destructive: true })
    if (!ok) return
    await favoriteRemove(name)
    setFavs(favs.filter(f => f.name !== name))
  }

  // favToResult builds the SearchResult shape the player/contents modal expect.
  const favToResult = (f: StreamFavorite): SearchResult => ({
    title: f.name, tracker: '', categoryId: 0, category: '', size: 0, seeders: 0, leechers: 0,
    age: '', magnetUri: f.magnet, link: '', infoHash: f.infoHash, publishDate: '',
  })

  const favHasValidMagnet = (f: StreamFavorite) =>
    !!f.magnet && (f.magnet.startsWith('magnet:') || f.magnet.startsWith('http'))

  const handleFavDragStart = (e: React.DragEvent, favName: string) => {
    e.dataTransfer.setData('text/x-favorite-name', favName)
    e.dataTransfer.effectAllowed = 'move'
    const ghost = document.createElement('div')
    ghost.textContent = favName
    ghost.style.cssText = 'position:fixed;top:-1000px;left:0;max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;padding:6px 12px;background:#16a34a;color:#fff;font-size:12px;border-radius:8px;box-shadow:0 4px 12px rgba(0,0,0,.4);pointer-events:none'
    document.body.appendChild(ghost)
    e.dataTransfer.setDragImage(ghost, 12, 12)
    setTimeout(() => ghost.remove(), 0)
  }

  const playFavorite = (f: StreamFavorite) => {
    if (!favHasValidMagnet(f)) {
      notify(t('favorites.magnetInvalidPlay'), 'error')
      return
    }
    playSingle(favToResult(f))
  }

  // "Baixar": abre o modal unificado (destino + seleção de arquivos/árvore),
  // como na busca. Antes baixava o torrent inteiro direto, sem perguntar nada.
  const downloadFavorite = (fav: StreamFavorite) => {
    if (!favHasValidMagnet(fav)) {
      notify(t('favorites.magnetInvalidDownload'), 'error')
      return
    }
    setDownloadTarget(favToResult(fav))
  }

  const openContents = (f: StreamFavorite) => {
    if (!favHasValidMagnet(f)) {
      notify(t('favorites.magnetInvalidPlay'), 'error')
      return
    }
    setContentsTarget(favToResult(f))
  }

  const handleCreateSub = async (parentId: number) => {
    const name = prompt(t('favorites.promptSubfolderName'))
    if (!name) return
    const f = await folderCreate(name, parentId)
    setFolders([...folders, f])
    setExpanded(new Set(expanded).add(parentId))
  }

  const handleRename = async (id: number, name: string) => {
    setEditingId(null)
    const trimmed = name.trim()
    if (!trimmed) return
    await folderRename(id, trimmed)
    setFolders(folders.map(f => f.id === id ? { ...f, name: trimmed } : f))
  }

  const handleDeleteFolder = async (id: number) => {
    const target = folders.find(f => f.id === id)
    const ok = await confirm({ title: t('favorites.deleteFolderTitle'), message: t('favorites.deleteFolderMessage', { name: target?.name }), confirmLabel: t('favorites.delete'), destructive: true })
    if (!ok) return
    await folderDelete(id)
    setFolders(folders.filter(f => f.id !== id))
    // Favoritos: server fez SET NULL, recarrego pra refletir
    const fresh = await favoritesList()
    setFavs(fresh || [])
    if (selectedFolderId === id) setViewMode(ALL_VIEW)
  }

  const handleDropOnFolder = async (folderId: number | null, favoriteName: string) => {
    setDropOnRoot(false)
    await favoriteSetFolder(favoriteName, folderId)
    setFavs(favs.map(f => f.name === favoriteName ? { ...f, folderId } : f))
  }

  // Versões prompt-based pro sheet do mobile (sem a sidebar/edição inline do
  // desktop): criar pasta raiz e renomear via prompt nativo (usável no iOS).
  const handleCreateRootPrompt = async () => {
    const name = prompt(t('favorites.promptNewFolderName'))?.trim()
    if (!name) return
    const f = await folderCreate(name, null)
    setFolders([...folders, f])
  }
  const handleRenamePrompt = (folder: FavoriteFolder) => {
    const name = prompt(t('favorites.promptRenameFolder'), folder.name)
    if (name != null) void handleRename(folder.id, name)
  }

  // ─── Multi-select (move several favorites to a folder at once) ─────────────
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const toggleSelected = (name: string) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(name)) {
        next.delete(name)
      } else {
        next.add(name)
      }
      return next
    })
  }
  const clearSelection = () => setSelected(new Set())
  const moveSelectedToFolder = async (folderId: number | null) => {
    const names = [...selected]
    // Batch call (Perf #9), auto-chunked below the server cap. Reconcile the
    // optimistic update with the result: a rejection means all failed, a
    // `failed` list means some did — only reflect the ones that actually moved
    // and surface the rest instead of silently reporting success.
    const res = await favoriteSetFolderBatch(names, folderId).catch(() => null)
    const failed = new Set<string>(res ? res.failed : names)
    setFavs(favs.map(f => (selected.has(f.name) && !failed.has(f.name)) ? { ...f, folderId } : f))
    if (failed.size > 0) notify(t('favorites.batchPartialFailed', { count: failed.size }), 'error')
    clearSelection()
  }
  const deleteSelected = async () => {
    const names = [...selected]
    const ok = await confirm({ title: t('favorites.deleteSelectedTitle'), message: t('favorites.deleteSelectedMessage', { count: names.length }), confirmLabel: t('favorites.delete'), destructive: true })
    if (!ok) return
    const res = await favoriteRemoveBatch(names).catch(() => null)
    const failed = new Set<string>(res ? res.failed : names)
    setFavs(favs.filter(f => !(selected.has(f.name) && !failed.has(f.name))))
    if (failed.size > 0) notify(t('favorites.batchPartialFailed', { count: failed.size }), 'error')
    clearSelection()
  }

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <PullToRefreshIndicator pull={ptr.pull} progress={ptr.progress} refreshing={ptr.refreshing} />
      <NavHeader />

      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col md:flex-row gap-4">
        {/* Sidebar — folder tree (oculta no mobile pra não comprimir o conteúdo) */}
        <FolderSidebar
          revealHidden={revealHidden}
          viewMode={viewMode}
          ALL_VIEW={ALL_VIEW}
          favs={favs}
          tree={tree}
          dropOnRoot={dropOnRoot}
          creatingRoot={creatingRoot}
          newFolderInput={newFolderInput}
          selectedFolderId={selectedFolderId}
          expanded={expanded}
          editingId={editingId}
          setCreatingRoot={setCreatingRoot}
          setViewMode={setViewMode}
          setSelectedFolderId={setSelectedFolderId}
          setDropOnRoot={setDropOnRoot}
          setExpanded={setExpanded}
          setEditingId={setEditingId}
          onCreateRoot={handleCreateRoot}
          onRename={handleRename}
          onDeleteFolder={handleDeleteFolder}
          onCreateSub={handleCreateSub}
          onToggleHidden={handleToggleHidden}
          onDropOnFolder={handleDropOnFolder}
        />

        {/* Main — favorites grid */}
        <section className="flex-1 min-w-0">
          {/* Dropdown de pasta — só no mobile (a sidebar é hidden md:block). */}
          <button
            onClick={() => setFolderSheetOpen(true)}
            className="md:hidden w-full flex items-center gap-2 px-3 min-h-[44px] mb-3 rounded-lg bg-surface-secondary border border-default text-sm text-text-primary"
          >
            <Folder className="w-4 h-4 text-pink-400 flex-shrink-0" />
            <span className="truncate flex-1 text-left">{pageTitle(viewMode, ALL_VIEW, folders, t)}</span>
            <ChevronDown className="w-4 h-4 text-text-muted flex-shrink-0" />
          </button>
          <div className="flex items-center justify-between flex-wrap gap-3 mb-4">
            <div className="flex items-center gap-3">
              <Heart className="w-5 h-5 text-pink-400 fill-current" />
              <h1 className="text-lg font-semibold text-text-primary">{pageTitle(viewMode, ALL_VIEW, folders, t)}</h1>
              {!loading && (
                <span className="text-xs text-text-muted bg-surface-secondary border border-default px-2 py-0.5 rounded-full">
                  {t('favorites.itemCount', { count: filteredFavs.length })}
                </span>
              )}
              {isAdmin && (
                <span className="text-[10px] uppercase bg-yellow-500/20 text-yellow-400 border border-yellow-500/30 px-2 py-0.5 rounded">
                  {t('favorites.adminSeesAll')}
                </span>
              )}
            </div>
            <div className="flex items-center gap-3">
              {filteredFavs.length > 0 && (
                <button
                  onClick={refreshSeeds}
                  disabled={seedRefreshing}
                  title={t('favorites.refreshSeedsTooltip')}
                  className="flex items-center gap-1.5 text-xs bg-surface-secondary hover:bg-surface-tertiary text-text-primary border border-default px-3 py-1.5 rounded-lg transition-colors disabled:opacity-50"
                >
                  <RefreshCw className={`w-3.5 h-3.5 ${seedRefreshing ? 'animate-spin' : ''}`} />
                  {t('favorites.refreshSeeds')}
                </button>
              )}
              <button
                onClick={() => { setShowImport(true); setImportMsg(null) }}
                className="flex items-center gap-1.5 text-xs bg-pink-500/15 hover:bg-pink-500/25 text-pink-700 dark:text-pink-200 border border-pink-500/30 px-3 py-1.5 rounded-lg transition-colors"
              >
                <Download className="w-3.5 h-3.5" />
                {t('favorites.importTorrent')}
              </button>
              <p className="text-xs text-text-muted hidden lg:block">{t('favorites.dragHint')}</p>
            </div>
          </div>

          {/* Search bar */}
          <div className="flex items-center gap-2 mb-4">
            <div className="relative flex-1">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-text-muted w-4 h-4" />
              <input
                type="text"
                value={searchQuery}
                onChange={e => { setSearchQuery(e.target.value); setVisible(PAGE_SIZE) }}
                placeholder={t('favorites.searchPlaceholder')}
                className="w-full bg-surface-secondary border border-default rounded-lg pl-9 pr-3 py-2 text-sm text-text-primary focus:outline-none focus:border-pink-500"
              />
              {searchQuery && (
                <button
                  onClick={() => { setSearchQuery(''); setVisible(PAGE_SIZE) }}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"
                >
                  <X className="w-4 h-4" />
                </button>
              )}
            </div>
            {/* Sort: criterion + direction. setVisible reset keeps the infinite
                scroll honest when the order (and thus the first page) changes. */}
            <FavoritesSortControl
              sortBy={sortBy}
              sortDir={sortDir}
              onSortBy={k => { setSortBy(k); setVisible(PAGE_SIZE) }}
              onToggleDir={() => { setSortDir(d => d === 'asc' ? 'desc' : 'asc'); setVisible(PAGE_SIZE) }}
            />
            <button
              onClick={() => {
                if (selected.size === filteredFavs.length) {
                  clearSelection()
                } else {
                  setSelected(new Set(filteredFavs.map(f => f.name)))
                }
              }}
              className="flex items-center gap-1.5 text-xs bg-surface-tertiary hover:bg-surface-tertiary text-text-primary px-3 py-2 rounded-lg transition-colors flex-shrink-0"
              title={selected.size === filteredFavs.length ? t('favorites.deselectAll') : t('favorites.selectAll')}
            >
              {selected.size === filteredFavs.length ? <Square className="w-3.5 h-3.5" /> : <CheckSquare className="w-3.5 h-3.5" />}
              {selected.size === filteredFavs.length ? t('favorites.clear') : t('favorites.select')}
            </button>
          </div>

          {(() => {
            const fallback = renderFavsContent(loading, error, filteredFavs, viewMode, ALL_VIEW, folders, t)
            if (fallback) return fallback
            const shown = filteredFavs.slice(0, visible)
            return <>
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
              {shown.map(fav => (
                <FavoriteCard
                  key={fav.name}
                  fav={fav}
                  selected={selected.has(fav.name)}
                  anySelected={selected.size > 0}
                  folders={folders}
                  seedRefresh={seedRefresh}
                  onToggleSelected={() => toggleSelected(fav.name)}
                  onDragStart={e => handleFavDragStart(e, fav.name)}
                  onPlay={() => playFavorite(fav)}
                  onRemove={() => handleRemove(fav.name)}
                  onDownload={() => downloadFavorite(fav)}
                  onOpenContents={() => openContents(fav)}
                  onMoveToFolder={folderId => handleDropOnFolder(folderId, fav.name)}
                />
              ))}
            </div>
              {visible < filteredFavs.length && (
                <div ref={sentinelRef} className="h-12 flex items-center justify-center text-text-muted text-xs">
                  <Loader2 className="w-4 h-4 animate-spin mr-2" />
                  {t('favorites.loadingMore')}
                </div>
              )}
              {visible >= filteredFavs.length && filteredFavs.length > PAGE_SIZE && (
                <p className="text-center text-text-muted text-xs py-4">{t('favorites.allItemsLoaded', { count: filteredFavs.length })}</p>
              )}
            </>
          })()}
        </section>
      </main>

      {/* Import modal — paste magnet(s) or drop a .torrent file.
          Usa o Sheet (mesmo padrão dos demais modais): centraliza certo no
          desktop/Safari e vira bottom-sheet no mobile. */}
      <ImportSheet
        open={showImport}
        onClose={() => { if (!importing) setShowImport(false) }}
        importing={importing}
        viewMode={viewMode}
        ALL_VIEW={ALL_VIEW}
        folders={folders}
        magnetInput={magnetInput}
        setMagnetInput={setMagnetInput}
        onImportMagnets={importMagnets}
        onImportFiles={importTorrentFiles}
        importMsg={importMsg}
        dragOverDrop={dragOverDrop}
        setDragOverDrop={setDragOverDrop}
      />

        {/* Multi-select action bar — appears when ≥1 favorite is checked. */}
      {selected.size > 0 && (
        <MultiSelectBar
          count={selected.size}
          folders={folders}
          onMoveToFolder={moveSelectedToFolder}
          onDeleteSelected={deleteSelected}
          onClear={clearSelection}
        />
      )}

      {/* Contents/details modal — files + torrent details without playing. */}
      <TorrentContentsModal
        result={contentsTarget}
        onClose={() => setContentsTarget(null)}
        onPlayFile={(r, fileIdx) => { setContentsTarget(null); playSingle(r, fileIdx) }}
        onDownload={(r) => { setContentsTarget(null); setDownloadTarget(r) }}
      />

      {/* Download modal — destino + seleção de arquivos (árvore), igual à busca. */}
      <DownloadModal result={downloadTarget} onClose={() => setDownloadTarget(null)} />

      {/* Dropdown de pastas no mobile — navega entre pastas sem a sidebar. */}
      <MobileFolderSheet
        open={folderSheetOpen}
        onClose={() => setFolderSheetOpen(false)}
        revealHidden={revealHidden}
        viewMode={viewMode}
        ALL_VIEW={ALL_VIEW}
        favs={favs}
        tree={tree}
        setViewMode={setViewMode}
        setSelectedFolderId={setSelectedFolderId}
        onCreateRoot={handleCreateRootPrompt}
        onToggleHidden={handleToggleHidden}
        onCreateSub={handleCreateSub}
        onRename={handleRenamePrompt}
        onDeleteFolder={handleDeleteFolder}
      />
    </div>
  )
}
