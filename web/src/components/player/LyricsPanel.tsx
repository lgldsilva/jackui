import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { lyricsGet, type Lyrics } from '../../api/client'
import { parseLrc, activeLineIndex } from '../../lib/lrc'

// useLyrics fetches lyrics (backend LrcLib proxy) for a track. Lifted out of the
// panel so the PARENT (MusicPanel) can know whether lyrics exist BEFORE rendering
// the section: on iOS the equalizer is hidden, so the "Letras" toggle should only
// appear when there actually are lyrics — never a control that does nothing.
export function useLyrics(title: string, artist: string, album: string, durationSec: number): { lyrics: Lyrics | null; loading: boolean } {
  const [lyrics, setLyrics] = useState<Lyrics | null>(null)
  const [loading, setLoading] = useState(false)
  // durationSec is only an optional exact-match hint AND it fluctuates as the
  // stream loads. Keep it OUT of the fetch deps (read via ref) — otherwise the
  // effect re-fired on every duration tick and the panel stuck on "Fetching…".
  const durationRef = useRef(durationSec)
  durationRef.current = durationSec

  useEffect(() => {
    if (!title) { setLyrics(null); setLoading(false); return }
    let cancelled = false
    setLoading(true)
    // Hard ceiling: the backend LrcLib proxy can take up to ~16s when the server
    // can't reach lrclib.net. Stop showing "Fetching…" after 10s regardless — the
    // result still applies if it arrives later. Guards against an infinite spinner.
    const timer = setTimeout(() => { if (!cancelled) setLoading(false) }, 10000)
    lyricsGet(title, artist, album, durationRef.current)
      .then((l) => { if (!cancelled) setLyrics(l) })
      .catch(() => { if (!cancelled) setLyrics(null) })
      .finally(() => { if (!cancelled) { clearTimeout(timer); setLoading(false) } })
    return () => { cancelled = true; clearTimeout(timer) }
  }, [title, artist, album])

  return { lyrics, loading }
}

// hasLyricsContent: true when the fetched lyrics carry synced OR plain text.
export function hasLyricsContent(l: Lyrics | null): boolean {
  return !!(l?.synced || l?.plain)
}

type LyricsPanelProps = {
  // Lyrics + loading come from the parent's useLyrics (single fetch shared with
  // the visibility decision); currentTime drives the synced active-line highlight.
  readonly lyrics: Lyrics | null
  readonly loading: boolean
  readonly currentTime: number
}

// LyricsPanel renders lyrics for the current track and, when synced (LRC),
// highlights the active line and auto-scrolls to it. Falls back to plain text,
// then to an empty state. Presentational — the fetch lives in useLyrics.
export function LyricsPanel({ lyrics, loading, currentTime }: LyricsPanelProps) {
  const { t } = useTranslation()
  const activeRef = useRef<HTMLParagraphElement>(null)

  const lines = useMemo(() => parseLrc(lyrics?.synced ?? ''), [lyrics?.synced])
  const active = activeLineIndex(lines, currentTime)

  useEffect(() => {
    activeRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }, [active])

  return (
    <section className="rounded-lg bg-surface-2 p-3 text-center" aria-label={t('player.lyrics.title')}>
      <span className="mb-2 block text-sm font-medium text-text">{t('player.lyrics.title')}</span>
      <div className="max-h-48 overflow-y-auto text-sm" aria-live="polite">
        {renderBody({ loading, lyrics, lines, active, activeRef, t })}
      </div>
    </section>
  )
}

// renderBody keeps LyricsPanel's JSX flat (one decision tree here) so the
// component stays under the cognitive-complexity gate.
function renderBody({ loading, lyrics, lines, active, activeRef, t }: {
  loading: boolean
  lyrics: Lyrics | null
  lines: ReturnType<typeof parseLrc>
  active: number
  activeRef: React.RefObject<HTMLParagraphElement>
  t: (k: string) => string
}) {
  if (loading) return <p className="text-text-muted">{t('player.lyrics.loading')}</p>
  if (lines.length > 0) {
    return lines.map((ln, i) => (
      <p
        key={`${ln.time}-${i}`}
        ref={i === active ? activeRef : undefined}
        className={i === active ? 'py-0.5 font-semibold text-primary' : 'py-0.5 text-text-muted'}
      >
        {ln.text || ' '}
      </p>
    ))
  }
  if (lyrics?.plain) return <pre className="whitespace-pre-wrap font-sans text-text-muted">{lyrics.plain}</pre>
  return <p className="text-text-muted">{t('player.lyrics.none')}</p>
}
