import { useTranslation } from 'react-i18next'
import { HardDrive } from 'lucide-react'
import { LocalMount, AdminUser } from '../../api/client'
import { Sheet } from '../Sheet'
import { MountBadge, MountSpaceLabel } from './MountBadge'

type Props = {
  readonly open: boolean
  readonly onClose: () => void
  readonly mounts: LocalMount[]
  readonly activeMount: string
  readonly onSelectMount: (name: string) => void
  readonly canViewAsUser: boolean
  readonly viewAsUser: string
  readonly onViewAsUser: (username: string) => void
  readonly adminUsers: AdminUser[]
}

// Dropdown de mounts (mobile) — a sidebar some em <md.
export function MountSheet({ open, onClose, mounts, activeMount, onSelectMount, canViewAsUser, viewAsUser, onViewAsUser, adminUsers }: Props) {
  const { t } = useTranslation()
  return (
    <Sheet
      open={open}
      onClose={onClose}
      title={t('local.mounts')}
      icon={<HardDrive className="w-4 h-4 text-green-400 flex-shrink-0" />}
      size="sm"
    >
      <ul className="space-y-1">
        {mounts.map((m) => (
          <li key={m.name}>
            <button
              onClick={() => { onSelectMount(m.name); onClose() }}
              className={`w-full flex items-center gap-2 px-3 min-h-[44px] rounded-lg text-sm transition-colors ${
                m.name === activeMount
                  ? 'bg-green-500/10 text-green-400 border border-green-500/30'
                  : 'text-text-primary hover:bg-surface-tertiary border border-transparent'
              }`}
            >
              <HardDrive className="w-4 h-4 flex-shrink-0" />
              <span className="truncate">{m.name}</span>
              <MountBadge m={m} />
            </button>
            <MountSpaceLabel m={m} />
          </li>
        ))}
      </ul>
      {canViewAsUser && (
        <div className="mt-4 pt-4 border-t border-default">
          <h3 className="text-xs uppercase tracking-wider text-text-muted mb-2">{t('local.viewAs')}</h3>
          <select
            value={viewAsUser}
            onChange={(e) => { onViewAsUser(e.target.value); onClose() }}
            className="w-full px-3 py-2 rounded-lg text-base bg-surface-secondary border border-default text-text-primary focus:border-green-500/50 focus:outline-none"
          >
            <option value="">{t('local.mySpace')}</option>
            {adminUsers.map((u) => (
              <option key={u.id} value={u.username}>{u.username}</option>
            ))}
          </select>
        </div>
      )}
    </Sheet>
  )
}
