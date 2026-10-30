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
import { useSubtitleOffset, useTrackProbe, useSubtitleChoicePersist, useSubtitleAutoFetch, type AutoSubPick } from './playerHooks'
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
  // settings.auto_fetch (GET /api/settings/subtitles), threaded by PlayerModal.
  // A failed settings load lands here as false — fail closed, because calling
  // the status endpoint KICKS the fetch server-side.
  readonly autoFetchEnabled: boolean
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
  const { videoRef, info, selectedFile, serverReady, result, mediaToken, setProbe, autoFetchEnabled } = opts
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
  // Virtual auto subtitle (lane W2): the OpenSubtitles file the auto pipeline
  // fetched, applied WITHOUT touching subActive/sidecarIdx (never persisted —
  // on replay the same effect re-applies it against the server cache). Null
  // once ANY manual pick happens: a user decision disables the auto entry.
  const [autoSub, setAutoSub] = useState<AutoSubPick | null>(null)
  // True when the pipeline ended in `failed` — hint text in the picker (no toast
  // unless the reason is quota).
  const [autoFailed, setAutoFailed] = useState(false)
  // Sticky per-file flag: any manual subtitle interaction during this file
  // blocks (re-)applying the auto pick, even if the pick is removed again.
  const [manualSubPicked, setManualSubPicked] = useState(false)
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

    // A manual upload is a user decision — it replaces (and disables) the auto pick.
    setManualSubPicked(true)
    setAutoSub(null)
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
      // Prefer hash-based auto search (frame-exact) — single API call, results ranked by relevance.
      // No explicit langs (RF2): the backend default (settings-driven DefaultLanguages) applies,
      // so an admin-configured language list reaches the manual panel too.
      const resp = await subtitlesAuto(info.infoHash, selectedFile)
      setSubResults(resp.results || [])
      if (resp.osHash && !resp.hashErr) setAutoSource('hash')
      else setAutoSource('title')
    } catch {
      // Fall back to plain title search if auto endpoint fails
      try {
        const baseTitle = info.name || result.title
        const { season, episode, cleanQuery } = parseSeasonEpisode(baseTitle)
        // Same RF2 rule as the auto search: omit langs, let the settings-driven backend default apply.
        const data = await subtitlesSearch(cleanQuery || baseTitle, { season, episode })
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
    // A manual search pick replaces (and disables) the virtual auto subtitle.
    setManualSubPicked(true)
    setAutoSub(null)
    setAutoFailed(false)
    setSubActive(s.id)
    clearCustomSub()
  }

  // Update offset and reapply to all cues
  const adjustSubOffset = (delta: number) => {
    setSubOffset((prev) => Math.round((prev + delta) * 10) / 10)
  }

  const resetSubOffset = () => setSubOffset(0)

  // Apply subtitle offset whenever active sub or offset changes (and reset the
  // cue snapshot when the subtitle changes). The virtual auto subtitle and the
  // custom upload are included so their (always late-arriving) <track src> gets
  // the Safari activation + working offset controls too (review F4).
  useSubtitleOffset({ videoRef, subActive, embeddedSub, sidecarIdx, customSubURL, localEmbeddedVttURL, autoSub, subOffset, origCuesRef })

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

  // Auto subtitle pipeline (lane W2): polls subtitleAutoStatus once per file and
  // applies the result as the VIRTUAL auto subtitle when the user has no saved
  // choice and nothing is picked yet. Side-effect only — never blocks playback.
  // The pick is NEVER persisted: the choice-persist effect above only saves
  // subActive/embeddedSub/sidecarIdx, so on replay the effect below re-applies
  // it against the server's cache (zero extra OpenSubtitles calls, RF7).
  useSubtitleAutoFetch({
    info, selectedFile, serverReady,
    isVideoFile: !!info?.files[selectedFile]?.isVideo,
    autoFetchEnabled,
    manualPicked: manualSubPicked,
    subActive, embeddedSub, sidecarIdx, customSubURL,
    onReady: (sub) => {
      setAutoSub(sub)
      notify(t('player.subtitles.autoApplied'), 'success')
    },
    onFailed: (reason) => {
      setAutoFailed(true)
      // Quota is worth a toast (it affects every future fetch today); other
      // failures stay a quiet hint inside the picker.
      if (reason === 'quota') notify(t('player.subtitles.autoQuota'), 'info')
    },
  })

  // Probe/auto-pick arbitration (review F3): the probe's embedded/sidecar pt
  // auto-pick fires through the RAW setters (it is not a user decision), so it
  // can land AFTER the auto pipeline already applied the virtual auto subtitle
  // (server-cached ready answer resolving before the ffprobe — the designed RF7
  // replay path). Without arbitration BOTH stay "active": the displayed <track>
  // flips to the embedded pick (embedded precedes autoSub in buildSubtitleVttURL)
  // while the picker still claims the auto entry and burn keeps targeting the
  // auto id. When an embedded/sidecar pick appears, the auto entry yields: clear
  // autoSub WITHOUT setting manualSubPicked — neither side is a user decision,
  // and the next file must stay free to auto-apply again. Manual picks already
  // clear autoSub themselves, so this never resurrects a cleared entry.
  useEffect(() => {
    if (embeddedSub === null && sidecarIdx === null) return
    setAutoSub(null)
  }, [embeddedSub, sidecarIdx])

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
    setAutoSub(null)
    setAutoFailed(false)
    setManualSubPicked(false)
    clearCustomSub()
    origCuesRef.current = []
  }

  // Per-file reset on playFile — SUBSET of the above (matches playFile's inline
  // subtitle resets; it intentionally keeps the search results / offset / custom
  // sub so switching episodes doesn't clear a manual pick's panel state).
  // The auto state resets WITH the file: the next episode polls fresh, and the
  // manual-disable window is per file (a pick for episode 1 doesn't gag episode 2).
  const resetSubtitlesForFile = () => {
    setSidecarIdx(null)
    setEmbeddedSub(null)
    setSubActive(null)
    setSidecars([])
    setSubRestored(false)
    setAutoSub(null)
    setAutoFailed(false)
    setManualSubPicked(false)
  }

  const subtitleLabel = getSubtitleLabel(embeddedSub, subActive, autoSource, subLoading, autoSub !== null)

  return {
    // state
    subOpen, subResults, subLoading, subError, subActive, subOffset, autoSource,
    embeddedSub, customSubURL, customSubName, localEmbeddedVttURL, sidecars, sidecarIdx,
    autoSub, autoFailed,
    subtitleLabel,
    // setters consumed by PlayerControlsPanel — WRAPPED so any explicit user
    // interaction (pick, switch, removal) marks the session as manually picked
    // and drops the virtual auto subtitle. Internal effects (probe auto-pick,
    // choice restore) keep the raw setters.
    setSubOpen,
    setSubActive: (v: SetStateAction<string | null>) => { setManualSubPicked(true); setAutoSub(null); setSubActive(v) },
    setEmbeddedSub: (v: SetStateAction<number | null>) => { setManualSubPicked(true); setAutoSub(null); setEmbeddedSub(v) },
    setSidecarIdx: (v: SetStateAction<number | null>) => { setManualSubPicked(true); setAutoSub(null); setSidecarIdx(v) },
    setAutoSource,
    // handlers
    handleCustomSubtitleUpload, clearCustomSub, openSubtitlePanel, pickSubtitle,
    adjustSubOffset, resetSubOffset,
    // resets
    resetSubtitles, resetSubtitlesForFile,
  }
}
