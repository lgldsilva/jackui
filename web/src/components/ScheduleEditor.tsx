import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import axios from 'axios'
import { Loader2, Wand2 } from 'lucide-react'
import { SchedKind, watchlistsParseSchedule } from '../api/client'

// ScheduleValue is the schedule slice of a watchlist draft — the same sched*
// field names the API uses, so it spreads straight into a WatchlistInput.
export type ScheduleValue = {
  schedKind: SchedKind
  schedMinutes: number
  schedWeekday: number
  schedHour: number
  schedMinute: number
}

export const DEFAULT_SCHEDULE: ScheduleValue = {
  schedKind: 'interval', schedMinutes: 15, schedWeekday: 0, schedHour: 8, schedMinute: 0,
}

const pad2 = (n: number) => String(n).padStart(2, '0')

// Translate function shape (kept local so we don't depend on i18next types).
type Tr = (key: string, opts?: Record<string, unknown>) => string

// schedSummary renders a schedule as a short human-readable phrase — shared by
// the watchlist cards AND the AI confirmation line in the ScheduleEditor.
export function schedSummary(t: Tr, w: ScheduleValue): string {
  const time = `${pad2(w.schedHour)}:${pad2(w.schedMinute)}`
  if (w.schedKind === 'daily') return t('watchlist.summary_daily', { time })
  if (w.schedKind === 'weekly') {
    return t('watchlist.summary_weekly', { weekday: t(`watchlist.weekdays.${w.schedWeekday}`), time })
  }
  if (w.schedMinutes > 0) return t('watchlist.summary_interval', { minutes: w.schedMinutes })
  return t('watchlist.server_default')
}

// aiParseUnavailable flips after the first 503 (AI disabled on the server) so
// the free-text field hides for the rest of the session instead of failing again.
let aiParseUnavailable = false

// ScheduleEditor — picks how often the server re-checks this watchlist:
// fixed interval (every N minutes), daily at HH:MM or weekly on a weekday.
// The optional free-text field below asks the server's AI to interpret a phrase
// like "toda segunda às 9h"; on success it fills the selects and shows the
// summary as confirmation — the user still saves manually.
export default function ScheduleEditor({ value, onChange }: Readonly<{ value: ScheduleValue; onChange: (v: ScheduleValue) => void }>) {
  const { t } = useTranslation()
  const [aiText, setAiText] = useState('')
  const [aiBusy, setAiBusy] = useState(false)
  const [aiError, setAiError] = useState('')
  const [aiApplied, setAiApplied] = useState(false)
  const [aiAvailable, setAiAvailable] = useState(!aiParseUnavailable)
  const timeValue = `${pad2(value.schedHour)}:${pad2(value.schedMinute)}`
  const setTime = (s: string) => {
    const [h, m] = s.split(':').map(part => Number.parseInt(part, 10))
    if (!Number.isNaN(h) && !Number.isNaN(m)) onChange({ ...value, schedHour: h, schedMinute: m })
  }
  const interpret = async () => {
    const text = aiText.trim()
    if (!text || aiBusy) return
    setAiBusy(true)
    setAiError('')
    setAiApplied(false)
    try {
      const parsed = await watchlistsParseSchedule(text)
      onChange({ ...value, ...parsed })
      setAiApplied(true)
    } catch (err) {
      const status = axios.isAxiosError(err) ? err.response?.status : undefined
      const code = axios.isAxiosError(err) ? (err.response?.data as { code?: string } | undefined)?.code : undefined
      if (status === 503 && code === 'ai_disabled') {
        // IA não configurada no servidor — esconde o recurso pela sessão.
        // Falha transitória da chain (ai_transient) mantém o campo visível.
        aiParseUnavailable = true
        setAiAvailable(false)
      } else if (status === 422) {
        setAiError(t('watchlist.ai_unclear'))
      } else {
        setAiError(t('watchlist.ai_error'))
      }
    } finally {
      setAiBusy(false)
    }
  }
  return (
    <div className="flex flex-col gap-2">
    <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
      <label className="flex flex-col gap-1 text-xs text-text-muted">
        {t('watchlist.sched_label')}
        <select
          className="input-field text-base sm:text-sm"
          value={value.schedKind}
          onChange={e => onChange({ ...value, schedKind: e.target.value as SchedKind })}
        >
          <option value="interval">{t('watchlist.kind_interval')}</option>
          <option value="daily">{t('watchlist.kind_daily')}</option>
          <option value="weekly">{t('watchlist.kind_weekly')}</option>
        </select>
      </label>
      {value.schedKind === 'interval' && (
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          {t('watchlist.every_minutes')}
          <input
            type="number" min={1} className="input-field text-base sm:text-sm"
            value={value.schedMinutes}
            onChange={e => onChange({ ...value, schedMinutes: Number.parseInt(e.target.value || '0', 10) })}
          />
        </label>
      )}
      {value.schedKind === 'weekly' && (
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          {t('watchlist.weekday_label')}
          <select
            className="input-field text-base sm:text-sm"
            value={value.schedWeekday}
            onChange={e => onChange({ ...value, schedWeekday: Number.parseInt(e.target.value, 10) })}
          >
            {[0, 1, 2, 3, 4, 5, 6].map(d => (
              <option key={d} value={d}>{t(`watchlist.weekdays.${d}`)}</option>
            ))}
          </select>
        </label>
      )}
      {(value.schedKind === 'daily' || value.schedKind === 'weekly') && (
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          {t('watchlist.time_label')}
          <input
            type="time" className="input-field text-base sm:text-sm"
            value={timeValue} onChange={e => setTime(e.target.value)}
          />
        </label>
      )}
    </div>
    {aiAvailable && (
      <div className="flex flex-col gap-1">
        <div className="flex gap-2">
          <input
            className="input-field text-base sm:text-sm flex-1"
            placeholder={t('watchlist.ai_placeholder')}
            value={aiText}
            onChange={e => { setAiText(e.target.value); setAiError(''); setAiApplied(false) }}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); interpret() } }}
          />
          <button
            type="button"
            onClick={interpret}
            disabled={aiBusy || !aiText.trim()}
            className="btn-secondary flex items-center gap-1.5 text-xs disabled:opacity-50 disabled:cursor-not-allowed"
            title={t('watchlist.ai_button')}
          >
            {aiBusy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Wand2 className="w-4 h-4 text-cyan-400" />}
            <span className="hidden sm:inline">{t('watchlist.ai_button')}</span>
          </button>
        </div>
        {aiError && <p className="text-xs text-red-400">{aiError}</p>}
        {aiApplied && !aiError && (
          <p className="text-xs text-emerald-400">{t('watchlist.ai_applied', { summary: schedSummary(t, value) })}</p>
        )}
      </div>
    )}
    </div>
  )
}
