import { useTranslation } from 'react-i18next'
import { Lock, Users } from 'lucide-react'
import { formatBytes } from '../../lib/format'
import { LocalMount } from '../../api/client'

// Barra de espaço livre/total do filesystem do mount (discos físicos, rclone).
// Some quando o backend não conseguiu medir (mount quebrado → totalBytes 0).
// MountBadge flags a mount's visibility: 🔒 per-user (private subdir) or
// 👥 restricted (visible only to specific users). Shared mounts get no badge.
export function MountBadge({ m }: { readonly m: LocalMount }) {
  const { t } = useTranslation()
  if (m.userSubpath) {
    return <Lock className="w-3 h-3 text-amber-400 flex-shrink-0" aria-label={t('local.mount.privateAria')} />
  }
  if (m.restricted) {
    return <Users className="w-3 h-3 text-blue-400 flex-shrink-0" aria-label={t('local.mount.restrictedAria')} />
  }
  return null
}

export function MountSpaceLabel({ m }: { readonly m: LocalMount }) {
  const { t } = useTranslation()
  if (!m.totalBytes || m.totalBytes <= 0) return null
  const free = m.freeBytes ?? 0
  const pctUsed = Math.min(100, Math.max(0, Math.round(((m.totalBytes - free) / m.totalBytes) * 100)))
  let barColor = 'bg-green-500'
  if (pctUsed > 90) barColor = 'bg-red-500'
  else if (pctUsed > 75) barColor = 'bg-amber-500'
  return (
    <div className="px-3 pb-1 -mt-0.5">
      <div className="h-1 rounded-full bg-surface-tertiary/80 overflow-hidden">
        <div className={`h-full rounded-full ${barColor}`} style={{ width: `${pctUsed}%` }} />
      </div>
      <p className="text-[10px] text-text-muted mt-0.5">{t('local.mount.spaceFree', { free: formatBytes(free), total: formatBytes(m.totalBytes) })}</p>
    </div>
  )
}
