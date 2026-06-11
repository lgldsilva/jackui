import { useEffect, useState } from 'react'
import { BarChart3, Clock, Download, Film, Loader2, Search, Bell } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import NavHeader from '../components/NavHeader'
import { UserStats, statsGet } from '../api/client'
import { formatBytes } from '../lib/format'
import { formatWatchTime, barHeights, monthLabel } from '../lib/stats'

// StatsPage shows personal usage aggregates (GET /api/stats): playback,
// downloads, searches, watchlists. Bars are plain CSS — no chart dependency.
export default function StatsPage() {
  const { t, i18n } = useTranslation()
  const [stats, setStats] = useState<UserStats | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    statsGet().then(setStats).finally(() => setLoading(false))
  }, [])

  if (loading) {
    return (
      <div className="min-h-screen bg-surface flex flex-col">
        <NavHeader />
        <div className="flex-1 flex items-center justify-center"><Loader2 className="w-8 h-8 animate-spin text-text-muted" /></div>
      </div>
    )
  }

  const lib = stats?.library
  const weekdayKeys = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat']

  return (
    <div className="min-h-screen bg-surface flex flex-col">
      <NavHeader />
      <main className="flex-1 max-w-7xl 2xl:max-w-[min(95vw,1600px)] mx-auto w-full px-4 py-6 flex flex-col gap-4">
        <h1 className="text-xl font-semibold text-text-primary flex items-center gap-2">
          <BarChart3 className="w-5 h-5 text-emerald-400" /> {t('stats.title')}
        </h1>

        {stats && lib && (
          <>
            <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
              <SummaryCard icon={<Clock className="w-4 h-4 text-emerald-400" />} label={t('stats.watch_time')} value={formatWatchTime(lib.watchSeconds)} />
              <SummaryCard
                icon={<Film className="w-4 h-4 text-purple-400" />} label={t('stats.titles')}
                value={String(lib.titles)} hint={t('stats.titles_hint', { completed: lib.completed, inProgress: lib.inProgress })}
              />
              <SummaryCard
                icon={<Download className="w-4 h-4 text-green-400" />} label={t('stats.downloaded')}
                value={formatBytes(stats.downloads.bytesDownloaded)} hint={t('stats.downloads_hint', { completed: stats.downloads.completed, total: stats.downloads.total })}
              />
              <SummaryCard icon={<Search className="w-4 h-4 text-text-secondary" />} label={t('stats.searches')} value={String(stats.searchQueries)} />
              <SummaryCard
                icon={<Bell className="w-4 h-4 text-amber-400" />} label={t('stats.watchlists')}
                value={String(stats.watchlists.count)} hint={t('stats.watchlists_hint', { hits: stats.watchlists.hits })}
              />
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
              <BarCard
                title={t('stats.added_by_month')}
                labels={lib.addedByMonth.map(m => monthLabel(m.month, i18n.language))}
                values={lib.addedByMonth.map(m => m.count)}
              />
              <BarCard
                title={t('stats.plays_by_weekday')}
                labels={weekdayKeys.map(k => t(`stats.weekday.${k}`))}
                values={lib.playsByWeekday}
              />
            </div>
            <BarCard
              title={t('stats.plays_by_hour')}
              labels={lib.playsByHour.map((_, h) => (h % 6 === 0 ? `${h}h` : ''))}
              values={lib.playsByHour}
              dense
            />

            {Object.keys(lib.byKind).length > 0 && (
              <div className="card">
                <p className="text-sm font-medium text-text-primary mb-2">{t('stats.by_kind')}</p>
                <div className="flex flex-wrap gap-2">
                  {Object.entries(lib.byKind).map(([kind, count]) => (
                    <span key={kind} className="text-xs px-2.5 py-1 rounded-full bg-surface-secondary text-text-secondary">
                      {t(`stats.kind.${kind}`, kind)}: <span className="text-text-primary font-medium">{count}</span>
                    </span>
                  ))}
                </div>
              </div>
            )}

            <p className="text-xs text-text-muted">{t('stats.disclaimer')}</p>
          </>
        )}
      </main>
    </div>
  )
}

type SummaryProps = { icon: React.ReactNode; label: string; value: string; hint?: string }

function SummaryCard({ icon, label, value, hint }: Readonly<SummaryProps>) {
  return (
    <div className="card flex flex-col gap-1">
      <p className="text-xs text-text-muted flex items-center gap-1.5">{icon} {label}</p>
      <p className="text-lg font-semibold text-text-primary">{value}</p>
      {hint && <p className="text-[11px] text-text-muted">{hint}</p>}
    </div>
  )
}

type BarCardProps = { title: string; labels: string[]; values: number[]; dense?: boolean }

function BarCard({ title, labels, values, dense }: Readonly<BarCardProps>) {
  const heights = barHeights(values)
  return (
    <div className="card">
      <p className="text-sm font-medium text-text-primary mb-3">{title}</p>
      <div className={`flex items-end ${dense ? 'gap-0.5' : 'gap-2'} h-28`}>
        {values.map((v, i) => (
          <div key={`${title}-${labels[i] || i}`} className="flex-1 flex flex-col items-center gap-1 min-w-0" title={`${labels[i] || i}: ${v}`}>
            <div className="w-full flex items-end justify-center flex-1">
              <div
                className="w-full max-w-8 rounded-t bg-emerald-500/70 dark:bg-emerald-400/60 transition-all"
                style={{ height: `${heights[i]}%`, minHeight: v > 0 ? '3px' : '0' }}
              />
            </div>
            <span className="text-[10px] text-text-muted truncate w-full text-center">{labels[i]}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
