import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { UploadCloud } from 'lucide-react'
import NavHeader from '../components/NavHeader'
import { useToast } from '../components/Toast'
import { usePersistedState } from '../lib/storage'
import { useEnumQueryParam, useQueryParam, useQuerySetter } from '../lib/useQueryState'
import { useScrollRestoration } from '../lib/useScrollRestoration'
import { useRevealHidden } from '../lib/reveal'
import {
  DownloadEntry, DownloadFilterParams, downloadsList, downloadsListFiltered,
  getDownloadsQueueSettings,
  downloadTrackers, downloadCategories,
  downloadsListAll, DownloadUserEntry,
  TorrentInfo, streamActive, streamGetLimits,
  LocalMount, localMounts, SearchResult,
} from '../api/client'
import { newPendingDeletes, reconcile } from '../lib/downloadsReconcile'
import PromoteModal from '../components/PromoteModal'
import DownloadInspectModal from '../components/DownloadInspectModal'
import DownloadModal from '../components/DownloadModal'
import AddTorrentModal from '../components/AddTorrentModal'
import { useAuth } from '../auth/AuthContext'
import { type CompletedFilterKey } from '../components/downloads/CompletedFilterChips'
import { errMessage } from '../lib/errMessage'
import { type Tab, DOWNLOAD_TABS } from '../components/downloads/tabs'
import { useDownloadDragDrop } from '../components/downloads/useDownloadDragDrop'
import { usePlayRouting } from '../components/downloads/usePlayRouting'
import { useDownloadsView } from '../components/downloads/useDownloadsView'
import { useDownloadActions } from '../components/downloads/useDownloadActions'
import { DownloadsSummary } from '../components/downloads/DownloadsSummary'
import { GlobalActionToolbar } from '../components/downloads/GlobalActionToolbar'
import { DownloadsFiltersBar } from '../components/downloads/DownloadsFiltersBar'
import { DownloadsTabsBar } from '../components/downloads/DownloadsTabsBar'
import { DownloadsTabContent } from '../components/downloads/DownloadsTabContent'
import { BulkActionsSheet } from '../components/downloads/BulkActionsSheet'
import { BulkActionBar } from '../components/downloads/BulkActionBar'

// Re-exported for the co-located unit tests that import from './DownloadsPage'.
export { countTorrents, groupByHash, completedViewCounts } from '../lib/downloadGroups'


// ═══════════════════════════════════════════════════════════════════════════════
// Premium Downloads & Network Dashboard
// ═══════════════════════════════════════════════════════════════════════════════

