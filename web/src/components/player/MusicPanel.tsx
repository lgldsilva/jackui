import { useEffect, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TorrentInfo, isLocalHash, parseLocalHash, localAudioMeta, streamAudioMeta, type AudioMeta } from '../../api/client'
import { usePersistedState } from '../../lib/storage'
import { useWebAudioGraph, type WebAudioGraph } from './useWebAudioGraph'
import { webAudioBlocked } from './playerFormat'
import { Equalizer } from './Equalizer'
import { AudioVisualizer } from './AudioVisualizer'
import { LyricsPanel } from './LyricsPanel'

type MusicPanelProps = {
  readonly videoRef: React.RefObject<HTMLVideoElement | null>
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly currentTime: number
  readonly duration: number
  // isTranscoded === the track plays over HLS (not a direct file). On WebKit that
  // is the one case the Web Audio graph can't tap (see webAudioBlocked).
  readonly isTranscoded: boolean
  // engineGraph: when the gapless/crossfade engine is the audio source, the EQ +
  // visualizer read ITS shared graph (the engine taps its own <audio> elements).
  // null → fall back to tapping the modal's <video> (the single-element path).
  readonly engineGraph?: WebAudioGraph | null
}

type Track = { title: string; artist: string; album: string; year: number }

function currentFileName(info: TorrentInfo | null, idx: number): string {
  return info?.files?.[idx]?.path ?? info?.name ?? ''
}

// parseArtistTitle derives a track identity from a filename ("Artist - Title").
// The basename is stripped of any directory prefix and extension first.
function parseArtistTitle(name: string): Track {
  const base = name.replace(/.*[\\/]/, '').replace(/\.[^.]+$/, '').trim()
  const dash = base.indexOf(' - ')
  if (dash > 0) return { artist: base.slice(0, dash).trim(), title: base.slice(dash + 3).trim(), album: '', year: 0 }
  return { artist: '', title: base, album: '', year: 0 }
}

// useTrack resolves the now-playing identity (for lyrics AND the metadata line).
// Tags come from the server — local files via dhowden/tag on disk, torrent files
// read through the streamer — and fall back to parsing the filename. Re-runs
// when the file changes.
function useTrack(info: TorrentInfo | null, selectedFile: number): Track {
  const name = currentFileName(info, selectedFile)
  const hash = info?.infoHash ?? ''
  const [track, setTrack] = useState<Track>(() => parseArtistTitle(name))
  useEffect(() => {
    const fromName = parseArtistTitle(name)
    setTrack(fromName)
    if (!hash) return
    let cancelled = false
    const apply = (m: AudioMeta) => {
      // Keep the filename-derived title when the tag has none; tags win otherwise.
      if (!cancelled && (m.title || m.artist || m.album)) {
        setTrack({ title: m.title || fromName.title, artist: m.artist, album: m.album, year: m.year })
      }
    }
    const loc = isLocalHash(hash) ? parseLocalHash(hash) : null
    const req = loc
      ? localAudioMeta(loc.mount, loc.path)
      : (selectedFile >= 0 ? streamAudioMeta(hash, selectedFile) : null)
    req?.then(apply).catch(() => {})
    return () => { cancelled = true }
  }, [hash, name, selectedFile])
  return track
}

// MusicPanel is the audio-mode experience surface: spectrum visualizer + 10-band
// equalizer + synced lyrics. Mounted ONLY in audio mode (the parent guards), so
// the Web Audio graph never taps a video element. Each piece lives in its own
// file — this is just the layout + track-identity wiring.
export function MusicPanel({ videoRef, info, selectedFile, currentTime, duration, isTranscoded, engineGraph }: MusicPanelProps) {
  const { t } = useTranslation()
  // Web Audio graph for EQ + visualizer. Mounts on every browser for direct-play
  // audio (incl. iOS); the hook keeps it safe — it only taps the element once the
  // AudioContext is running (unlocked by a user gesture), so audio is never
  // silenced and the EQ/visualizer light up on the first interaction. The one
  // case it can't tap is a transcoded HLS track on WebKit (Safari/iOS) → there it
  // stays off and we show a note instead.
  // When the gapless engine is the audio source it provides its OWN dual graph;
  // we read that and DON'T mount the single-element graph on the <video> (which
  // is muted/srcless then). The engine only runs on direct-play → never blocked.
  const ownGraph = useWebAudioGraph(videoRef, !engineGraph, isTranscoded)
  const graph = engineGraph ?? ownGraph
  const blocked = !engineGraph && webAudioBlocked(isTranscoded)
  const track = useTrack(info, selectedFile)
  const [open, setOpen] = usePersistedState<boolean>('audio:toolsOpen', false)
  const metaLine = [track.artist, track.album, track.year ? String(track.year) : '']
    .map((s) => s.trim())
    .filter(Boolean)
    .join(' · ')
  return (
    <div className="mt-3">
      {metaLine && (
        <p className="mb-2 truncate text-xs text-text-muted" title={metaLine}>{metaLine}</p>
      )}
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
            {blocked ? (
              <p className="rounded-lg bg-surface-2 p-3 text-xs text-text-muted">{t('player.eqUnavailable')}</p>
            ) : (
              <>
                <AudioVisualizer analyser={graph.analyser} />
                <Equalizer graph={graph} />
              </>
            )}
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
