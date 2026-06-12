import { useEffect, useState } from 'react'
import { TorrentInfo, isLocalHash, parseLocalHash, localAudioMeta } from '../../api/client'
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
  const graph = useWebAudioGraph(videoRef, true)
  const track = useTrack(info, selectedFile)
  return (
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
  )
}
