import { useMemo, useState } from 'react'
import { Check, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Subtitle } from '../../api/client'

// Above this many results, show a filter box — OpenSubtitles often returns
// dozens of releases for a popular title and scrolling them is painful.
const FILTER_THRESHOLD = 6

type Props = {
  readonly subResults: Subtitle[]
  readonly subActive: string | number | null
  readonly pickSubtitle: (s: Subtitle) => void
}

// SubtitleResultsList renders the OpenSubtitles matches with a client-side
// filter (language / release name / uploader). Kept in its own file so the
// already-large PlayerControlsPanel doesn't grow further.
export function SubtitleResultsList({ subResults, subActive, pickSubtitle }: Props) {
  const { t } = useTranslation()
  const [filter, setFilter] = useState('')

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return subResults
    return subResults.filter(s =>
      `${s.language} ${s.release ?? ''} ${s.uploaderName ?? ''}`.toLowerCase().includes(q),
    )
  }, [subResults, filter])

  return (
    <div className="flex flex-col gap-1">
      {subResults.length >= FILTER_THRESHOLD && (
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-text-muted pointer-events-none" />
          <input
            type="text"
            value={filter}
            onChange={e => setFilter(e.target.value)}
            placeholder={t('player.subtitles.filter')}
            className="w-full bg-surface border border-default rounded-lg pl-7 pr-3 py-1.5 text-xs text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500"
          />
        </div>
      )}
      <div className="flex flex-col gap-1 max-h-[50vh] overflow-y-auto">
        {filtered.length === 0 && (
          <p className="text-xs text-text-muted py-2">{t('player.subtitles.noMatch')}</p>
        )}
        {filtered.map(s => (
          <button
            key={s.id}
            onClick={() => pickSubtitle(s)}
            className={`flex items-center justify-between gap-2 px-3 py-2 rounded-lg text-xs text-left transition-colors ${
              subActive === s.id
                ? 'bg-green-500/20 text-green-400 border border-green-500/30'
                : 'bg-surface/50 hover:bg-surface text-text-primary border border-transparent'
            }`}
          >
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="font-mono uppercase text-[10px] bg-surface-tertiary px-1.5 py-0.5 rounded">
                  {s.language}
                </span>
                <span className="truncate">{s.release || '(sem release name)'}</span>
                {s.trusted && <span className="text-green-400 text-[10px]">✓ trusted</span>}
                {s.hearingImpaired && <span className="text-yellow-400 text-[10px]">[HI]</span>}
              </div>
              <div className="text-[10px] text-text-muted mt-0.5">
                {s.uploaderName} • {s.downloads.toLocaleString()} downloads
              </div>
            </div>
            {subActive === s.id && <Check className="w-4 h-4 flex-shrink-0" />}
          </button>
        ))}
      </div>
    </div>
  )
}
