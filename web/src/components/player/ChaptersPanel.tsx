import { ListVideo, ChevronsLeft, ChevronsRight } from 'lucide-react'
import { MediaChapter } from '../../api/client'

type ChaptersPanelProps = {
  readonly chapters: MediaChapter[]
  readonly currentTime: number
  readonly onSeek: (sec: number) => void
  readonly formatTime: (s: number) => string
}

// activeChapterIndex returns the index of the chapter currently playing (the
// last one whose start is at or before currentTime), or -1 before the first.
export function activeChapterIndex(chapters: MediaChapter[], currentTime: number): number {
  let active = -1
  for (let i = 0; i < chapters.length; i++) {
    if (currentTime >= chapters[i].startSec) active = i
    else break
  }
  return active
}

// chapterSeekTargets resolves where the prev/next chapter buttons land from
// the current position. "Prev" follows the convention every player uses: more
// than `backThreshold` seconds into a chapter goes back to ITS start; right at
// the start goes to the previous chapter. null = no target (edge reached).
export function chapterSeekTargets(
  chapters: MediaChapter[],
  currentTime: number,
  backThreshold = 3,
): { prevSec: number | null; nextSec: number | null } {
  if (!chapters.length) return { prevSec: null, nextSec: null }
  const active = activeChapterIndex(chapters, currentTime)
  const nextSec = active < chapters.length - 1 ? chapters[active + 1].startSec : null
  let prevSec: number | null = null
  if (active >= 0) {
    const intoChapter = currentTime - chapters[active].startSec
    if (intoChapter > backThreshold) prevSec = chapters[active].startSec
    else if (active > 0) prevSec = chapters[active - 1].startSec
    else prevSec = chapters[0].startSec === 0 && currentTime <= backThreshold ? null : chapters[0].startSec
  }
  return { prevSec, nextSec }
}

// ChapterNavButtons — compact prev/next chapter controls for the transport
// row. Rendered only when the probe found more than one chapter.
export function ChapterNavButtons({ chapters, currentTime, onSeek }: {
  readonly chapters: MediaChapter[]
  readonly currentTime: number
  readonly onSeek: (sec: number) => void
}) {
  const { prevSec, nextSec } = chapterSeekTargets(chapters, currentTime)
  const btn = 'flex items-center text-sm sm:text-xs bg-surface-tertiary hover:bg-surface-secondary text-text-secondary hover:text-text-primary border border-default px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors disabled:opacity-30 flex-shrink-0'
  return (
    <>
      <button
        onClick={() => { if (prevSec !== null) onSeek(prevSec) }}
        disabled={prevSec === null}
        title="Capítulo anterior"
        className={btn}
      >
        <ChevronsLeft className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
      </button>
      <button
        onClick={() => { if (nextSec !== null) onSeek(nextSec) }}
        disabled={nextSec === null}
        title="Próximo capítulo"
        className={btn}
      >
        <ChevronsRight className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
      </button>
    </>
  )
}

// ChaptersPanel lists embedded chapter markers and seeks the <video> on click.
// It navigates by time (video.currentTime) from the probe list rather than a
// <track kind="chapters">, because the HLS transcode strips embedded chapters —
// the same list then works for both direct-play and HLS.
export function ChaptersPanel({ chapters, currentTime, onSeek, formatTime }: ChaptersPanelProps) {
  const activeIdx = activeChapterIndex(chapters, currentTime)
  return (
    <div className="px-3 sm:px-4 py-3 border-b border-default flex flex-col gap-1.5">
      <p className="text-xs text-text-muted mb-0.5 flex items-center gap-2">
        <ListVideo className="w-3 h-3" />
        Capítulos ({chapters.length})
      </p>
      <div className="flex flex-col gap-1 max-h-48 overflow-y-auto">
        {chapters.map((ch, i) => (
          <button
            key={ch.index}
            onClick={() => onSeek(ch.startSec)}
            title={`Ir para ${formatTime(ch.startSec)}`}
            className={`flex items-center gap-2 text-left text-[11px] px-2 py-1 rounded border transition-colors ${
              i === activeIdx
                ? 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/30'
                : 'bg-surface-secondary text-text-secondary border-default hover:text-text-primary'
            }`}
          >
            <span className="tabular-nums text-text-muted flex-shrink-0">{formatTime(ch.startSec)}</span>
            <span className="min-w-0 truncate">{ch.title || `Capítulo ${i + 1}`}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
