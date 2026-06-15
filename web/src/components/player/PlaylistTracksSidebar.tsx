import { useState } from 'react'
import { ChevronRight, ChevronDown, Loader2, AlertCircle, ListMusic, Music2, FileVideo } from 'lucide-react'
import { useTranslation } from 'react-i18next'

// The translate fn's exact type (react-i18next's TFunction) — sharing it across
// the helper sub-components keeps tsc happy without `any`.
type TFn = ReturnType<typeof useTranslation>['t']
import { totalReadyTracks, type PlaylistGroup, type PlaylistTrack } from './playlistTracks'

// Ready groups with at most this many tracks auto-expand (a single local file,
// a short EP). Big packs (a 90-track discography) stay collapsed with a count
// so the list doesn't explode — the user expands the one they want.
const AUTO_EXPAND_MAX = 25

// Single source for the "resolved" check — keeps the 'ready' literal in one
// place (S1192) and reads clearly at each call site.
const isReady = (g: PlaylistGroup): boolean => g.status === 'ready'

type Props = {
  // Agregado resolvido pelo PAI (usePlaylistTracks levantado pro PlayerModal, pra
  // o motor gapless cross-item também enxergar). Antes a sidebar chamava o hook.
  readonly groups: PlaylistGroup[]
  readonly ensureLoaded: (itemIndex: number) => void
  readonly currentItemIndex: number
  readonly selectedFile: number
  // Play a file of the CURRENTLY-loaded item (smooth, no reload).
  readonly playFile: (fileIndex: number) => void
  // Jump the playlist to another item + file (switches torrent).
  readonly onJump: (itemIndex: number, fileIndex: number) => void
  readonly onClose: () => void
}

// PlaylistTracksSidebar — the AGGREGATED track list shown while a playlist
// plays. Instead of only the current torrent's files, it lists every playable
// file across ALL playlist items (torrent packs + single local files), grouped
// by item. The groups are resolved by usePlaylistTracks in the PARENT (so the
// gapless engine sees the same aggregate) and passed in. Clicking any track
// plays it directly.
export function PlaylistTracksSidebar({
  groups, ensureLoaded, currentItemIndex, selectedFile, playFile, onJump, onClose,
}: Props) {
  const { t } = useTranslation()
  // Explicit user expand/collapse choices (itemIndex → open). Absent = use the
  // default rule in isOpen(). Keeps the auto-expand behaviour overridable
  // without fighting React effects.
  const [overrides, setOverrides] = useState<Map<number, boolean>>(new Map())

  const isOpen = (g: PlaylistGroup): boolean => {
    const o = overrides.get(g.itemIndex)
    if (o !== undefined) return o
    if (g.itemIndex === currentItemIndex) return true
    return isReady(g) && g.tracks.length > 0 && g.tracks.length <= AUTO_EXPAND_MAX
  }

  const toggle = (g: PlaylistGroup) => {
    const open = isOpen(g)
    setOverrides(prev => new Map(prev).set(g.itemIndex, !open))
    if (!open && (g.status === 'pending' || g.status === 'error')) ensureLoaded(g.itemIndex)
  }

  const ready = totalReadyTracks(groups)

  return (
    <aside className="flex flex-col flex-1 lg:flex-initial lg:flex-shrink-0 lg:w-80 xl:w-96 border-t lg:border-t-0 lg:border-l border-default bg-surface-elevated/50 min-h-0 lg:overflow-hidden">
      <button
        type="button"
        onClick={onClose}
        title={t('player.tracks.hide')}
        className="w-full flex items-center justify-between gap-2 px-3 py-2 border-b border-default flex-shrink-0 text-left cursor-pointer hover:bg-surface-tertiary/40 transition-colors"
      >
        <p className="text-xs text-text-secondary flex items-center gap-2 min-w-0">
          <ListMusic className="w-3.5 h-3.5 text-text-muted flex-shrink-0" />
          <span className="truncate">
            {t('player.tracks.title')}
            <span className="text-text-muted"> · {t('player.tracks.count', { count: ready })}</span>
          </span>
        </p>
        <span className="text-text-muted p-1 flex-shrink-0 pointer-events-none">
          <ChevronRight className="w-4 h-4" />
        </span>
      </button>

      <div className="flex flex-col overflow-y-auto min-h-0 flex-1 lg:flex-none lg:max-h-[62vh]">
        {groups.map(g => (
          <TrackGroup
            key={g.itemIndex}
            group={g}
            isCurrent={g.itemIndex === currentItemIndex}
            open={isOpen(g)}
            selectedFile={selectedFile}
            onToggle={() => toggle(g)}
            onPlayCurrent={playFile}
            onJump={onJump}
            t={t}
          />
        ))}
      </div>
    </aside>
  )
}

