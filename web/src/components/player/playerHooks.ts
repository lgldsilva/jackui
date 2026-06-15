import { Dispatch, MutableRefObject, RefObject, SetStateAction, useEffect, useMemo, useState } from 'react'
import {
  StreamProbe,
  TorrentInfo,
  TranscodeCapabilities,
  SidecarSubtitle,
  streamProbe,
  streamSidecars,
} from '../../api/client'
import { clientLog } from '../../lib/diag'
import { fileKind } from '../../lib/playable'
import { load, save } from '../../lib/storage'

// Per-file subtitle choice persisted in localStorage (mirrors the type in
// PlayerModal). Kept local to avoid a circular import.
type SubChoiceLite = {
  readonly external: string | null
  readonly embedded: number | null
  readonly sidecar: number | null
  readonly offset: number
}

// Hooks extracted verbatim from PlayerModal to shrink that 2000+ line component
// and make these self-contained side effects independently readable/testable.
// Behavior is unchanged — same effect bodies, same dependency arrays.

type KeyboardShortcutsOpts = {
  // HTMLMediaElement (não HTMLVideoElement) pra aceitar tanto o <video> quanto o
  // <audio> do motor gapless — só mexe em play/pause/seek/volume (membros comuns).
  readonly videoRef: RefObject<HTMLMediaElement | null>
  readonly minimized: boolean
  readonly requestFullscreen: () => void
}

