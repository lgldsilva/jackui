import { useEffect, type RefObject } from 'react'
import { load, save } from '../../lib/storage'

// Mute/volume are per-USER preferences, not per-media: the <video> is recreated
// on every file/mode switch (audioElementKey drives its `key`), so a brand-new
// element used to come back unmuted at volume 1 and the user had to mute again
// on every play. Persisted under the shared "jackui:" namespace, same as the
// playback speed.
export const MUTED_KEY = 'player.muted'
export const VOLUME_KEY = 'player.volume'

// clampVolume keeps a stored value inside the HTMLMediaElement contract [0,1] —
// a corrupted/hand-edited entry must not throw when assigned.
export function clampVolume(v: unknown): number {
  // Only a real number counts: coercing would turn null/'' into 0, i.e. silence
  // from a corrupted entry — the default has to be full volume, not mute.
  if (typeof v !== 'number' || !Number.isFinite(v)) return 1
  return Math.min(1, Math.max(0, v))
}

export function readPersistedAudio(): { muted: boolean; volume: number } {
  return {
    muted: load<boolean>(MUTED_KEY, false) === true,
    volume: clampVolume(load<number>(VOLUME_KEY, 1)),
  }
}

export function writePersistedAudio(muted: boolean, volume: number): void {
  save(MUTED_KEY, muted)
  save(VOLUME_KEY, clampVolume(volume))
}

// usePersistedVolume restores mute+volume onto the current media element and
// records every user change. Listening to `volumechange` covers BOTH input
// paths: the native controls (the <video> renders `controls`) and the M/arrow
// keyboard shortcuts, which mutate the element directly (playerHooks.ts).
//
// The ref is an HTMLMediaElement so the same hook serves the <video> and the
// gapless engine's <audio>.
export function usePersistedVolume({ mediaRef, forceMuted = false, elementKey }: {
  readonly mediaRef: RefObject<HTMLMediaElement | null>
  /**
   * The gapless engine plays audio through its own <audio>, so the <video> MUST
   * stay silent while it's active. That silence is engine-driven, never a user
   * preference — so it's applied but not recorded.
   */
  readonly forceMuted?: boolean
  /** Changes whenever the element is remounted, re-running the restore. */
  readonly elementKey?: string
}): void {
  useEffect(() => {
    const el = mediaRef.current
    if (!el) return

    const apply = () => {
      const { muted, volume } = readPersistedAudio()
      el.volume = volume
      el.muted = forceMuted || muted
    }
    apply()

    const onVolumeChange = () => {
      if (forceMuted) return
      writePersistedAudio(el.muted, el.volume)
    }
    // `loadstart` re-applies when only the src swaps (no remount, so no new key).
    el.addEventListener('volumechange', onVolumeChange)
    el.addEventListener('loadstart', apply)
    return () => {
      el.removeEventListener('volumechange', onVolumeChange)
      el.removeEventListener('loadstart', apply)
    }
  }, [mediaRef, forceMuted, elementKey])
}
