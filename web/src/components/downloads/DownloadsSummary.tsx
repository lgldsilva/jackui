import { useTranslation } from 'react-i18next'
import { Activity, Users, ArrowDownCircle, ArrowUpCircle } from 'lucide-react'
import { formatRate } from '../../lib/format'
import { StatCard } from './StatCard'

// DownloadsSummary — the 4 stat cards at the top of the downloads dashboard.
export function DownloadsSummary({ totalDown, totalUp, totalPeers, activeValue, queueSubtitle }: {
  readonly totalDown: number
  readonly totalUp: number
  readonly totalPeers: number
  readonly activeValue: string
  readonly queueSubtitle?: string
}) {
  const { t } = useTranslation()
  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
      <StatCard
        icon={<ArrowDownCircle className="w-5 h-5" />}
        label={t('downloads.page.statDownload')}
        value={formatRate(totalDown)}
        gradient="from-emerald-500/20 to-teal-500/10"
        iconColor="text-emerald-400"
        pulse={totalDown > 0}
      />
      <StatCard
        icon={<ArrowUpCircle className="w-5 h-5" />}
        label={t('downloads.page.statUpload')}
        value={formatRate(totalUp)}
        gradient="from-violet-500/20 to-purple-500/10"
        iconColor="text-violet-400"
        pulse={totalUp > 0}
      />
      <StatCard
        icon={<Users className="w-5 h-5" />}
        label={t('downloads.page.statPeers')}
        value={String(totalPeers)}
        gradient="from-blue-500/20 to-cyan-500/10"
        iconColor="text-blue-400"
      />
      <StatCard
        icon={<Activity className="w-5 h-5" />}
        label={t('downloads.page.statQueue')}
        value={activeValue}
        subtitle={queueSubtitle}
        gradient="from-amber-500/20 to-orange-500/10"
        iconColor="text-amber-400"
      />
    </div>
  )
}