// useKeyboardShortcuts wires space/arrows/M/F to the <video>. Skipped while
// minimized, while typing in an input/select, and when the <video> itself has
// focus (so the browser's native handler acts and we don't double-seek).
export function useKeyboardShortcuts({ videoRef, minimized, requestFullscreen }: KeyboardShortcutsOpts) {
  useEffect(() => {
    if (minimized) return
    const handleKeyDown = (e: KeyboardEvent) => {
      const v = videoRef.current
      if (!v) return
      const tgt = e.target as HTMLElement | null
      if (tgt && (tgt.tagName === 'INPUT' || tgt.tagName === 'TEXTAREA' || tgt.tagName === 'SELECT' || tgt === v)) return
      const dur = Number.isFinite(v.duration) ? v.duration : Infinity
      switch (e.key) {
        case ' ': e.preventDefault(); if (v.paused) v.play().catch(() => {}); else v.pause(); break
        case 'ArrowRight': e.preventDefault(); v.currentTime = Math.min(dur, v.currentTime + 10); break
        case 'ArrowLeft': e.preventDefault(); v.currentTime = Math.max(0, v.currentTime - 10); break
        case 'ArrowUp': e.preventDefault(); v.volume = Math.min(1, v.volume + 0.1); break
        case 'ArrowDown': e.preventDefault(); v.volume = Math.max(0, v.volume - 0.1); break
        case 'm': case 'M': v.muted = !v.muted; break
        case 'f': case 'F': requestFullscreen(); break
      }
    }
    globalThis.addEventListener('keydown', handleKeyDown)
    return () => globalThis.removeEventListener('keydown', handleKeyDown)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [minimized])
}

// WebKit-only AirPlay surface (Safari/iOS) — not present in lib.dom types.
interface WebKitAirPlayVideo {
  webkitShowPlaybackTargetPicker?: () => void
  webkitCurrentPlaybackTargetIsWireless?: boolean
}
type WebKitAvailabilityEvent = Event & { availability?: 'available' | 'not-available' }

export type AirPlayState = {
  /** A device is reachable on the network → worth showing the button. */
  readonly available: boolean
  /** Playback is currently routed to an AirPlay target. */
  readonly active: boolean
  /** Opens the native AirPlay route picker. */
  readonly show: () => void
}

// useAirPlay surfaces AirPlay state for the <video> via the WebKit API (Safari/
// iOS). The standard Remote Playback API doesn't cover AirPlay reliably in
// Safari, so we use webkit hooks: `webkitplaybacktargetavailabilitychanged`
// fires an initial state on registration plus every change, and
// `webkitShowPlaybackTargetPicker()` opens the native picker. Listeners are
// removed on cleanup — Apple warns that monitoring availability drains battery.
// `srcKey` (the stream URL) is a dep so the listeners re-attach when the <video>
// is remounted on a source/fallback change.
export function useAirPlay(videoRef: RefObject<HTMLVideoElement | null>, srcKey: string): AirPlayState {
  const [available, setAvailable] = useState(false)
  const [active, setActive] = useState(false)

  useEffect(() => {
    const el = videoRef.current as (HTMLVideoElement & WebKitAirPlayVideo) | null
    if (!el || typeof el.webkitShowPlaybackTargetPicker !== 'function') return
    const onAvail = (e: Event) => setAvailable((e as WebKitAvailabilityEvent).availability === 'available')
    const onWireless = () => setActive(!!el.webkitCurrentPlaybackTargetIsWireless)
    el.addEventListener('webkitplaybacktargetavailabilitychanged', onAvail)
    el.addEventListener('webkitcurrentplaybacktargetiswirelesschanged', onWireless)
    return () => {
      el.removeEventListener('webkitplaybacktargetavailabilitychanged', onAvail)
      el.removeEventListener('webkitcurrentplaybacktargetiswirelesschanged', onWireless)
    }
  }, [videoRef, srcKey])

  const show = () => {
    const el = videoRef.current as (HTMLVideoElement & WebKitAirPlayVideo) | null
    el?.webkitShowPlaybackTargetPicker?.()
  }

  return { available, active, show }
}

export type MediaQueue = {
  /** File indices of playable files of the SAME kind as the current one, in file order. */
  readonly indices: number[]
  /** Position of selectedFile inside `indices` (-1 if not in queue). */
  readonly cursor: number
  /** File index of the previous track/episode, or -1 at the start. */
  readonly prevIdx: number
  /** File index of the next track/episode, or -1 at the end. */
  readonly nextIdx: number
}

// buildMediaQueue is the pure core of useMediaQueue: the playable files of the
// SAME kind as the current one (audio↔audio in an album, video↔video in a
// series) IN THE ORDER GIVEN, with prev/next around selectedFile. The caller
// passes the files in DISPLAY order (filterAndSortFiles) so the next/prev
// buttons walk the list exactly as the user sees it — torrents rarely store
// episodes in file order, and the old file-order queue jumped around.
export function buildMediaQueue(files: readonly TorrentInfo['files'][number][], selectedFile: number): MediaQueue {
  const cur = files.find(f => f.index === selectedFile)
  const curKind = cur ? fileKind(cur.path, cur.isVideo) : 'other'
  const indices = curKind === 'other'
    ? []
    : files.filter(f => fileKind(f.path, f.isVideo) === curKind).map(f => f.index)
  const cursor = indices.indexOf(selectedFile)
  const prevIdx = cursor > 0 ? indices[cursor - 1] : -1
  const nextIdx = cursor >= 0 && cursor < indices.length - 1 ? indices[cursor + 1] : -1
  return { indices, cursor, prevIdx, nextIdx }
}

// useMediaQueue builds the in-torrent track/episode queue for the file
// currently playing. `orderedFiles` (when given) is the sidebar's display
// order — queue and list must agree; without it, falls back to file order.
export function useMediaQueue(info: TorrentInfo | null, selectedFile: number, orderedFiles?: readonly TorrentInfo['files'][number][]): MediaQueue {
  return useMemo(
    () => buildMediaQueue(orderedFiles ?? info?.files ?? [], selectedFile),
    [info, selectedFile, orderedFiles],
  )
}

type MediaSessionOpts = {
  // HTMLMediaElement: aceita o <video> ou o <audio> ativo do motor gapless.
  readonly videoRef: RefObject<HTMLMediaElement | null>
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly playlistName?: string
  readonly onNext?: () => void
  readonly onPrev?: () => void
}

// useMediaSession exposes "what's playing" + media keys / lock-screen controls
// to the OS. Without it, iOS shows "JackUI" with no metadata and AirPods/
// bluetooth controls don't fire next/previous on the playlist.
export function useMediaSession({ videoRef, info, selectedFile, playlistName, onNext, onPrev }: MediaSessionOpts) {
  useEffect(() => {
    if (!info || selectedFile < 0) return
    if (!('mediaSession' in navigator)) return
    const file = info.files[selectedFile]
    const title = file?.path?.split('/').pop() || info.name
    navigator.mediaSession.metadata = new MediaMetadata({
      title,
      album: playlistName || info.name,
      artist: 'JackUI',
    })
    const v = () => videoRef.current
    navigator.mediaSession.setActionHandler('play', () => { v()?.play().catch(() => {}) })
    navigator.mediaSession.setActionHandler('pause', () => { v()?.pause() })
    navigator.mediaSession.setActionHandler('previoustrack', () => onPrev?.())
    navigator.mediaSession.setActionHandler('nexttrack', () => onNext?.())
    navigator.mediaSession.setActionHandler('seekto', (d) => {
      const el = v()
      if (el && d.seekTime != null) el.currentTime = d.seekTime
    })
    return () => {
      try {
        navigator.mediaSession.setActionHandler('play', null)
        navigator.mediaSession.setActionHandler('pause', null)
        navigator.mediaSession.setActionHandler('previoustrack', null)
        navigator.mediaSession.setActionHandler('nexttrack', null)
        navigator.mediaSession.setActionHandler('seekto', null)
      } catch {}
    }
  }, [info?.infoHash, selectedFile, playlistName, onNext, onPrev])
}

type SubtitleOffsetOpts = {
  readonly videoRef: RefObject<HTMLVideoElement>
  readonly subActive: string | null
  readonly subOffset: number
  readonly origCuesRef: MutableRefObject<{ start: number; end: number }[]>
}

// useSubtitleOffset applies the user-chosen sync offset to every cue of the
// active text track, snapshotting the original timings once per loaded sub so
// repeated offset changes stay relative to the source. Also clears the snapshot
// whenever the active subtitle changes. Extracted verbatim from PlayerModal.
export function useSubtitleOffset({ videoRef, subActive, subOffset, origCuesRef }: SubtitleOffsetOpts) {
  useEffect(() => {
    const v = videoRef.current
    if (!v || !subActive) return

    const applyOffset = () => {
      const track = v.textTracks?.[0]
      if (!track?.cues?.length) return
      // Save originals once per loaded sub
      if (origCuesRef.current.length !== track.cues.length) {
        origCuesRef.current = Array.from(track.cues).map((c: any) => ({
          start: c.startTime,
          end: c.endTime,
        }))
      }
      Array.from(track.cues).forEach((cue: any, i) => {
        const orig = origCuesRef.current[i]
        if (!orig) return
        cue.startTime = Math.max(0, orig.start + subOffset)
        cue.endTime = Math.max(0, orig.end + subOffset)
      })
      track.mode = 'showing'
    }

    // Try now, and again when the track finishes loading
    applyOffset()
    const tracks = v.textTracks
    const onLoad = () => applyOffset()
    for (const track of tracks) {
      track.addEventListener('cuechange', onLoad)
    }
    return () => {
      for (const track of tracks) {
        track.removeEventListener('cuechange', onLoad)
      }
    }
  }, [subActive, subOffset])

  // Reset original cue timings when subtitle changes
  useEffect(() => {
    origCuesRef.current = []
  }, [subActive])
}

type TrackProbeOpts = {
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly serverReady: boolean
  readonly subActive: string | null
  readonly embeddedSub: number | null
  readonly setProbe: Dispatch<SetStateAction<StreamProbe | null>>
  readonly setEmbeddedSub: Dispatch<SetStateAction<number | null>>
  readonly setAutoSource: Dispatch<SetStateAction<'hash' | 'title' | 'embedded' | null>>
  readonly setSidecars: Dispatch<SetStateAction<SidecarSubtitle[]>>
  readonly setSidecarIdx: Dispatch<SetStateAction<number | null>>
}

// useTrackProbe runs ffprobe (embedded tracks) + sidecar discovery once the
// torrent is live, auto-picking a pt subtitle unless the user already saved a
// choice for this file. Extracted verbatim from PlayerModal — same gating,
// same stale-closure-safe storage read, same deps.
export function useTrackProbe(opts: TrackProbeOpts) {
  const { info, selectedFile, serverReady, subActive, embeddedSub,
    setProbe, setEmbeddedSub, setAutoSource, setSidecars, setSidecarIdx } = opts
  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0 || !serverReady) return
    // If the user previously chose a subtitle for THIS file, skip auto-load —
    // the restore effect applies that choice and it must win over pt auto-pick.
    // Read storage directly (not state) to avoid stale-closure races with the
    // async probe callback.
    const savedChoice = load<SubChoiceLite | null>(`sub.${info.infoHash}.${selectedFile}`, null)
    const hasSavedChoice = !!savedChoice && (savedChoice.external !== null || savedChoice.embedded !== null || savedChoice.sidecar !== null)

    streamProbe(info.infoHash, selectedFile)
      .then(p => {
        const safe = { audio: p.audio ?? [], subtitles: p.subtitles ?? [] }
        setProbe(safe)
        const ptSub = safe.subtitles.find(t => !t.image && /^(pt|por)/i.test(t.language || ''))
        if (ptSub && !hasSavedChoice && !subActive) {
          setEmbeddedSub(ptSub.index)
          setAutoSource('embedded')
        }
      })
      .catch(err => console.warn('probe failed:', err?.response?.data?.error || err.message))

    // Sidecar subtitle files (separate .srt in the torrent) — cheap, parallel with probe
    streamSidecars(info.infoHash, selectedFile)
      .then(list => {
        setSidecars(list ?? [])
        // Auto-pick pt sidecar if no embedded already chosen and no saved choice
        if (!hasSavedChoice && !subActive && embeddedSub === null && list && list.length > 0) {
          const pt = list.find(s => /^(pt|por)/i.test(s.language || ''))
          if (pt) {
            setSidecarIdx(pt.index)
            setAutoSource('embedded')
          }
        }
      })
      .catch(() => setSidecars([]))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash, selectedFile, serverReady])
}

