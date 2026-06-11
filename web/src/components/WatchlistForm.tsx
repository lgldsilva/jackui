import { useState } from 'react'
import { Save, DownloadCloud } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Watchlist, WatchlistInput } from '../api/client'
import ScheduleEditor, { DEFAULT_SCHEDULE, ScheduleValue } from './ScheduleEditor'

const GiB = 1024 * 1024 * 1024

export const RESOLUTIONS = ['', '480p', '720p', '1080p', '2160p'] as const
export const CODECS = ['', 'x264', 'x265', 'av1'] as const

type Props = {
  initial?: Watchlist
  onSave: (input: WatchlistInput) => void | Promise<void>
  onCancel: () => void
}

// Create/edit form for a watchlist, including the auto-download quality
// filters. Controlled locally; parent only receives the final payload.
export default function WatchlistForm({ initial, onSave, onCancel }: Readonly<Props>) {
  const { t } = useTranslation()
  const [query, setQuery] = useState(initial?.query ?? '')
  const [category, setCategory] = useState(initial?.category ?? '')
  const [minSeeders, setMinSeeders] = useState(initial?.minSeeders ?? 1)
  const [ntfyTopic, setNtfyTopic] = useState(initial?.ntfyTopic ?? '')
  const [autoDownload, setAutoDownload] = useState(initial?.autoDownload ?? false)
  const [minResolution, setMinResolution] = useState(initial?.minResolution ?? '')
  const [codec, setCodec] = useState(initial?.codec ?? '')
  const [maxSizeGB, setMaxSizeGB] = useState(
    initial?.maxSizeBytes ? String(Math.round((initial.maxSizeBytes / GiB) * 10) / 10) : '',
  )
  const [sched, setSched] = useState<ScheduleValue>(initial ? {
    schedKind: initial.schedKind || 'interval',
    schedMinutes: initial.schedMinutes > 0 ? initial.schedMinutes : 15,
    schedWeekday: initial.schedWeekday || 0,
    schedHour: initial.schedHour || 0,
    schedMinute: initial.schedMinute || 0,
  } : DEFAULT_SCHEDULE)

  const save = () => {
    if (!query.trim()) return
    const gb = Number.parseFloat(maxSizeGB)
    onSave({
      query: query.trim(),
      category,
      minSeeders,
      ntfyTopic: ntfyTopic.trim(),
      ...sched,
      autoDownload,
      minResolution,
      codec,
      maxSizeBytes: Number.isFinite(gb) && gb > 0 ? Math.round(gb * GiB) : 0,
    })
  }

  return (
    <div className="flex flex-col gap-2">
      <input
        className="input-field text-base sm:text-sm" placeholder={t('watchlist.query_placeholder')}
        value={query} onChange={e => setQuery(e.target.value)} autoFocus={!initial}
      />
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
        <input
          className="input-field text-base sm:text-sm" placeholder={t('watchlist.category_placeholder')}
          value={category} onChange={e => setCategory(e.target.value)}
        />
        <input
          type="number" min={0} className="input-field text-base sm:text-sm" placeholder={t('watchlist.min_seeders')}
          value={minSeeders} onChange={e => setMinSeeders(Number.parseInt(e.target.value || '0', 10))}
        />
      </div>
      <input
        className="input-field text-base sm:text-sm" placeholder={t('watchlist.ntfy_placeholder')}
        value={ntfyTopic} onChange={e => setNtfyTopic(e.target.value)}
      />
      <ScheduleEditor value={sched} onChange={setSched} />

      <label className="flex items-center gap-2 text-sm text-text-primary cursor-pointer select-none mt-1">
        <input
          type="checkbox" className="accent-amber-500 w-4 h-4"
          checked={autoDownload} onChange={e => setAutoDownload(e.target.checked)}
        />
        <DownloadCloud className="w-4 h-4 text-amber-400" />
        {t('watchlist.auto_download')}
      </label>
      {autoDownload && (
        <>
          <p className="text-xs text-text-muted">{t('watchlist.auto_download_help')}</p>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
            <label className="flex flex-col gap-1 text-xs text-text-muted">
              {t('watchlist.min_resolution')}
              <select
                className="input-field text-base sm:text-sm" value={minResolution}
                onChange={e => setMinResolution(e.target.value)}
              >
                {RESOLUTIONS.map(r => (
                  <option key={r} value={r}>{r === '' ? t('watchlist.any') : r}</option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-text-muted">
              {t('watchlist.codec')}
              <select
                className="input-field text-base sm:text-sm" value={codec}
                onChange={e => setCodec(e.target.value)}
              >
                {CODECS.map(c => (
                  <option key={c} value={c}>{c === '' ? t('watchlist.any') : c}</option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-text-muted">
              {t('watchlist.max_size_gb')}
              <input
                type="number" min={0} step={0.5} className="input-field text-base sm:text-sm"
                placeholder={t('watchlist.unlimited')} value={maxSizeGB}
                onChange={e => setMaxSizeGB(e.target.value)}
              />
            </label>
          </div>
        </>
      )}

      <div className="flex gap-2">
        <button onClick={save} className="btn-primary flex items-center gap-1.5">
          <Save className="w-4 h-4" /> {t('watchlist.save')}
        </button>
        <button onClick={onCancel} className="btn-secondary">{t('watchlist.cancel')}</button>
      </div>
    </div>
  )
}
