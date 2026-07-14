import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import { useQuerySetter } from '../lib/useQueryState'
import { X } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { usePersistedState } from '../lib/storage'
import { useAuth } from '../auth/AuthContext'
import { DuplicatesModal } from '../components/local/DuplicatesModal'
import { BatchActionBar } from '../components/BatchActionBar'
import LocalPromoteModal from '../components/LocalPromoteModal'
import ReclassifyFolderModal from '../components/ReclassifyFolderModal'
import MoveFolderModal from '../components/MoveFolderModal'
import RenameModal from '../components/RenameModal'
import { isVideo, isAudio } from '../components/local/entryFormat'
import { useIncrementalReveal } from '../components/player/useIncrementalReveal'
import {
  LocalEntry,
  LocalMount,
  AdminUser,
  buildLocalHash,
  localList,
  localMounts,
  adminListUsers,
  setLocalViewAsUser,
} from '../api/client'
import { useRevealHidden } from '../lib/reveal'
import { useTransfers } from '../lib/transfers'
import FileProgressBar from '../components/FileProgressBar'
import FilePreviewModal from '../components/FilePreviewModal'
import { detectViewerKind } from '../components/viewer/viewerKind'
import { previewRawURL } from '../api/preview'
import { matchesEntryStatus, type LocalStatusFilter } from '../lib/localFilter'
import { errMessage } from '../lib/errMessage'
import { type SortKey, type KindFilter } from '../components/local/localViewTypes'
import { LocalSidebar } from '../components/local/LocalSidebar'
import { MountSheet } from '../components/local/MountSheet'
import { LocalActionBar } from '../components/local/LocalActionBar'
import { StatusBanner } from '../components/StatusBanner'
import { LoadingState } from '../components/LoadingState'
import { LocalToolbar } from '../components/local/LocalToolbar'
import { LocalEntryList } from '../components/local/LocalEntryList'
import { useHiddenEntries } from '../components/local/useHiddenEntries'
import { useLocalUpload } from '../components/local/useLocalUpload'
import { useLocalOps } from '../components/local/useLocalOps'
import { useLocalSelection } from '../components/local/useLocalSelection'
import { useLocalPlayback } from '../components/local/useLocalPlayback'