type SubtitleChoicePersistOpts = {
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly subRestored: boolean
  readonly subActive: string | null
  readonly embeddedSub: number | null
  readonly sidecarIdx: number | null
  readonly subOffset: number
  readonly setSubActive: Dispatch<SetStateAction<string | null>>
  readonly setEmbeddedSub: Dispatch<SetStateAction<number | null>>
  readonly setSidecarIdx: Dispatch<SetStateAction<number | null>>
  readonly setSubOffset: Dispatch<SetStateAction<number>>
  readonly setAutoSource: Dispatch<SetStateAction<'hash' | 'title' | 'embedded' | null>>
  readonly setSubRestored: Dispatch<SetStateAction<boolean>>
}

// useSubtitleChoicePersist restores the saved subtitle choice for the current
// file (before the pt auto-load can fire), then persists changes back to
// localStorage. Extracted verbatim from PlayerModal — same deps & gating.
export function useSubtitleChoicePersist(opts: SubtitleChoicePersistOpts) {
  const { info, selectedFile, subRestored, subActive, embeddedSub, sidecarIdx, subOffset,
    setSubActive, setEmbeddedSub, setSidecarIdx, setSubOffset, setAutoSource, setSubRestored } = opts

  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0 || subRestored) return
    const saved = load<SubChoiceLite | null>(`sub.${info.infoHash}.${selectedFile}`, null)
    if (saved) {
      setSubActive(saved.external)
      setEmbeddedSub(saved.embedded)
      setSidecarIdx(saved.sidecar)
      setSubOffset(saved.offset || 0)
      if (saved.external !== null || saved.embedded !== null || saved.sidecar !== null) {
        setAutoSource(null)
      }
    }
    setSubRestored(true)
  }, [info?.infoHash, selectedFile, subRestored])

  useEffect(() => {
    if (!subRestored || !info?.infoHash || selectedFile < 0) return
    save<SubChoiceLite>(`sub.${info.infoHash}.${selectedFile}`, {
      external: subActive,
      embedded: embeddedSub,
      sidecar: sidecarIdx,
      offset: subOffset,
    })
  }, [subActive, embeddedSub, sidecarIdx, subOffset, subRestored, info?.infoHash, selectedFile])
}

