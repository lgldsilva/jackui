import { useTranslation, Trans } from 'react-i18next'
import { HardDrive } from 'lucide-react'
import { LocalMount, AdminUser } from '../../api/client'
import { MountBadge, MountSpaceLabel } from './MountBadge'

type Props = {
  readonly mounts: LocalMount[]
  readonly activeMount: string
  readonly onSelectMount: (name: string) => void
  readonly canViewAsUser: boolean
  readonly viewAsUser: string
  readonly onViewAsUser: (username: string) => void
  readonly adminUsers: AdminUser[]
}

// Sidebar — desktop é coluna fixa à esquerda. No mobile some por completo
// (hidden) e dá lugar a um dropdown de mount na barra do breadcrumb, que
// não rouba altura nem força scroll horizontal de chips.
export function LocalSidebar({ mounts, activeMount, onSelectMount, canViewAsUser, viewAsUser, onViewAsUser, adminUsers }: Props) {
  const { t } = useTranslation()
  return (
    <aside className="hidden md:block md:w-56 flex-shrink-0 md:overflow-y-auto">
      <h2 className="text-xs uppercase tracking-wider text-text-muted mb-2 md:mb-3">
        {t('local.mounts')}
      </h2>
      {mounts.length === 0 ? (
        <><p className="text-sm text-text-muted">
          <Trans i18nKey="local.noMountsConfigured" components={{ c: <code /> }} />
        </p>
        <code className="block mt-2 p-2 bg-surface-secondary rounded text-xs">
            external:{'\n'}  mounts:{'\n'}    - name: HD Externo{'\n'}      path: /mnt/external
          </code></>
      ) : (
        <ul className="flex flex-col gap-1 space-y-1">
          {mounts.map((m) => {
            const active = m.name === activeMount
            return (
              <li key={m.name} className="flex-shrink-0">
                <button
                  onClick={() => onSelectMount(m.name)}
                  className={`w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-colors whitespace-nowrap ${
                    active
                      ? 'bg-green-500/10 text-green-400 border border-green-500/30'
                      : 'text-text-primary hover:bg-surface-secondary border border-transparent'
                  }`}
                >
                  <HardDrive className="w-4 h-4 flex-shrink-0" />
                  <span className="truncate">{m.name}</span>
                  <MountBadge m={m} />
                </button>
                <MountSpaceLabel m={m} />
              </li>
            )
          })}
        </ul>
      )}

      {canViewAsUser && (
        <div className="mt-5 md:mt-6">
          <h2 className="text-xs uppercase tracking-wider text-text-muted mb-2">{t('local.viewAs')}</h2>
          <select
            value={viewAsUser}
            onChange={(e) => onViewAsUser(e.target.value)}
            className="w-full px-3 py-2 rounded-lg text-sm bg-surface-secondary border border-default text-text-primary focus:border-green-500/50 focus:outline-none"
          >
            <option value="">{t('local.mySpace')}</option>
            {adminUsers.map((u) => (
              <option key={u.id} value={u.username}>{u.username}</option>
            ))}
          </select>
          {viewAsUser && (
            <p className="mt-1.5 text-[11px] text-amber-400/80">
              <Trans i18nKey="local.viewingSpaceOf" values={{ user: viewAsUser }} components={{ b: <strong /> }} />
            </p>
          )}
        </div>
      )}
    </aside>
  )
}