export default function LocalPage() {
  const { t } = useTranslation()
  const [searchParams] = useSearchParams()
  const setQuery = useQuerySetter()
  const [mounts, setMounts] = useState<LocalMount[]>([])
  const activeMount = searchParams.get('mount') || ''
  const path = searchParams.get('path') || ''
  const [entries, setEntries] = useState<LocalEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [showDuplicates, setShowDuplicates] = useState(false)
  const [kind, setKind] = usePersistedState<KindFilter>('local.kind', 'all')
  const [statusFilter, setStatusFilter] = usePersistedState<LocalStatusFilter>('local.status', 'all')
  const [sortKey, setSortKey] = usePersistedState<SortKey>('local.sortKey', 'name')
  const [sortDir, setSortDir] = usePersistedState<'asc' | 'desc'>('local.sortDir', 'asc')

  // Files queued for the promote modal: 1 (single, via the row action) or many
  // (batch selection). The modal applies one destination + AI choice to all in
  // a single call — no more one-modal-per-file walk.
  const [promoteEntries, setPromoteEntries] = useState<LocalEntry[]>([])
  const [reclassifyItem, setReclassifyItem] = useState<LocalEntry | null>(null)
  const [moveItem, setMoveItem] = useState<LocalEntry | null>(null)
  const [renameItem, setRenameItem] = useState<LocalEntry | null>(null)
  // Viewer universal pra arquivos não-reproduzíveis (NFO/imagem/PDF/CBZ/zip/EPUB)
  const [previewEntry, setPreviewEntry] = useState<LocalEntry | null>(null)

  // Busca textual por nome (filtra a lista visível) + seletor de mount (mobile).
  const [search, setSearch] = useState('')
  const [mountSheetOpen, setMountSheetOpen] = useState(false)

  // Hidden curtain (global easter egg): hidden entries drop from the list unless
  // it's open.
  const [revealHidden] = useRevealHidden()

  const { isGuest, isAdmin } = useAuth()
  // Admin "view as user": '' = own space. When set, every /api/local/* call
  // carries ?user= (the backend re-checks the admin role before honoring it).
  const [viewAsUser, setViewAsUser] = useState('')
  const [adminUsers, setAdminUsers] = useState<AdminUser[]>([])
  const activeMountObj = useMemo(() => mounts.find((m) => m.name === activeMount), [mounts, activeMount])
  const canViewAsUser = isAdmin && !!activeMountObj?.userSubpath
  const canManipulate = !isGuest && activeMount.toLowerCase() === 'meus downloads'

  // useCallback: o handler passado ao breadcrumb / navegação precisa de referência
  // estável pra que o React.memo das rows continue funcionando.
  const updateNavigation = useCallback((newMount: string, newPath: string, replace = false) => {
    // Atomic two-key update (mount + path) via the shared helper, which merges over
    // the live query so an active ?play= is preserved.
    setQuery({ mount: newMount || null, path: newPath || null }, { replace })
  }, [setQuery])

  // Folders always show (so navigation never gets filtered away); the kind
  // filter + sort apply within each group, folders kept on top.
  const visible = useMemo(() => {
    const q = search.trim().toLowerCase()
    // O filtro de status (baixando/concluído) aplica a pastas E arquivos — mas só
    // quando ≠ 'all', pra que no default a navegação por pastas siga livre.
    const keep = (e: LocalEntry) =>
      (!q || e.name.toLowerCase().includes(q)) && matchesEntryStatus(e, statusFilter)
    const dirs = entries.filter((e) => e.isDir && keep(e))
    let files = entries.filter((e) => !e.isDir && keep(e))
    if (kind === 'video') files = files.filter((e) => isVideo(e.name))
    else if (kind === 'audio') files = files.filter((e) => isAudio(e.name))
    else if (kind === 'other') files = files.filter((e) => !isVideo(e.name) && !isAudio(e.name))

    const cmp = (a: LocalEntry, b: LocalEntry) => {
      let r = 0
      if (sortKey === 'name') r = a.name.localeCompare(b.name, undefined, { numeric: true })
      else if (sortKey === 'size') r = a.size - b.size
      else r = new Date(a.modTime).getTime() - new Date(b.modTime).getTime()
      return sortDir === 'asc' ? r : -r
    }
    return [...dirs.sort(cmp), ...files.sort(cmp)]
  }, [entries, kind, statusFilter, sortKey, sortDir, search])

  // Windowing: renderiza a lista em LOTES e revela mais ao rolar até o fim
  // (sentinela + IntersectionObserver) — igual Search/Favorites. resetKey volta ao
  // 1º lote quando muda a pasta/mount ou os filtros/ordenação/busca.
  const reveal = useIncrementalReveal(
    visible.length,
    `${activeMount}|${path}|${kind}|${statusFilter}|${sortKey}|${sortDir}|${search}`,
  )

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortKey(key); setSortDir(key === 'name' ? 'asc' : 'desc') }
  }

  // reqSeq guards against out-of-order responses: when the user navigates
  // quickly (or the initial mount load is still in flight), two localList calls
  // race and the slower one could overwrite the newer result — showing stale or
  // empty content and a flash. Only the latest request is allowed to commit.
  const reqSeq = useRef(0)

  const refresh = useCallback(() => {
    if (!activeMount) return
    // Sync the client module's view-as state BEFORE any local call so list +
    // subsequent play/thumb/move/delete all hit the selected user's space.
    setLocalViewAsUser(viewAsUser)
    const seq = ++reqSeq.current
    setLoading(true)
    setError('')
    localList(activeMount, path)
      .then((data) => {
        if (seq !== reqSeq.current) return
        setEntries(data)
      })
      .catch((e: unknown) => {
        if (seq !== reqSeq.current) return
        const msg = errMessage(e)
        setError(msg)
        setEntries([])
      })
      .finally(() => {
        if (seq === reqSeq.current) setLoading(false)
      })
  }, [activeMount, path, viewAsUser])

  const { requestDelete, requestCleanEmptyDirs, handleToggleLock, requestCacheFolder } =
    useLocalOps(activeMount, path, refresh, setError, setNotice)
  const { hiddenSet, handleToggleHidden } = useHiddenEntries(activeMount, revealHidden, refresh)
  const { upload, uploadError, setUploadError, uploadAbortRef, fileInputRef, handleUploadPick } =
    useLocalUpload(activeMount, path, viewAsUser, refresh)
  const {
    selectMode, setSelectMode, selected, selectedEntries,
    batchRunning, batchMoveOpen, setBatchMoveOpen,
    clearSelection, toggleSelect, enterSelect, selectAllVisible, setSelected,
    runBatchDelete, runBatchPromote,
  } = useLocalSelection(entries, visible, activeMount, path, refresh, setError, setPromoteEntries)
  const handleEntryClick = useLocalPlayback(activeMount, path, visible, updateNavigation, setPreviewEntry)

  const promoteOne = useCallback((entry: LocalEntry) => setPromoteEntries([entry]), [])

  useEffect(() => {
    localMounts()
      .then(setMounts)
      .catch((e: unknown) => {
        const msg = errMessage(e)
        setError(msg)
      })
  }, [])

  // Auto-seleciona o primeiro mount sempre que NENHUM está selecionado — no land
  // inicial E ao re-clicar "Local" na nav (que vai pra /local sem ?mount=, zerando
  // o activeMount). Tem que ser REATIVO, não só no load: no mobile o seletor de
  // mount vive DENTRO do bloco `{activeMount && ...}`, então um activeMount vazio
  // escondia os mounts por completo ("clico no Local 2x e some os mounts"). Manter
  // sempre um mount selecionado evita esse estado morto. Loop não acontece: ao
  // selecionar, activeMount deixa de ser vazio e o efeito vira no-op.
  useEffect(() => {
    if (mounts.length > 0 && !activeMount) {
      updateNavigation(mounts[0].name, '', true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mounts, activeMount])

  useEffect(() => {
    setNotice('') // stale "N folders removed" shouldn't linger across navigation
    refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeMount, path, viewAsUser, revealHidden])

  // Async move/promote now run server-side and report to the Transfers dock; when
  // the last running transfer finishes, re-list so the browser reflects the new
  // layout (the source is gone, the destination has the file).
  const { transfers } = useTransfers()
  const prevRunningTransfers = useRef(0)
  useEffect(() => {
    const running = transfers.filter((t) => t.status === 'running').length
    if (prevRunningTransfers.current > 0 && running === 0) refresh()
    prevRunningTransfers.current = running
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [transfers])

  // Load the user list once for the admin "view as user" selector.
  useEffect(() => {
    if (!isAdmin) return
    adminListUsers().then(setAdminUsers).catch(() => setAdminUsers([]))
  }, [isAdmin])

  // Reset the view-as override when leaving the page so other views (e.g. the
  // player opening a local "continue watching" item) aren't silently scoped to
  // another user.
  useEffect(() => {
    return () => setLocalViewAsUser('')
  }, [])

  const handleSelectMount = (name: string) => {
    updateNavigation(name, '')
    setViewAsUser('') // back to own space when switching mounts
  }

  const handleViewAsUser = (username: string) => {
    setLocalViewAsUser(username) // module state — takes effect on the next call
    setViewAsUser(username)
    updateNavigation(activeMount, '') // jump to the root of the selected user's space
  }

  return (
    <div className="h-screen bg-surface flex flex-col overflow-hidden">
      <NavHeader />
      <main className="flex-1 min-h-0 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col md:flex-row gap-4 md:gap-6">
        <LocalSidebar
          mounts={mounts}
          activeMount={activeMount}
          onSelectMount={handleSelectMount}
          canViewAsUser={canViewAsUser}
          viewAsUser={viewAsUser}
          onViewAsUser={handleViewAsUser}
          adminUsers={adminUsers}
        />

        {/* Content */}
        <section className="flex-1 min-w-0 min-h-0 flex flex-col gap-4">
          {activeMount && (
            <LocalActionBar
              activeMount={activeMount}
              path={path}
              onNavigate={(p) => updateNavigation(activeMount, p)}
              onOpenMountSheet={() => setMountSheetOpen(true)}
              onRefresh={refresh}
              loading={loading}
              activeMountObj={activeMountObj}
              onCacheFolder={requestCacheFolder}
              canManipulate={canManipulate}
              isAdmin={isAdmin}
              fileInputRef={fileInputRef}
              onUploadPick={handleUploadPick}
              uploadInFlight={!!upload}
              onCleanEmptyDirs={requestCleanEmptyDirs}
              onShowDuplicates={() => setShowDuplicates(true)}
              onReclassify={setReclassifyItem}
            />
          )}

          {/* Banner de progresso do upload (streaming direto pro disco no backend) —
              mesmo componente FileProgressBar usado no dock global de Transferências. */}
          {upload && (
            <div className="flex-shrink-0 bg-surface-secondary border border-green-500/30 rounded-xl p-3">
              <FileProgressBar
                label={upload.name}
                status="running"
                bytesDone={upload.loaded}
                bytesTotal={upload.total}
                onCancel={() => uploadAbortRef.current?.abort()}
              />
            </div>
          )}

          {uploadError && (
            <div className="flex-shrink-0 bg-amber-500/10 border border-amber-500/30 text-amber-400 rounded-xl px-4 py-2.5 text-sm flex items-center justify-between gap-2">
              <span>{uploadError}</span>
              <button onClick={() => setUploadError('')} className="text-amber-400/70 hover:text-amber-500 dark:hover:text-amber-300">
                <X className="w-4 h-4" />
              </button>
            </div>
          )}

          {activeMount && entries.length > 0 && (
            <LocalToolbar
              search={search}
              onSearchChange={setSearch}
              canManipulate={canManipulate}
              isAdmin={isAdmin}
              selectMode={selectMode}
              onEnterSelectMode={() => setSelectMode(true)}
              kind={kind}
              onKindChange={setKind}
              statusFilter={statusFilter}
              onStatusChange={setStatusFilter}
              sortKey={sortKey}
              sortDir={sortDir}
              onToggleSort={toggleSort}
            />
          )}

          {error && (
            <StatusBanner variant="error">{error}</StatusBanner>
          )}

          {notice && (
            <StatusBanner
              variant="success"
              onDismiss={() => setNotice('')}
              dismissLabel={t('local.close')}
            >
              {notice}
            </StatusBanner>
          )}

          {loading && <LoadingState size="sm" label={t('local.loading')} />}

          {!loading && !error && activeMount && visible.length === 0 && (
            <div className="text-text-muted text-sm">
              {entries.length === 0 ? t('local.emptyFolder') : t('local.noFilterMatch')}
            </div>
          )}

          {!loading && visible.length > 0 && (
            <LocalEntryList
              visible={visible}
              reveal={reveal}
              activeMount={activeMount}
              selectMode={selectMode}
              selected={selected}
              canManipulate={canManipulate}
              isAdmin={isAdmin}
              hiddenSet={hiddenSet}
              onOpen={handleEntryClick}
              onEnterSelect={enterSelect}
              onToggleSelect={toggleSelect}
              onRename={setRenameItem}
              onPromote={promoteOne}
              onReclassify={setReclassifyItem}
              onMove={setMoveItem}
              onLock={handleToggleLock}
              onDelete={requestDelete}
              onToggleHidden={handleToggleHidden}
            />
          )}

          {/* Viewer universal — arquivo local não-reproduzível clicado */}
          {previewEntry && activeMount && (() => {
            // Irmãs imagens da pasta viram navegação ←/→ no viewer.
            const imageSiblings = visible.filter(x => !x.isDir && detectViewerKind(x.name) === 'image')
            const imageStart = Math.max(0, imageSiblings.findIndex(x => x.path === previewEntry.path))
            return (
              <FilePreviewModal
                infoHash={buildLocalHash(activeMount, previewEntry.path)}
                fileIdx={0}
                filePath={previewEntry.path}
                fileSize={previewEntry.size}
                imageItems={imageSiblings.map(x => ({ label: x.path, url: previewRawURL(buildLocalHash(activeMount, x.path), 0) }))}
                imageStart={imageStart}
                onClose={() => setPreviewEntry(null)}
              />
            )
          })()}

          {/* Modal de Promoção — individual (1) ou lote (N) num único fluxo */}
          <LocalPromoteModal
            mount={activeMount}
            entries={promoteEntries}
            onClose={() => setPromoteEntries([])}
            onPromoted={() => { refresh(); clearSelection() }}
          />

          {/* Modal de Reclassificação em lote via IA */}
          <ReclassifyFolderModal
            mount={activeMount}
            entry={reclassifyItem}
            onClose={() => setReclassifyItem(null)}
            onDone={() => { setReclassifyItem(null); refresh() }}
          />

          {/* Modal de renomear arquivo/pasta in-place */}
          <RenameModal
            mount={activeMount}
            entry={renameItem}
            onClose={() => setRenameItem(null)}
            onRenamed={refresh}
          />

          {/* Modal de Mover entre mounts — individual (moveItem) ou lote (selectedEntries) */}
          <MoveFolderModal
            mount={activeMount}
            entry={moveItem}
            entries={batchMoveOpen ? selectedEntries : undefined}
            onClose={() => {
              setMoveItem(null)
              if (batchMoveOpen) { setBatchMoveOpen(false); clearSelection() }
            }}
            onMoved={refresh}
          />

          {/* Modal de limpar duplicados por conteúdo (pasta atual, recursivo) */}
          {showDuplicates && activeMount && (
            <DuplicatesModal
              mount={activeMount}
              path={path}
              onClose={() => setShowDuplicates(false)}
              onDeleted={(n) => {
                setNotice(n > 0 ? t('local.duplicates.removedNotice', { count: n }) : t('local.duplicates.noneRemoved'))
                refresh()
              }}
            />
          )}
        </section>
      </main>

      {/* Barra de ações em lote (modo seleção) */}
      {selectMode && (
        <BatchActionBar
          count={selected.size}
          onCancel={clearSelection}
          allSelected={visible.length > 0 && selected.size === visible.length}
          onSelectAll={selected.size === visible.length ? () => setSelected(new Set()) : selectAllVisible}
          canMove={isAdmin}
          canPromote={canManipulate || isAdmin}
          onDelete={runBatchDelete}
          onMove={() => setBatchMoveOpen(true)}
          onPromote={runBatchPromote}
          running={batchRunning}
        />
      )}

      <MountSheet
        open={mountSheetOpen}
        onClose={() => setMountSheetOpen(false)}
        mounts={mounts}
        activeMount={activeMount}
        onSelectMount={handleSelectMount}
        canViewAsUser={canViewAsUser}
        viewAsUser={viewAsUser}
        onViewAsUser={handleViewAsUser}
        adminUsers={adminUsers}
      />
    </div>
  )
}