type HevcBackstopOpts = {
  readonly videoRef: RefObject<HTMLVideoElement>
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly audioMode: boolean
  readonly transcodeAudio: number | null
  readonly forceH264: boolean
  readonly burnSubTrack: number | null
  readonly transcodeFallbackAttempted: boolean
  readonly videoError: boolean
  readonly bufferedEnd: number
  // From the ffprobe (#16): true=codec needs transcode, false=browser-safe,
  // undefined=probe ainda não chegou. Trava o backstop quando já se sabe que o
  // codec é browser-safe — aí um stall é rede/moov, não rejeição de codec.
  readonly needsTranscode?: boolean
  readonly caps: TranscodeCapabilities | null
  readonly videoDiagnostic: () => Record<string, unknown> | { reason: string }
  readonly setTranscodeFallbackAttempted: Dispatch<SetStateAction<boolean>>
  readonly setForceH264: Dispatch<SetStateAction<boolean>>
}

// backstopStuck: depois de 20s, readyState < 2 (nada tocável) + currentTime < 0.1
// (não andou um frame) + buffered ~0. Cada condição sozinha é benigna durante
// buffering normal; as três juntas por 20s cheiram a problema.
export function backstopStuck(readyState: number, currentTime: number, bufferedEnd: number): boolean {
  return readyState < 2 && currentTime < 0.1 && bufferedEnd < 0.5
}

