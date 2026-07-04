import { Dispatch, RefObject, SetStateAction, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Subtitle,
  SidecarSubtitle,
  StreamProbe,
  SearchResult,
  TorrentInfo,
  subtitlesSearch,
  subtitlesAuto,
  isLocalHash,
  localSubtrackBlobURL,
} from '../../api/client'
import { getSubtitleLabel } from './playerFormat'
import { useSubtitleOffset, useTrackProbe, useSubtitleChoicePersist } from './playerHooks'
import { useToast } from '../Toast'

type UseSubtitlesOpts = {
  readonly videoRef: RefObject<HTMLVideoElement>
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly serverReady: boolean
  readonly result: SearchResult | null
  readonly mediaToken: string
  // probe (ffprobe: audio + subtitle tracks) stays owned by PlayerModal because
  // it also drives audio auto-transcode + the HEVC backstop; useTrackProbe (run
  // here) populates it via this setter alongside the subtitle auto-pick.
  readonly setProbe: Dispatch<SetStateAction<StreamProbe | null>>
}

// useSubtitles owns the whole subtitle cluster extracted from PlayerModal: the
// external/embedded/sidecar/custom track state, the OpenSubtitles search panel,
// the sync offset, the local-embedded blob fetch, and the three lower-level
// subtitle effects (offset apply, ffprobe track discovery, per-file choice
// persistence). Behavior is unchanged — same effect bodies, same deps, same
// gating; PlayerModal now consumes the returned state/handlers. The two reset
// helpers reproduce the exact subtitle-state resets the modal did inline on a
// torrent switch (resetSubtitles) and on a file switch (resetSubtitlesForFile).
export function useSubtitles(opts: UseSubtitlesOpts) {
  const { videoRef, info, selectedFile, serverReady, result, mediaToken, setProbe } = opts
  const { t } = useTranslation()
  const { notify } = useToast()

  const [subOpen, setSubOpen] = useState(false)
  const [subResults, setSubResults] = useState<Subtitle[]>([])
  const [subLoading, setSubLoading] = useState(false)
  const [subError, setSubError] = useState('')
  const [subActive, setSubActive] = useState<string | null>(null)
  const [subOffset, setSubOffset] = useState(0) // seconds; +/-0.1s steps
  // True once we've restored (or decided there's nothing to restore) the saved
  // subtitle choice for the current file. Gates the save effect so the reset on
  // file-switch doesn't persist an empty choice before restore runs.
  const [subRestored, setSubRestored] = useState(false)
  const [autoSource, setAutoSource] = useState<'hash' | 'title' | 'embedded' | null>(null)
  const [embeddedSub, setEmbeddedSub] = useState<number | null>(null) // selected embedded sub track index
  const [customSubURL, setCustomSubURL] = useState<string | null>(null)
  const [customSubName, setCustomSubName] = useState<string | null>(null)
  // Blob URL of a LOCAL embedded sub fetched with retry — the server extracts
  // large rclone files in the background (503 until ready), so a <track src>
  // pointing straight at the endpoint would 502/hang. '' until extracted.
  const [localEmbeddedVttURL, setLocalEmbeddedVttURL] = useState('')
  // Sidecar subtitle files (separate .srt/.vtt inside the torrent)
  const [sidecars, setSidecars] = useState<SidecarSubtitle[]>([])
  const [sidecarIdx, setSidecarIdx] = useState<number | null>(null) // selected sidecar file index
  // Store the original (un-offset) cue timings the first time we see them
  const origCuesRef = useRef<{ start: number; end: number }[]>([])

  useEffect(() => {
    return () => {
      setCustomSubURL(prev => {
        if (prev) URL.revokeObjectURL(prev)
        return null
      })
    }
  }, [])

  const handleCustomSubtitleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return

    let text: string
    try {
      text = await file.text()
    } catch {
      notify(t('player.modal.subtitleReadError'), 'error')
      return
    }

    const vttContent = file.name.endsWith('.srt')
      ? 'WEBVTT\n\n' + text.replaceAll(/(\d{2}:\d{2}:\d{2}),(\d{3})/g, '$1.$2')
      : text

    setCustomSubURL(prev => {
      if (prev) URL.revokeObjectURL(prev)
      return null
    })

    const blob = new Blob([vttContent], { type: 'text/vtt' })
    const url = URL.createObjectURL(blob)
    setCustomSubURL(url)
    setCustomSubName(file.name)

    setSubActive(null)
    setSidecarIdx(null)
    setEmbeddedSub(null)
    setAutoSource(null)
  }

  // Drop any uploaded custom subtitle (revoking its blob URL) when the user
  // switches to an embedded/sidecar/external track instead.
  const clearCustomSub = () => {
    setCustomSubURL(prev => { if (prev) { URL.revokeObjectURL(prev) } return null })
    setCustomSubName(null)
  }

  // Fetch (with retry) the selected LOCAL embedded subtitle as a VTT blob. Polls
  // while the server reports "extracting"; sets the blob when ready so the track
  // appears without the player hanging. Revokes the previous blob on change.
  // (Placed after mediaToken so its value is available when the effect runs.)
  useEffect(() => {
    setLocalEmbeddedVttURL(prev => {
      if (prev) URL.revokeObjectURL(prev)
      return ''
    })
    if (!info || !isLocalHash(info.infoHash) || embeddedSub === null) return
    let cancelled = false
    localSubtrackBlobURL(info.infoHash, selectedFile, embeddedSub, mediaToken, () => cancelled)
      .then(url => {
        if (cancelled) {
          if (url) URL.revokeObjectURL(url)
          return
        }
        if (url) setLocalEmbeddedVttURL(url)
      })
    return () => { cancelled = true }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash, selectedFile, embeddedSub, mediaToken])

  // Detect season/episode from title for better subtitle matches
  const parseSeasonEpisode = (title: string): { season?: number; episode?: number; cleanQuery: string } => {
    const match = /[Ss](\d{1,2})[Ee](\d{1,3})/.exec(title)
    if (!match) return { cleanQuery: title }
    return {
      season: Number.parseInt(match[1]),
      episode: Number.parseInt(match[2]),
      cleanQuery: title.slice(0, match.index).trim().replaceAll(/[._]/g, ' '),
    }
  }

  const openSubtitlePanel = async () => {
    setSubOpen(true)
    if (subResults.length > 0 || !result || !info) return
    setSubLoading(true)
    setSubError('')
    try {
      // Prefer hash-based auto search (frame-exact) — single API call, results ranked by relevance
      const resp = await subtitlesAuto(info.infoHash, selectedFile, 'pt-BR,pt')
      setSubResults(resp.results || [])
      if (resp.osHash && !resp.hashErr) setAutoSource('hash')
      else setAutoSource('title')
    } catch {
      // Fall back to plain title search if auto endpoint fails
      try {
        const baseTitle = info.name || result.title
        const { season, episode, cleanQuery } = parseSeasonEpisode(baseTitle)
        const data = await subtitlesSearch(cleanQuery || baseTitle, { season, episode, langs: 'pt-BR,pt' })
        setSubResults(data || [])
        setAutoSource('title')
      } catch (error_: any) {
        setSubError(error_?.response?.data?.error || error_.message || t('player.modal.subtitleSearchError'))
      }
    } finally {
      setSubLoading(false)
    }
  }

  const pickSubtitle = (s: Subtitle) => {
    // Apply but keep the panel open so the active subtitle shows its ✓/highlight
    // and the user can switch or remove it without reopening. They close it via
    // the ✕ (or the "Legendas" toggle) when done.
    setSubActive(s.id)
    clearCustomSub()
  }

  // Update offset and reapply to all cues
  const adjustSubOffset = (delta: number) => {
    setSubOffset((prev) => Math.round((prev + delta) * 10) / 10)
  }

  const resetSubOffset = () => setSubOffset(0)

  // Apply subtitle offset whenever active sub or offset changes (and reset the
  // cue snapshot when the subtitle changes).
  useSubtitleOffset({ videoRef, subActive, embeddedSub, sidecarIdx, localEmbeddedVttURL, subOffset, origCuesRef })

  // Probe container for embedded audio + subtitle tracks (uses ffprobe on first ~16MB).
  // Gated by serverReady so we don't fire while the torrent is still warming up —
  // ffprobe needs a live Reader from the streamer's active map.
  useTrackProbe({
    info, selectedFile, serverReady, subActive, embeddedSub,
    setProbe, setEmbeddedSub, setAutoSource, setSidecars, setSidecarIdx,
  })

  // Restore the saved subtitle choice for this file (external/embedded/sidecar
  // + offset). Runs before the pt auto-load gets a chance (which is gated by
  // hasSavedChoice in the probe effect), so the user's pick wins. subRestored
  // gates the save effect below so the file-switch reset can't persist an empty
  // choice before this runs.
  useSubtitleChoicePersist({
    info, selectedFile, subRestored, subActive, embeddedSub, sidecarIdx, subOffset,
    setSubActive, setEmbeddedSub, setSidecarIdx, setSubOffset, setAutoSource, setSubRestored,
  })

  // Full reset on a torrent/result switch — matches the block the [result]
  // effect ran inline. probe is reset by PlayerModal (it owns that state).
  const resetSubtitles = () => {
    setSubActive(null)
    setSubResults([])
    setSubError('')
    setSubOpen(false)
    setSubOffset(0)
    setAutoSource(null)
    setEmbeddedSub(null)
    setSidecars([])
    setSubRestored(false)
    setSidecarIdx(null)
    clearCustomSub()
    origCuesRef.current = []
  }

  // Per-file reset on playFile — SUBSET of the above (matches playFile's inline
  // subtitle resets; it intentionally keeps the search results / offset / custom
  // sub so switching episodes doesn't clear a manual pick's panel state).
  const resetSubtitlesForFile = () => {
    setSidecarIdx(null)
    setEmbeddedSub(null)
    setSubActive(null)
    setSidecars([])
    setSubRestored(false)
  }

  const subtitleLabel = getSubtitleLabel(embeddedSub, subActive, autoSource, subLoading)

  return {
    // state
    subOpen, subResults, subLoading, subError, subActive, subOffset, autoSource,
    embeddedSub, customSubURL, customSubName, localEmbeddedVttURL, sidecars, sidecarIdx,
    subtitleLabel,
    // setters consumed by PlayerControlsPanel
    setSubOpen, setSubActive, setEmbeddedSub, setSidecarIdx, setAutoSource,
    // handlers
    handleCustomSubtitleUpload, clearCustomSub, openSubtitlePanel, pickSubtitle,
    adjustSubOffset, resetSubOffset,
    // resets
    resetSubtitles, resetSubtitlesForFile,
  }
}
