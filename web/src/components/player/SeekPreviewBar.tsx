import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { streamThumbnailURL } from '../../api/stream-urls'
import { isLocalHash } from '../../api/local'

type SeekPreviewBarProps = {
  readonly duration: number
  readonly currentTime: number
  readonly bufferedRanges: Array<[number, number]>
  readonly torrentProgress: number
  readonly infoHash?: string
  readonly fileIndex: number
  readonly formatTime: (s: number) => string
  readonly onSeek: (sec: number) => void
}

function ratioFromEvent(e: React.MouseEvent<HTMLDivElement> | React.PointerEvent<HTMLDivElement>): number {
  const rect = e.currentTarget.getBoundingClientRect()
  if (rect.width <= 0) return 0
  return Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width))
}

// Clickable buffer/progress strip with a hover thumbnail (torrent files only —
// local thumbs already have their own endpoint and are not 10s-quantized).
export function SeekPreviewBar({
  duration, currentTime, bufferedRanges, torrentProgress,
  infoHash, fileIndex, formatTime, onSeek,
}: SeekPreviewBarProps) {
  const { t } = useTranslation()
  const [hover, setHover] = useState<{ ratio: number; x: number } | null>(null)
  const canThumb = !!infoHash && !isLocalHash(infoHash) && duration > 0 && fileIndex >= 0
  const hoverSec = hover && duration > 0 ? hover.ratio * duration : 0

  return (
    <div className="relative">
      {hover && duration > 0 && (
        <div
          className="absolute bottom-3 z-10 pointer-events-none -translate-x-1/2 flex flex-col items-center"
          style={{ left: `${hover.x}px` }}
        >
          {canThumb && (
            <img
              src={streamThumbnailURL(infoHash, fileIndex, hoverSec)}
              alt=""
              className="w-32 h-20 object-cover rounded shadow-lg border border-black/40 bg-black"
            />
          )}
          <span className="mt-1 text-[10px] px-1.5 py-0.5 rounded bg-black/80 text-white tabular-nums">
            {formatTime(hoverSec)}
          </span>
        </div>
      )}
      <div
        role="slider"
        tabIndex={0}
        aria-label={t('player.controls.seekBar')}
        aria-valuemin={0}
        aria-valuemax={Math.round(duration)}
        aria-valuenow={Math.round(currentTime)}
        className="relative bg-surface-tertiary rounded-full h-2 cursor-pointer"
        onClick={e => { if (duration > 0) onSeek(ratioFromEvent(e) * duration) }}
        onMouseMove={e => setHover({ ratio: ratioFromEvent(e), x: e.clientX - e.currentTarget.getBoundingClientRect().left })}
        onMouseLeave={() => setHover(null)}
      >
        <div
          className="absolute inset-y-0 left-0 bg-gray-500 rounded-full"
          style={{ width: `${torrentProgress * 100}%` }}
        />
        {duration > 0 && bufferedRanges.map(([start, end]) => (
          <div
            key={start}
            className="absolute inset-y-0 bg-blue-500/50 rounded-full"
            style={{
              left: `${(start / duration) * 100}%`,
              width: `${(Math.max(0, end - start) / duration) * 100}%`,
            }}
          />
        ))}
        {duration > 0 && (
          <div
            className="absolute inset-y-0 left-0 bg-green-500 rounded-full"
            style={{ width: `${(currentTime / duration) * 100}%` }}
          />
        )}
      </div>
    </div>
  )
}
