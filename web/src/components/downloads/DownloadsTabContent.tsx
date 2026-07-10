import type { DownloadEntry, DownloadPriority, StreamPriority, TorrentInfo } from '../../api/client'
import { CompletedFilterChips, type CompletedFilterKey } from './CompletedFilterChips'
import { ActiveTab } from './ActiveTab'
import { SeedingTab } from './SeedingTab'
import { NetworkTab } from './NetworkTab'
import type { Tab, TabDownloads, TabTorrents } from './tabs'

// DownloadsTabContent — the body under the tab bar: the completed-view filter
// chips, the active/seeding lists for a status tab, or the network panel. Pure
// composition of the existing tab components; every action is delegated up.
export function DownloadsTabContent(props: {
  readonly activeTab: Tab
  readonly torrentsLoaded: boolean
  readonly hasCompletedForTab: boolean
  readonly completedFilter: CompletedFilterKey
  readonly setCompletedFilter: (v: CompletedFilterKey) => void
  readonly effectiveCompletedFilter: CompletedFilterKey
  readonly seedingCountForTab: number
  readonly onDiskCountForTab: number
  readonly tabTorrents: TabTorrents
  readonly tabDownloads: TabDownloads
  readonly busyHash: string | null
  readonly busyID: number | null
  readonly selected: Set<number>
  readonly onToggleSelected: (id: number) => void
  readonly loading: boolean
  readonly onTorrentPause: (h: string) => void
  readonly onTorrentResume: (h: string) => void
  readonly onTorrentPriority: (h: string, p: StreamPriority) => void
  readonly onTorrentDelete: (h: string) => void
  readonly onTorrentPlay: (t: TorrentInfo) => void
  readonly onPause: (id: number) => void
  readonly onResume: (id: number) => void
  readonly onDelete: (id: number) => void
  readonly onPlay: (d: DownloadEntry) => void
  readonly onInspect: (d: DownloadEntry) => void
  readonly openLocalFor: (d: DownloadEntry) => (() => void) | undefined
  readonly onPromote: (d: DownloadEntry) => void
  readonly onStopSeed: (id: number, name: string) => void
  readonly onPromoteMany: (ds: DownloadEntry[]) => void
  readonly onDeleteMany: (ds: DownloadEntry[]) => void
  readonly onStopSeedMany: (ds: DownloadEntry[]) => void
  readonly onRetryMany: (ds: DownloadEntry[]) => void
  readonly onSetPriority: (id: number, p: DownloadPriority) => void
  readonly limitDownKB: string
  readonly limitUpKB: string
  readonly setLimitDownKB: (v: string) => void
  readonly setLimitUpKB: (v: string) => void
  readonly limitsSaving: boolean
  readonly limitsMsg: string
  readonly onSaveLimits: () => void
  readonly totalDown: number
  readonly totalUp: number
  readonly totalPeers: number
}) {
  const {
    activeTab, torrentsLoaded, hasCompletedForTab, completedFilter, setCompletedFilter,
    effectiveCompletedFilter, seedingCountForTab, onDiskCountForTab, tabTorrents, tabDownloads,
    busyHash, busyID, selected, onToggleSelected, loading,
    onTorrentPause, onTorrentResume, onTorrentPriority, onTorrentDelete, onTorrentPlay,
    onPause, onResume, onDelete, onPlay, onInspect, openLocalFor,
    onPromote, onStopSeed, onPromoteMany, onDeleteMany, onStopSeedMany, onRetryMany, onSetPriority,
    limitDownKB, limitUpKB, setLimitDownKB, setLimitUpKB, limitsSaving, limitsMsg, onSaveLimits,
    totalDown, totalUp, totalPeers,
  } = props
  return (
    <div className="min-h-[300px]">
      {activeTab !== 'network' && (
        <>
          {/* Completed-view filter — at the TOP, so it heads the whole list
              instead of sitting between the active and completed cards. */}
          {torrentsLoaded && hasCompletedForTab && (
            <div className="mb-4">
              <CompletedFilterChips
                value={completedFilter}
                onChange={setCompletedFilter}
                seedingN={seedingCountForTab}
                onDiskN={onDiskCountForTab}
              />
            </div>
          )}
          {/* Active / downloading torrents — hidden when filtering "No disco". */}
          {tabTorrents[activeTab].length > 0 && effectiveCompletedFilter !== 'ondisk' && (
            <ActiveTab
              torrents={tabTorrents[activeTab]}
              downloads={[]}
              torrentsLoaded={torrentsLoaded}
              loading={false}
              busyHash={busyHash}
              busyID={null}
              onTorrentPause={onTorrentPause}
              onTorrentResume={onTorrentResume}
              onTorrentPriority={onTorrentPriority}
              onTorrentDelete={onTorrentDelete}
              onTorrentPlay={onTorrentPlay}
              onPause={onPause}
              onResume={onResume}
              onDelete={onDelete}
              onPlay={onPlay}
              onInspect={onInspect}
            openLocalFor={openLocalFor}
            />
          )}
          {/* Background downloads for this tab */}
          <SeedingTab
            torrents={[]}
            downloads={tabDownloads[activeTab]}
            completedFilter={effectiveCompletedFilter}
            torrentsLoaded={torrentsLoaded}
            busyHash={busyHash}
            busyID={busyID}
            selected={selected}
            onToggleSelected={onToggleSelected}
            onTorrentPause={onTorrentPause}
            onTorrentResume={onTorrentResume}
            onTorrentPriority={onTorrentPriority}
            onTorrentDelete={onTorrentDelete}
            onTorrentPlay={onTorrentPlay}
            onPause={onPause}
            onResume={onResume}
            onDelete={onDelete}
            onPromote={onPromote}
            onStopSeed={onStopSeed}
            onPromoteMany={onPromoteMany}
            onDeleteMany={onDeleteMany}
            onStopSeedMany={onStopSeedMany}
            onRetryMany={onRetryMany}
            onSetPriority={onSetPriority}
            onPlay={onPlay}
            onInspect={onInspect}
            openLocalFor={openLocalFor}
            loading={loading}
          />
        </>
      )}
      {activeTab === 'network' && (
        <NetworkTab
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
      )}
    </div>
  )
}