// statusBadge — the right-aligned indicator on each group header.
function statusBadge(g: PlaylistGroup, isCurrent: boolean, t: TFn) {
  if (g.status === 'loading') return <Loader2 className="w-3.5 h-3.5 animate-spin text-text-muted" />
  if (g.status === 'error') return <AlertCircle className="w-3.5 h-3.5 text-red-400" aria-label={t('player.tracks.error')} />
  if (isCurrent) {
    return (
      <span className="flex items-center gap-1 text-green-600 dark:text-green-400">
        <span className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse" />
        {t('player.tracks.playing')}
      </span>
    )
  }
  if (isReady(g)) return <span className="tabular-nums text-text-muted">{t('player.tracks.count', { count: g.tracks.length })}</span>
  return null
}

type TrackGroupProps = {
  readonly group: PlaylistGroup
  readonly isCurrent: boolean
  readonly open: boolean
  readonly selectedFile: number
  readonly onToggle: () => void
  readonly onPlayCurrent: (fileIndex: number) => void
  readonly onJump: (itemIndex: number, fileIndex: number) => void
  readonly t: TFn
}

// One playlist item (album/pack/local file) and, when expanded, its tracks.
function TrackGroup({ group, isCurrent, open, selectedFile, onToggle, onPlayCurrent, onJump, t }: TrackGroupProps) {
  const playTrack = (tr: PlaylistTrack) => {
    if (isCurrent) onPlayCurrent(tr.fileIndex)
    else onJump(group.itemIndex, tr.fileIndex)
  }
  return (
    <div className="border-b border-default/60">
      <button
        type="button"
        onClick={onToggle}
        className={`w-full flex items-center gap-2 px-3 py-2 text-left text-xs hover:bg-surface-tertiary/40 transition-colors ${isCurrent ? 'bg-green-500/5' : ''}`}
      >
        {open ? <ChevronDown className="w-3.5 h-3.5 text-text-muted flex-shrink-0" /> : <ChevronRight className="w-3.5 h-3.5 text-text-muted flex-shrink-0" />}
        <span className={`truncate flex-1 min-w-0 ${isCurrent ? 'text-text-primary font-medium' : 'text-text-secondary'}`} title={group.title}>
          {group.title}
        </span>
        <span className="flex-shrink-0 text-[11px]">{statusBadge(group, isCurrent, t)}</span>
      </button>
      {open && group.tracks.length > 0 && (
        <ul className="pb-1">
          {group.tracks.map((tr, i) => {
            const selected = isCurrent && tr.fileIndex === selectedFile
            return (
              <li key={`${tr.fileIndex}-${tr.path}`}>
                <button
                  type="button"
                  onClick={() => playTrack(tr)}
                  className={`w-full flex items-center gap-2 pl-8 pr-3 py-1.5 text-left text-xs hover:bg-surface-tertiary/40 transition-colors ${selected ? 'bg-green-500/15 text-green-700 dark:text-green-300 font-medium' : 'text-text-secondary'}`}
                >
                  <span className="tabular-nums text-text-muted w-6 text-right flex-shrink-0">{i + 1}</span>
                  {tr.kind === 'video'
                    ? <FileVideo className="w-3 h-3 text-blue-400/70 flex-shrink-0" />
                    : <Music2 className="w-3 h-3 text-text-muted flex-shrink-0" />}
                  <span className="truncate min-w-0" title={tr.name}>{tr.name}</span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {open && isReady(group) && group.tracks.length === 0 && (
        <p className="pl-8 pr-3 py-1.5 text-[11px] text-text-muted">{t('player.tracks.empty')}</p>
      )}
    </div>
  )
}
