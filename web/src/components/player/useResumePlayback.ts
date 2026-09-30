import { useEffect, useRef } from 'react'
import { TorrentInfo, libraryGetByHash, libraryUpdateResume, isIOS } from '../../api/client'
import { clientLog } from '../../lib/diag'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// Resume-position plumbing: loads the per-user library entry, persists the final
// position on real unmount, and drives the seek/auto-resume/autoplay decision on
// canplay. State (libraryEntryID/resumePosition/showResumePrompt) is owned by the
// modal and passed in so the new-result reset can clear it in one place.
export function useResumePlayback(deps: {
  info: TorrentInfo | null
  incognito: boolean
  selectedFile: number
  initialSeek?: number
  audioMode: boolean
  blessed: boolean
  videoRef: React.RefObject<HTMLVideoElement | null>
  libraryEntryID: number | null
  resumePosition: number | null
  setLibraryEntryID: Setter<number | null>
  setResumePosition: Setter<number | null>
  setShowResumePrompt: Setter<boolean>
}) {
  const {
    info, incognito, selectedFile, initialSeek, audioMode, blessed, videoRef,
    libraryEntryID, resumePosition, setLibraryEntryID, setResumePosition, setShowResumePrompt,
  } = deps

  const applyLibraryEntry = (entry: { id: number; resumeSeconds: number; durationSeconds: number } | null) => {
    if (!entry) return
    setLibraryEntryID(entry.id)
    if (entry.resumeSeconds > 30 && entry.durationSeconds > 0 && entry.resumeSeconds < entry.durationSeconds - 30) {
      setResumePosition(entry.resumeSeconds)
    }
  }

  // Mirror the values the unmount cleanup needs into a ref, refreshed every
  // render. This lets the cleanup run ONLY on real unmount (deps: []) while
  // still seeing current values — without it, depending on [libraryEntryID]
  // re-ran the cleanup the moment the library entry loaded mid-playback,
  // calling streamDrop() and KILLING the torrent we were actively streaming
  // (ffmpeg then died with "torrent closed" → "Sem seeds").
  const cleanupRef = useRef<{ readonly infoHash: string; readonly libraryEntryID: number | null; readonly fileIndex: number; readonly incognito: boolean }>({ infoHash: '', libraryEntryID: null, fileIndex: -1, incognito: false })
  useEffect(() => {
    cleanupRef.current = { infoHash: info?.infoHash ?? '', libraryEntryID, fileIndex: selectedFile, incognito }
  })

  // Persist final resume position — ONLY when the modal truly unmounts (user
  // closes/navigates), never on intra-playback state changes. Dropping the
  // torrent is handled by the viewer-lease effect below (keyed on the hash), not
  // here, so switching A→B in the same instance releases A as well.
  useEffect(() => {
    return () => {
      const { libraryEntryID: libID, fileIndex, incognito: wasIncognito } = cleanupRef.current
      const v = videoRef.current
      if (!wasIncognito && libID !== null && v && v.currentTime > 1) {
        // Persist which file was watched so reopening a season pack resumes the
        // same episode (not the torrent's primary file).
        libraryUpdateResume(libID, v.currentTime, v.duration || 0, fileIndex >= 0 ? fileIndex : undefined).catch(() => {})
      }
    }
  }, [])

  // After torrent metadata loads, fetch the library entry to know if we have a saved resume position
  useEffect(() => {
    if (!info?.infoHash || incognito) return
    const hash = info.infoHash
    libraryGetByHash(hash).then(applyLibraryEntry).catch(() => {})
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash])

  // One-shot guard for the URL-supplied seek. Without it we'd re-apply the
  // initial seek every time `canplay` fires (which happens on each format
  // negotiation, transcode fallback, etc.), making it impossible to scrub away.
  const appliedInitialSeekRef = useRef(false)
  // Same idea for the library-driven auto-resume — fire once per file selection
  // and then keep `resumePosition` populated so the "Continuar" button can use
  // it after the user goes back to the start.
  const appliedAutoResumeRef = useRef(false)
  // Autoplay nativo (iOS): dispara uma vez por fonte, no canplay sem-resume.
  const autoplayTriedRef = useRef(false)
  useEffect(() => {
    // Reset whenever a new file is selected so a future URL-driven re-play
    // (e.g., navigating to ?play=X&t=...) re-applies the seek instead of
    // remembering "already done" from the previous file.
    appliedInitialSeekRef.current = false
    appliedAutoResumeRef.current = false
    autoplayTriedRef.current = false
    setShowResumePrompt(false)
  }, [selectedFile, info?.infoHash])

  // Seek once the video can play. Priority:
  //   1. URL-supplied initialSeek (explicit, e.g. shared link with `t=120`)
  //   2. per-user library resumeSeconds (background-saved, silent)
  // iosAudio: AUDIO path on iPhone/iPad. Single gate of "tap-to-play": on iOS the
  // play() of audio-bearing media REQUIRES a gesture (Apple's rule), so we turn off
  // non-gesture autoplay and the nudges, show the "Play" overlay and let the user's
  // tap start it. isIOS() (not isSafariBrowser) so we do NOT regress macOS Safari,
  // which plays with normal autoplay. Only depends on audioMode (prop) → valid here.
  const iosAudio = audioMode && isIOS()
  // Sequential (not nested) play attempts: sound first, then muted. Every
  // rejection is handled here, so the returned promise never rejects.
  const autoplayWithMutedFallback = async (v: HTMLVideoElement): Promise<void> => {
    try {
      await v.play()
      clientLog('info', 'player', 'autoplay ok (sound)', {})
      return
    } catch (e) {
      // AbortError ≠ autoplay block (NotAllowedError): the play() was
      // INTERRUPTED by a load()/src swap/element remount while
      // still pending (on iOS the initial buffering window is long).
      // do NOT chain a muted play() on a still-loading element — that only
      // worsens the abort and kills sound for good. Instead, release the
      // one-shot guard so the NEXT loadedmetadata/canplay retries cleanly on the
      // already-settled element (with SOUND). That was the cause of "played then stopped /
      // no sound" on the iPhone.
      if ((e as { name?: string })?.name === 'AbortError') {
        clientLog('warn', 'player', 'autoplay aborted (load interrupted) — will retry', { err: String(e) })
        autoplayTriedRef.current = false
        return
      }
      clientLog('warn', 'player', 'autoplay blocked, trying muted', { err: String(e) })
    }
    v.muted = true
    try {
      await v.play()
      clientLog('info', 'player', 'autoplay ok (muted)', {})
    } catch (error_) {
      clientLog('error', 'player', 'autoplay failed (not even muted)', { err: String(error_) })
    }
  }
  // Autoplay on the NATIVE path (<video> without hls.js): iOS ignores the autoPlay
  // attribute when there's audio, so we try play() explicitly (with muted
  // fallback). Once per source. Not called when we're about to show the resume prompt
  // — then the user picks continue/restart. (The hls.js path already handles
  // autoplay on MANIFEST_PARSED; an extra play() here would be an idempotent no-op.)
  const maybeAutoplayNative = (v: HTMLVideoElement) => {
    if (autoplayTriedRef.current) return
    autoplayTriedRef.current = true
    // iOS-audio NOT yet started (not blessed): do NOT attempt autoplay. Apple forbids
    // play() of audio-bearing media outside a gesture; a non-gesture play() wedges the element
    // at readyState 1 and aborts in a loop. We leave it paused and show the "Play" overlay
    // — the user's tap (gesture) starts it. AFTER it has started (blessed), Apple allows
    // programmatic play() → we fall into the normal path below and the album's next
    // track plays by itself (auto-advance).
    if (iosAudio && !blessed) {
      clientLog('info', 'player', 'iOS: autoplay skipped — waiting for gesture (tap-to-play)', { readyState: v.readyState })
      return
    }
    // DIAGNOSTIC (temporary): logs which autoplay path the device took,
    // to pin down the iOS flakiness — played with SOUND, fell back to MUTED (no gesture),
    // or failed. Same logic as tryAutoplayMutedFallback + logs.
    clientLog('info', 'player', 'autoplay try', { readyState: v.readyState, file: selectedFile })
    void autoplayWithMutedFallback(v)
  }
  const handleVideoCanPlay = () => {
    const v = videoRef.current
    if (!v) return
    if (initialSeek !== undefined && initialSeek > 0 && !appliedInitialSeekRef.current) {
      if (v.currentTime < 1) {
        v.currentTime = initialSeek
      }
      appliedInitialSeekRef.current = true
      // Clear DB resume to avoid the second branch firing on the same canplay
      setResumePosition(null)
      maybeAutoplayNative(v)
      return
    }
    if (resumePosition !== null && v.currentTime < 1 && resumePosition > 30 && !appliedAutoResumeRef.current) {
      appliedAutoResumeRef.current = true
      // Ask instead of silently jumping: the user picks "continue" or "restart"
      // via the overlay (see resume prompt). Mark applied so it only asks once.
      // DIAGNOSTIC (temporary): this path does NOT auto-play (waits for the gesture on
      // the prompt) — if it shows up a lot, it's the cause of "didn't play" on tracks with a position.
      clientLog('info', 'player', 'resume prompt shown (autoplay skipped)', { resumePosition })
      setShowResumePrompt(true)
      return
    }
    // No explicit seek and no resume prompt → starts playing by itself.
    maybeAutoplayNative(v)
  }

  return { handleVideoCanPlay }
}
