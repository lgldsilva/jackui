import { RefObject, useEffect } from 'react'
import { TorrentInfo } from '../../api/client'

// Hooks extracted verbatim from PlayerModal to shrink that 2000+ line component
// and make these self-contained side effects independently readable/testable.
// Behavior is unchanged — same effect bodies, same dependency arrays.

type KeyboardShortcutsOpts = {
  readonly videoRef: RefObject<HTMLVideoElement>
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
      const dur = isFinite(v.duration) ? v.duration : Infinity
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
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [minimized])
}

type MediaSessionOpts = {
  readonly videoRef: RefObject<HTMLVideoElement>
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
