import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { PictureInPicture2, SkipForward } from 'lucide-react'
import type { MediaChapter } from '../../api/stream-types'
import { findIntroSkip, shouldShowSkipIntro } from '../../lib/skipIntro'
import { shouldShowUpNext, upNextCountdown } from '../../lib/upNext'

type ExperienceProps = {
  readonly videoRef: React.RefObject<HTMLVideoElement | null>
  readonly chapters?: readonly MediaChapter[]
  readonly currentTime: number
  readonly duration: number
  readonly hasNext: boolean
  readonly nextLabel?: string
  readonly audioMode: boolean
  readonly videoError: boolean
  readonly showResumePrompt: boolean
  readonly onNext?: () => void
}

// Overlays that sit ON the video: skip-intro, Up Next, native PiP.
// Kept out of VideoPlayerElement so that file stays under the complexity gate.
export function PlayerExperienceOverlays({
  videoRef, chapters, currentTime, duration, hasNext, nextLabel,
  audioMode, videoError, showResumePrompt, onNext,
}: ExperienceProps) {
  const { t } = useTranslation()
  const intro = useMemo(() => findIntroSkip(chapters), [chapters])
  const showSkip = !audioMode && !videoError && !showResumePrompt && shouldShowSkipIntro(intro, currentTime)
  const [upNextDismissed, setUpNextDismissed] = useState(false)
  useEffect(() => { setUpNextDismissed(false) }, [nextLabel, hasNext])
  const showUpNext = shouldShowUpNext({
    currentTime, duration, hasNext, dismissed: upNextDismissed, audioMode,
  }) && !videoError && !showResumePrompt

  const skipIntro = () => {
    const v = videoRef.current
    if (!v || !intro) return
    v.currentTime = intro.endSec
  }

  const requestPip = () => {
    const v = videoRef.current
    if (!v) return
    const webkit = v as HTMLVideoElement & { webkitSetPresentationMode?: (m: string) => void }
    if (typeof v.requestPictureInPicture === 'function') {
      v.requestPictureInPicture().catch(() => {})
      return
    }
    webkit.webkitSetPresentationMode?.('picture-in-picture')
  }

  if (audioMode || videoError) return null

  return (
    <>
      {showSkip && intro && (
        <button
          type="button"
          onClick={skipIntro}
          className="absolute bottom-14 right-3 z-20 flex items-center gap-1.5 px-3 py-2 rounded-lg bg-black/75 text-white text-sm hover:bg-black/90 backdrop-blur-sm"
        >
          <SkipForward className="w-4 h-4" />
          {t('player.overlays.skipIntro')}
        </button>
      )}
      {showUpNext && (
        <div className="absolute bottom-14 left-3 z-20 max-w-[min(100%-1.5rem,20rem)] rounded-xl bg-black/80 text-white p-3 backdrop-blur-sm border border-white/10">
          <p className="text-[11px] uppercase tracking-wide text-white/60">{t('player.overlays.upNextIn', { n: upNextCountdown(currentTime, duration) })}</p>
          {nextLabel && <p className="text-sm font-medium mt-0.5 line-clamp-2">{nextLabel}</p>}
          <div className="flex gap-2 mt-2">
            <button type="button" onClick={() => onNext?.()} className="btn-primary !py-1 !px-3 text-xs">
              {t('player.overlays.playNow')}
            </button>
            <button type="button" onClick={() => setUpNextDismissed(true)} className="btn-secondary !py-1 !px-3 text-xs">
              {t('player.overlays.dismissUpNext')}
            </button>
          </div>
        </div>
      )}
      {documentPictureInPictureAvailable() && (
        <button
          type="button"
          onClick={requestPip}
          title={t('player.overlays.pip')}
          aria-label={t('player.overlays.pip')}
          className="absolute top-2 right-12 z-20 p-2 rounded-md bg-black/55 text-white hover:bg-black/75 backdrop-blur-sm"
        >
          <PictureInPicture2 className="w-4 h-4" />
        </button>
      )}
    </>
  )
}

function documentPictureInPictureAvailable(): boolean {
  if (globalThis.document === undefined) return false
  const proto = globalThis.HTMLVideoElement?.prototype as (HTMLVideoElement & { webkitSetPresentationMode?: unknown }) | undefined
  return typeof document.pictureInPictureEnabled === 'boolean'
    ? document.pictureInPictureEnabled
    : typeof proto?.webkitSetPresentationMode === 'function'
}
