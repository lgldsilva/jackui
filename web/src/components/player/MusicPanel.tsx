import { useEffect, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TorrentInfo, isLocalHash, parseLocalHash, localAudioMeta } from '../../api/client'
import { usePersistedState } from '../../lib/storage'
import { useWebAudioGraph } from './useWebAudioGraph'
import { Equalizer } from './Equalizer'
import { AudioVisualizer } from './AudioVisualizer'
import { LyricsPanel } from './LyricsPanel'

type MusicPanelProps = {
  readonly videoRef: React.RefObject<HTMLVideoElement | null>
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly currentTime: number
  readonly duration: number
}

type Track = { title: string; artist: string; album: string }

function currentFileName(info: TorrentInfo | null, idx: number): string {
  return info?.files?.[idx]?.path ?? info?.name ?? ''
}

// parseArtistTitle derives a track identity from a filename ("Artist - Title").
// The basename is stripped of any directory prefix and extension first.
function parseArtistTitle(name: string): Track {
  const base = name.replace(/.*[\\/]/, '').replace(/\.[^.]+$/, '').trim()
  const dash = base.indexOf(' - ')
  if (dash > 0) return { artist: base.slice(0, dash).trim(), title: base.slice(dash + 3).trim(), album: '' }
  return { artist: '', title: base, album: '' }
}

// useTrack resolves the now-playing identity for lyrics. Local files get clean
// tags from the server (dhowden/tag); torrents fall back to parsing the
// filename. Re-runs when the file changes.
function useTrack(info: TorrentInfo | null, selectedFile: number): Track {
  const name = currentFileName(info, selectedFile)
  const hash = info?.infoHash ?? ''
  const [track, setTrack] = useState<Track>(() => parseArtistTitle(name))
  useEffect(() => {
    setTrack(parseArtistTitle(name))
    if (!hash || !isLocalHash(hash)) return
    const loc = parseLocalHash(hash)
    if (!loc) return
    let cancelled = false
    localAudioMeta(loc.mount, loc.path)
      .then((m) => { if (!cancelled && m.title) setTrack({ title: m.title, artist: m.artist, album: m.album }) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [hash, name])
  return track
}

// MusicPanel is the audio-mode experience surface: spectrum visualizer + 10-band
// equalizer + synced lyrics. Mounted ONLY in audio mode (the parent guards), so
// the Web Audio graph never taps a video element. Each piece lives in its own
// file — this is just the layout + track-identity wiring.
export function MusicPanel({ videoRef, info, selectedFile, currentTime, duration }: MusicPanelProps) {
  const { t } = useTranslation()
  // The graph is built unconditionally (audio mode) so the saved EQ curve applies
  // even while the tools are collapsed. Collapsed BY DEFAULT so the track list
  // below isn't pushed off-screen on phones — the #1 mobile complaint. Persisted,
  // so once a user opens the tools they stay open.
  const graph = useWebAudioGraph(videoRef, true)
  const track = useTrack(info, selectedFile)
  const [open, setOpen] = usePersistedState<boolean>('audio:toolsOpen', false)
  return (
    <div className="mt-3">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 rounded-lg bg-surface-2 px-3 py-2 text-sm font-medium text-text hover:bg-surface-3"
      >
        <ChevronRight className={`h-4 w-4 transition-transform ${open ? 'rotate-90' : ''}`} />
        {t('player.audioTools')}
      </button>
      {open && (
        <div className="mt-3 grid gap-3 lg:grid-cols-2">
          <div className="flex flex-col gap-3">
            <AudioVisualizer analyser={graph.analyser} />
            <Equalizer graph={graph} />
          </div>
          <LyricsPanel
            title={track.title}
            artist={track.artist}
            album={track.album}
            durationSec={duration}
            currentTime={currentTime}
          />
        </div>
      )}
    </div>
  )
}
