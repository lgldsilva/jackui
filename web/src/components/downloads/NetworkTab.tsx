import { useTranslation } from 'react-i18next'
import { Loader2, Gauge, Wifi, ArrowDownCircle, ArrowUpCircle } from 'lucide-react'
import { formatRate } from '../../lib/format'

// NetworkTab — bandwidth limit controls.
export function NetworkTab({ limitDownKB, limitUpKB, setLimitDownKB, setLimitUpKB,
  limitsSaving, limitsMsg, onSaveLimits, totalDown, totalUp, totalPeers,
}: {
  readonly limitDownKB: string
  readonly limitUpKB: string
  readonly setLimitDownKB: (v: string) => void
  readonly setLimitUpKB: (v: string) => void
  readonly limitsSaving: boolean
  readonly limitsMsg: string
  readonly onSaveLimits: () => void
  readonly totalDown: number
  readonly totalUp: number
  readonly totalPeers: number
}) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-6">
      {/* Live network overview */}
      <div className="rounded-xl border border-default/50 bg-card dark:bg-gradient-to-br dark:from-gray-800/80 dark:to-gray-900/80 backdrop-blur-sm p-6">
        <h3 className="text-sm font-semibold text-text-primary uppercase tracking-wider flex items-center gap-2 mb-5">
          <Wifi className="w-4 h-4 text-cyan-400" />
          {t('downloads.page.realtimeMonitoring')}
        </h3>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-6">
          <div className="flex flex-col gap-1">
            <span className="text-xs text-text-muted">{t('downloads.page.currentDownload')}</span>
            <span className="text-2xl font-bold text-emerald-400">{formatRate(totalDown)}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-xs text-text-muted">{t('downloads.page.currentUpload')}</span>
            <span className="text-2xl font-bold text-violet-400">{formatRate(totalUp)}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-xs text-text-muted">{t('downloads.page.connectedPeers')}</span>
            <span className="text-2xl font-bold text-blue-400">{totalPeers}</span>
          </div>
        </div>
      </div>

      {/* Bandwidth limits form */}
      <div className="rounded-xl border border-default/50 bg-card dark:bg-gradient-to-br dark:from-gray-800/60 dark:to-gray-900/60 backdrop-blur-sm p-6">
        <h3 className="text-sm font-semibold text-text-primary uppercase tracking-wider flex items-center gap-2 mb-5">
          <Gauge className="w-4 h-4 text-amber-400" />
          {t('downloads.page.speedLimits')}
        </h3>
        <p className="text-xs text-text-muted mb-4">
          {t('downloads.page.speedLimitsHint')}
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-5">
          <div className="flex flex-col gap-2">
            <label className="text-xs text-text-secondary flex items-center gap-1.5">
              <ArrowDownCircle className="w-3.5 h-3.5 text-emerald-400" />
              {t('downloads.page.downloadLimit')}
            </label>
            <input
              type="number"
              min={0}
              placeholder={t('downloads.page.unlimited')}
              value={limitDownKB}
              onChange={e => setLimitDownKB(e.target.value)}
              className="bg-surface/80 border border-default rounded-lg px-3 py-2.5 text-text-primary text-sm focus:outline-none focus:border-emerald-500 focus:ring-1 focus:ring-emerald-500/30 transition-all"
            />
          </div>
          <div className="flex flex-col gap-2">
            <label className="text-xs text-text-secondary flex items-center gap-1.5">
              <ArrowUpCircle className="w-3.5 h-3.5 text-violet-400" />
              {t('downloads.page.uploadLimit')}
            </label>
            <input
              type="number"
              min={0}
              placeholder={t('downloads.page.unlimited')}
              value={limitUpKB}
              onChange={e => setLimitUpKB(e.target.value)}
              className="bg-surface/80 border border-default rounded-lg px-3 py-2.5 text-text-primary text-sm focus:outline-none focus:border-violet-500 focus:ring-1 focus:ring-violet-500/30 transition-all"
            />
          </div>
        </div>
        <div className="flex items-center gap-3">
          <button
            onClick={onSaveLimits}
            disabled={limitsSaving}
            className="flex items-center gap-2 text-sm bg-emerald-500/20 hover:bg-emerald-500/30 disabled:opacity-50 text-emerald-700 dark:text-emerald-300 border border-emerald-500/40 px-5 py-2 rounded-lg transition-all font-medium"
          >
            {limitsSaving && <Loader2 className="w-4 h-4 animate-spin" />}
            {t('downloads.page.applyLimits')}
          </button>
          {limitsMsg && (
            <span className={`text-sm font-medium ${limitsMsg === t('downloads.page.limitsApplied') ? 'text-emerald-400' : 'text-red-400'}`}>
              {limitsMsg}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