export default function DownloadsPage() {
  const [items, setItems] = useState<DownloadEntry[]>([])
  // Multi-select of completed downloads for batch promote. Set of IDs.
  const [selected, setSelected] = useState<Set<number>>(new Set())
  // Items passed to the promote modal (null = closed). Single = [d], batch = [d1, d2, ...]
  const [promoteTargets, setPromoteTargets] = useState<DownloadEntry[] | null>(null)
  // Download being inspected: the ID lives in the URL (?inspect=) to survive
  // reload/back; the target is resolved from `items` (push so Back closes it). If the
  // item hasn't loaded yet (poll) or is gone, it stays null and the modal waits/closes.
  const [inspectId, setInspectId] = useQueryParam('inspect', '', { replace: false })
  const inspectTarget = useMemo(
    () => (inspectId ? items.find(d => String(d.id) === inspectId) ?? null : null),
    [inspectId, items],
  )
  // Browsable mounts — used to decide whether Play goes through the local player
  // (file on a mount like /mnt/downloads) or the torrent one (in /data/streams).
  // Loaded once; mounts don't change during a session.
  const [mounts, setMounts] = useState<LocalMount[]>([])
  const [loading, setLoading] = useState(true)
  const [busyID, setBusyID] = useState<number | null>(null)
  const mountedRef = useRef(true)
  // Monotonic load token: the 2s poll + filter/sort changes can have several
  // load()s in flight at once and their responses can land OUT OF ORDER. Only
  // the newest load may apply its result — otherwise an older poll (which still
  // carries a just-deleted row) lands after a newer one and `reconcile` prunes
  // the pending-delete, making the row reappear (the single-user "Remove didn't
  // take"). Each load captures its token and bails if a newer load started.
  const loadSeqRef = useRef(0)
  // Optimistic-delete tracker: IDs the user removed are hidden from poll
  // results until the backend confirms they're gone, so a stale in-flight poll
  // (started before the DELETE landed) can't resurrect a just-deleted row —
  // the "clicked Remove, row came back" race. A ref (not state) so the polling
  // closure always reads the latest set without re-subscribing.
  const pendingDeletesRef = useRef(newPendingDeletes())

  const [activeTab, setActiveTab] = useEnumQueryParam<Tab>('tab', DOWNLOAD_TABS, 'all')
  // Completed-view filter (Todos / Semeando / No disco). Lifted to the page so the
  // chips render ONCE at the top of the tab content — before the active cards —
  // instead of inside SeedingTab (which put them in the MIDDLE of the list,
  // between the active and the completed cards).
  const [completedFilter, setCompletedFilter] = usePersistedState<CompletedFilterKey>('downloads.completedFilter', 'all')

  const [torrents, setTorrents] = useState<TorrentInfo[]>([])
  const [torrentsLoaded, setTorrentsLoaded] = useState(false)
  const [maxActive, setMaxActive] = useState<number>(0)
  const [busyHash, setBusyHash] = useState<string | null>(null)
  const [bulkBusy, setBulkBusy] = useState(false)
  const [bulkSheetOpen, setBulkSheetOpen] = useState(false)
  const { notifyError } = useToast()
  const { t } = useTranslation()

  const [limitDownKB, setLimitDownKB] = useState<string>('')
  const [limitUpKB, setLimitUpKB] = useState<string>('')
  const [limitsSaving, setLimitsSaving] = useState(false)
  const [limitsMsg, setLimitsMsg] = useState<string>('')

  // ─── Filter & Sort state (in the URL: survives navigation/reload/reopen) ──────
  // All via useQueryParam (same [v,set] signature as useState) → the reload effect
  // below doesn't change. Multi-set in a single handler ("Clear" button) uses the
  // atomic setQuery, otherwise each setter would read a stale location.search.
  const [filterSearch, setFilterSearch] = useQueryParam('q')
  const [filterStatus, setFilterStatus] = useQueryParam('status')
  const [filterTracker, setFilterTracker] = useQueryParam('tracker')
  const [filterCategory, setFilterCategory] = useQueryParam('cat')
  const [sortCol, setSortCol] = useQueryParam('sort', 'created_at')
  const [sortDir, setSortDir] = useQueryParam('dir', 'desc')
  const setQuery = useQuerySetter()
  const [availableTrackers, setAvailableTrackers] = useState<string[]>([])
  const [availableCategories, setAvailableCategories] = useState<string[]>([])
  const [filtersParam, setFiltersParam] = useQueryParam('filters')
  const showFilters = filtersParam === '1'
  const filterTimeoutRef = useRef<ReturnType<typeof setTimeout>>()
  // Admin mode: toggle between own downloads and all users' downloads
  const { isAdmin, isGuest } = useAuth()
  const [usersParam, setUsersParam] = useQueryParam('users')
  const showAllUsers = isAdmin && usersParam === 'all'
  const [availableUsers, setAvailableUsers] = useState<DownloadUserEntry[]>([])
  const [filterUserId, setFilterUserId] = useQueryParam('uid')
  // Global hidden curtain: downloads tied to a hidden favourite folder drop out
  // unless it's open (re-fetch on flip; backend filters by the header).
  const [revealHidden] = useRevealHidden()
  // Restores the scroll position when coming back/reloading, as soon as the list loads.
  useScrollRestoration(!loading)

  // Add Torrent & Magnet Modals State
  const [showAddModal, setShowAddModal] = useState(false)
  const [downloadTarget, setDownloadTarget] = useState<SearchResult | null>(null)
  const [preloadFiles, setPreloadFiles] = useState<File[] | null>(null)

  // Drag & drop of magnet/.torrent onto the whole page (overlay + handlers).
  const drag = useDownloadDragDrop({ setLoading, setDownloadTarget, setPreloadFiles, setShowAddModal })

  // ─── Data loading ─────────────────────────────────────────────────────────

  const load = async () => {
    const seq = ++loadSeqRef.current
    try {
      const list = showAllUsers
        ? await downloadsListAll({
            userId: filterUserId || undefined,
            sort: sortCol,
            order: sortDir,
          })
        : await downloadsList()
      // Drop stale/out-of-order responses: only the latest load reconciles.
      if (mountedRef.current && seq === loadSeqRef.current) setItems(reconcile(pendingDeletesRef.current, list))
    } catch (err) {
      notifyError(errMessage(err))
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }

  const loadFiltered = async () => {
    const seq = ++loadSeqRef.current
    try {
      const params: DownloadFilterParams = {
        status: filterStatus || undefined,
        tracker: filterTracker || undefined,
        category: filterCategory || undefined,
        search: filterSearch || undefined,
        sort: sortCol,
        order: sortDir,
      }
      if (showAllUsers) params.userId = filterUserId || undefined
      const list = showAllUsers
        ? await downloadsListAll(params)
        : await downloadsListFiltered(params)
      if (mountedRef.current && seq === loadSeqRef.current) setItems(reconcile(pendingDeletesRef.current, list))
    } catch (err) {
      notifyError(errMessage(err))
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }

  // Poll + mutations must respect active filters — the mount-only interval used
  // to always call load() and overwrote a filtered view every ~2s.
  const reloadDownloadsRef = useRef<() => Promise<void>>(async () => {})
  reloadDownloadsRef.current = async () => {
    const hasFilters = !!(filterStatus || filterTracker || filterCategory || filterSearch
      || sortCol !== 'created_at' || sortDir !== 'desc')
    if (hasFilters) await loadFiltered()
    else await load()
  }

  const loadFilterOptions = async () => {
    try {
      const [trackers, cats] = await Promise.all([downloadTrackers(), downloadCategories()])
      if (mountedRef.current) {
        setAvailableTrackers(trackers)
        setAvailableCategories(cats)
      }
    } catch { /* silent */ }
  }

  const loadLimits = async () => {
    try {
      const cur = await streamGetLimits()
      if (!mountedRef.current) return
      setLimitDownKB(cur.down > 0 ? String(Math.round(cur.down / 1024)) : '')
      setLimitUpKB(cur.up > 0 ? String(Math.round(cur.up / 1024)) : '')
    } catch { /* leave empty */ }
  }

  const loadTorrents = async () => {
    try {
      const list = await streamActive()
      if (mountedRef.current) setTorrents(list)
    } catch { /* keep last */ } finally {
      if (mountedRef.current) setTorrentsLoaded(true)
    }
  }

  useEffect(() => {
    mountedRef.current = true
    reloadDownloadsRef.current().catch(() => {}); loadTorrents(); loadLimits(); loadFilterOptions()
    localMounts().then(setMounts).catch(() => {})
    getDownloadsQueueSettings().then(s => setMaxActive(s.maxActive)).catch(() => {})
    // Skip the poll while the tab is hidden — each cycle redoes streamActive→buildInfo
    // for every active torrent (expensive on a multi-file pack). Resumes on focus.
    const t = setInterval(() => {
      if (document.hidden) return
      reloadDownloadsRef.current().catch(() => {})
      loadTorrents()
    }, 2000)
    return () => { mountedRef.current = false; clearInterval(t) }
  }, [])

  // Reload when filters/sort change (debounced for search)
  useEffect(() => {
    if (!mountedRef.current) return
    if (filterSearch !== undefined && filterTimeoutRef.current) {
      clearTimeout(filterTimeoutRef.current)
    }
    filterTimeoutRef.current = setTimeout(() => {
      if (mountedRef.current) reloadDownloadsRef.current().catch(() => {})
    }, filterSearch ? 300 : 0)
    return () => { if (filterTimeoutRef.current) clearTimeout(filterTimeoutRef.current) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterStatus, filterTracker, filterCategory, filterSearch, sortCol, sortDir, filterUserId, showAllUsers, revealHidden])

  // Roteamento de Play (player local vs torrent) + "abrir no local".
  const { onPlay, onTorrentPlay, openLocalFor } = usePlayRouting(mounts)

  // ─── Derived data ─────────────────────────────────────────────────────────

  const {
    downloadsByStatus, completedDownloads, queuedDownloads, stalledCount,
    totalDown, totalUp, totalPeers, queueSubtitle, activeValue,
    tabCounts, tabDownloads, tabTorrents,
    seedingCountForTab, onDiskCountForTab, hasCompletedForTab, effectiveCompletedFilter,
  } = useDownloadsView({ items, torrents, sortCol, sortDir, maxActive, activeTab, completedFilter })

  // ─── Actions ──────────────────────────────────────────────────────────────

  const {
    onPause, onResume, onSetPriority, onDelete, onPromote, onPromoteSelected,
    onBatchPause, onBatchResume, onBatchDelete, handleToggleSelectAll, onPromoted,
    onStopSeed, onPromoteMany, onDeleteMany, onStopSeedMany, onRetryMany,
    onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete, onSaveLimits,
    doResumeAll, doPauseAll, doRemoveCompleted, doClearFailed, doClearQueued, onToggleSelected,
  } = useDownloadActions({
    items, setItems, selected, setSelected, setBusyID, setBusyHash, setBulkBusy, setPromoteTargets,
    pendingDeletesRef, reloadDownloadsRef, loadTorrents, loadLimits, mountedRef,
    limitDownKB, limitUpKB, setLimitsSaving, setLimitsMsg,
    completedDownloads, downloadsByStatus, queuedDownloads,
  })

  // ─── Render ───────────────────────────────────────────────────────────────

  return (
    <section
      onDragEnter={drag.handleDragEnter}
      onDragOver={drag.handleDragOver}
      onDragLeave={drag.handleDragLeave}
      onDrop={drag.handleDrop}
      aria-label={t('downloads.page.sectionAriaLabel')}
      className="relative min-h-screen bg-surface"
    >
      {drag.isDraggingPage && (
        <div className="fixed inset-0 z-50 bg-surface-elevated/80 backdrop-blur-md flex flex-col items-center justify-center border-4 border-dashed border-cyan-500/50 m-4 rounded-3xl pointer-events-none transition-all duration-300 animate-pulse">
          <UploadCloud className="w-16 h-16 text-cyan-400 mb-4 animate-bounce" />
          <h2 className="text-xl font-bold text-text-primary mb-1">{t('downloads.page.dropOverlayTitle')}</h2>
          <p className="text-sm text-text-secondary">{t('downloads.page.dropOverlaySubtitle')}</p>
        </div>
      )}

      <NavHeader />
      <main id="main-content" tabIndex={-1} className="max-w-5xl mx-auto px-4 py-6 flex flex-col gap-6">
        {/* ═══════════════ Summary Dashboard ═══════════════ */}
        <DownloadsSummary
          totalDown={totalDown}
          totalUp={totalUp}
          totalPeers={totalPeers}
          activeValue={activeValue}
          queueSubtitle={queueSubtitle}
        />

        {/* ═══════════════ Global Action Toolbar ═══════════════ */}
        <GlobalActionToolbar
          downloadingCount={downloadsByStatus.downloading.length}
          pausedCount={downloadsByStatus.paused.length}
          completedCount={completedDownloads.length}
          failedCount={downloadsByStatus.failed.length}
          queuedCount={queuedDownloads.length}
          stalledCount={stalledCount}
          isGuest={isGuest}
          bulkBusy={bulkBusy}
          onResumeAll={doResumeAll}
          onPauseAll={doPauseAll}
          onRemoveCompleted={doRemoveCompleted}
          onClearFailed={doClearFailed}
          onClearQueued={doClearQueued}
          onOpenSheet={() => setBulkSheetOpen(true)}
        />

        {/* ═══════════════ Filters Bar ═══════════════ */}
        <DownloadsFiltersBar
          filterSearch={filterSearch}
          setFilterSearch={setFilterSearch}
          showFilters={showFilters}
          setFiltersParam={setFiltersParam}
          filterStatus={filterStatus}
          setFilterStatus={setFilterStatus}
          filterTracker={filterTracker}
          setFilterTracker={setFilterTracker}
          availableTrackers={availableTrackers}
          filterCategory={filterCategory}
          setFilterCategory={setFilterCategory}
          availableCategories={availableCategories}
          showAllUsers={showAllUsers}
          availableUsers={availableUsers}
          filterUserId={filterUserId}
          setFilterUserId={setFilterUserId}
          sortCol={sortCol}
          setSortCol={setSortCol}
          sortDir={sortDir}
          setSortDir={setSortDir}
          setQuery={setQuery}
        />

        {/* ═══════════════ Tabs & Actions ═══════════════ */}
        <DownloadsTabsBar
          activeTab={activeTab}
          setActiveTab={setActiveTab}
          tabCounts={tabCounts}
          isAdmin={isAdmin}
          isGuest={isGuest}
          showAllUsers={showAllUsers}
          setQuery={setQuery}
          setUsersParam={setUsersParam}
          setAvailableUsers={setAvailableUsers}
          setPreloadFiles={setPreloadFiles}
          setShowAddModal={setShowAddModal}
        />

        {/* ═══════════════ Tab Content ═══════════════ */}
        <DownloadsTabContent
          activeTab={activeTab}
          torrentsLoaded={torrentsLoaded}
          hasCompletedForTab={hasCompletedForTab}
          completedFilter={completedFilter}
          setCompletedFilter={setCompletedFilter}
          effectiveCompletedFilter={effectiveCompletedFilter}
          seedingCountForTab={seedingCountForTab}
          onDiskCountForTab={onDiskCountForTab}
          tabTorrents={tabTorrents}
          tabDownloads={tabDownloads}
          busyHash={busyHash}
          busyID={busyID}
          selected={selected}
          onToggleSelected={onToggleSelected}
          loading={loading}
          onTorrentPause={onTorrentPause}
          onTorrentResume={onTorrentResume}
          onTorrentPriority={onTorrentPriority}
          onTorrentDelete={onTorrentDelete}
          onTorrentPlay={onTorrentPlay}
          onPause={onPause}
          onResume={onResume}
          onDelete={onDelete}
          onPlay={onPlay}
          onInspect={(d: DownloadEntry) => setInspectId(String(d.id))}
          openLocalFor={openLocalFor}
          onPromote={onPromote}
          onStopSeed={onStopSeed}
          onPromoteMany={onPromoteMany}
          onDeleteMany={onDeleteMany}
          onStopSeedMany={onStopSeedMany}
          onRetryMany={onRetryMany}
          onSetPriority={onSetPriority}
          limitDownKB={limitDownKB}
          limitUpKB={limitUpKB}
          setLimitDownKB={setLimitDownKB}
          setLimitUpKB={setLimitUpKB}
          limitsSaving={limitsSaving}
          limitsMsg={limitsMsg}
          onSaveLimits={onSaveLimits}
          totalDown={totalDown}
          totalUp={totalUp}
          totalPeers={totalPeers}
        />
      </main>

      {/* Modal de promove — navegador de subpastas + nova pasta + keep-seeding.
          Aceita single (1 item) ou batch (N selecionados). Backend cria subdirs
          inexistentes via os.MkdirAll. */}
      <PromoteModal
        items={promoteTargets}
        onClose={() => setPromoteTargets(null)}
        onPromoted={onPromoted}
      />

      {/* Detailed download inspection modal (with recheck, files list and stop seed) */}
      <DownloadInspectModal
        download={inspectTarget}
        onClose={() => setQuery({ inspect: null }, { replace: true })}
        siblings={inspectTarget ? items.filter(i => i.infoHash === inspectTarget.infoHash) : []}
        onAdopted={() => { reloadDownloadsRef.current().catch(() => {}) }}
        onMutated={(updated) => {
          setItems(prev => prev.map(item => item.id === updated.id ? updated : item))
        }}
        onDeleted={() => {
          setItems(prev => prev.filter(item => item.id !== inspectTarget?.id))
          setQuery({ inspect: null }, { replace: true })
        }}
        onPromote={onPromote}
        onPlay={onPlay}
      />

      {/* Add torrents via drag & dropped files or magnet link */}
      <AddTorrentModal
        isOpen={showAddModal}
        onClose={() => setShowAddModal(false)}
        preloadFiles={preloadFiles}
        onAdded={(result) => {
          setDownloadTarget(result)
        }}
      />

      {/* Modal to configure the download of a single torrent resolved from search or dragged in */}
      <DownloadModal
        result={downloadTarget}
        onClose={() => {
          setDownloadTarget(null)
          reloadDownloadsRef.current().catch(() => {})
          loadTorrents().catch(() => {})
        }}
      />

      {/* Global actions (mobile) */}
      <BulkActionsSheet
        open={bulkSheetOpen}
        onClose={() => setBulkSheetOpen(false)}
        bulkBusy={bulkBusy}
        completedCount={completedDownloads.length}
        failedCount={downloadsByStatus.failed.length}
        queuedCount={queuedDownloads.length}
        onResumeAll={doResumeAll}
        onPauseAll={doPauseAll}
        onRemoveCompleted={doRemoveCompleted}
        onClearFailed={doClearFailed}
        onClearQueued={doClearQueued}
      />

      {/* Floating bulk-actions bar, only shows with an active selection. */}
      {selected.size > 0 && (
        <BulkActionBar
          selectedCount={selected.size}
          allSelected={items.length > 0 && selected.size === items.length}
          bulkBusy={bulkBusy}
          onBatchPause={onBatchPause}
          onBatchResume={onBatchResume}
          onPromoteSelected={onPromoteSelected}
          onBatchDelete={onBatchDelete}
          onToggleSelectAll={handleToggleSelectAll}
        />
      )}
    </section>
  )
}