// startGapNudgeTarget: o Safari (HLS nativo, caminho EVENT/live dos arquivos
// locais transcodados) às vezes bufferiza o primeiro segmento começando um fio
// DEPOIS de 0 (resíduo sub-tick de PTS que o muxer MPEG-TS deixa mesmo com
// -muxdelay 0; observado: buffered.start = 0.000002). O currentTime fica
// EXATAMENTE em 0 — logo ANTES de buffered.start(0) — então o Safari nunca
// chega a readyState 3, o `canplay` não dispara, o autoplay não roda e o vídeo
// trava no t=0 com segundos já bufferizados (sintoma: "pulou pro live mas não
// tocou"). Detecta exatamente essa forma (parado em ~0, com o 1º range
// começando em (currentTime, gapMax]) e devolve o alvo do nudge; o caller
// avança o currentTime pra dentro do buffer. gapMax (1,5s) cobre desde o
// resíduo de µs até o histórico initial_offset de 1,4s do muxer.
export function startGapNudgeTarget(currentTime: number, bufferedStart: number | null): number | null {
  if (currentTime > 0.25) return null        // já andou / passou do buraco inicial
  if (bufferedStart === null) return null
  const gap = bufferedStart - currentTime
  if (gap <= 0 || gap > 1.5) return null      // sem buraco, ou grande demais p/ ser o resíduo de PTS
  return bufferedStart + 0.05                 // pousa 50ms dentro do range bufferizado
}

// backstopShouldFire decide se o backstop deve FORÇAR transcode (h264) num stall.
// Regra (#16): se o probe já confirmou codec browser-safe (needsTranscode===false),
// o stall é de rede/moov — transcodar H264→H264 da mesma fonte fria não ajuda →
// NÃO dispara. Se o codec precisa de transcode (true) ou é desconhecido
// (undefined, probe ainda não chegou), dispara — desde que haja encoder de GPU.
export function backstopShouldFire(stuck: boolean, needsTranscode: boolean | undefined, hasEncoder: boolean): boolean {
  if (!stuck) return false
  if (needsTranscode === false) return false
  return hasEncoder
}

// hlsFatalAction decide, sem tocar no objeto Hls, qual recuperação aplicar a um
// erro FATAL do hls.js: NETWORK_ERROR → recarrega (startLoad), MEDIA_ERROR →
// recoverMediaError, qualquer outro → destrói. `types` é o enum Hls.ErrorTypes
// (passado pra evitar acoplar este módulo puro/testável ao import de hls.js).
export function hlsFatalAction(
  type: string,
  types: { NETWORK_ERROR: string; MEDIA_ERROR: string },
): 'startLoad' | 'recoverMedia' | 'destroy' {
  if (type === types.NETWORK_ERROR) return 'startLoad'
  if (type === types.MEDIA_ERROR) return 'recoverMedia'
  return 'destroy'
}

// useHevcBackstop is the Safari silent-HEVC-failure backstop: after 20s with no
// playable frame it fires the same fallback <video onError> would. Extracted
// verbatim from PlayerModal — same 20s window, same gating, same deps.
export function useHevcBackstop(opts: HevcBackstopOpts) {
  const { videoRef, info, selectedFile, audioMode, transcodeAudio, forceH264, burnSubTrack,
    transcodeFallbackAttempted, videoError, bufferedEnd, needsTranscode, caps, videoDiagnostic,
    setTranscodeFallbackAttempted, setForceH264 } = opts
  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0) return
    const transcodingActive = transcodeAudio !== null || forceH264 || burnSubTrack !== null
    // Audio files don't need H264 transcoding — skip backstop entirely.
    if (audioMode || transcodingActive || transcodeFallbackAttempted || videoError) return
    const timer = globalThis.setTimeout(() => {
      const v = videoRef.current
      if (!v) return
      const stuck = backstopStuck(v.readyState, v.currentTime, bufferedEnd)
      const hasEncoder = !!(caps && (caps.hasNvidia || caps.hasVaapi || caps.hasQsv))
      clientLog('info', 'player', '20s backstop tick', { stuck, readyState: v.readyState, currentTime: v.currentTime, bufferedEnd, needsTranscode, src: v.currentSrc })
      if (stuck) {
        // O probe (#16) já confirmou codec browser-safe (H264/AAC/MP4)? Então
        // este stall (readyState 0, buffered ~0) é problema de rede/moov — ex:
        // moov do MP4 ainda não baixou —, NÃO a falha silenciosa de HEVC do
        // Safari. Transcodar H264→H264 lendo a MESMA fonte fria não acelera
        // nada e só adiciona latência. Não dispara o fallback.
        if (needsTranscode === false) {
          clientLog('info', 'player', 'backstop skipped — codec browser-safe (probe); stall é rede/moov, não codec', { needsTranscode, readyState: v.readyState, bufferedEnd })
          return
        }
        if (backstopShouldFire(stuck, needsTranscode, hasEncoder)) {
          clientLog('warn', 'player', 'backstop firing fallback — Safari silent HEVC path likely', videoDiagnostic())
          setTranscodeFallbackAttempted(true)
          setForceH264(true)
        } else {
          clientLog('warn', 'player', 'backstop wanted to fallback but no GPU encoder available', { caps })
        }
      }
    }, 20000)
    return () => globalThis.clearTimeout(timer)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash, selectedFile, transcodeAudio, forceH264, burnSubTrack, transcodeFallbackAttempted, videoError, needsTranscode, caps])
}
