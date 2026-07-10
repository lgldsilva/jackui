import { useTranslation } from 'react-i18next'
import {
  Pause, Users, Zap, Plus, Wifi, ListFilter, CheckSquare, AlertCircle,
} from 'lucide-react'
import { downloadUsers, DownloadUserEntry } from '../../api/client'
import type { QuerySetter } from '../../lib/useQueryState'
import type { Tab } from './tabs'

// DownloadsTabsBar — status tabs (with badge counts) + the admin "all users"
// toggle + the "add torrent/magnet" button.
export function DownloadsTabsBar(props: {
  readonly activeTab: Tab
  readonly setActiveTab: (t: Tab) => void
  readonly tabCounts: Record<Tab, number>
  readonly isAdmin: boolean
  readonly isGuest: boolean
  readonly showAllUsers: boolean
  readonly setQuery: QuerySetter
  readonly setUsersParam: (v: string) => void
  readonly setAvailableUsers: (u: DownloadUserEntry[]) => void
  readonly setPreloadFiles: (f: File[] | null) => void
  readonly setShowAddModal: (v: boolean) => void
}) {
  const {
    activeTab, setActiveTab, tabCounts, isAdmin, isGuest, showAllUsers,
    setQuery, setUsersParam, setAvailableUsers, setPreloadFiles, setShowAddModal,
  } = props
  const { t } = useTranslation()
  return (
    <div className="flex items-center justify-between border-b border-default/60 flex-wrap gap-3">
      <div className="flex items-center gap-0.5 overflow-x-auto">
        {([
          { key: 'all'         as Tab, label: t('downloads.page.tabAll'),         icon: <ListFilter className="w-3.5 h-3.5" /> },
          { key: 'downloading' as Tab, label: t('downloads.page.tabDownloading'), icon: <Zap className="w-3.5 h-3.5" /> },
          { key: 'paused'      as Tab, label: t('downloads.page.tabPaused'),      icon: <Pause className="w-3.5 h-3.5" /> },
          { key: 'completed'   as Tab, label: t('downloads.page.tabCompleted'),   icon: <CheckSquare className="w-3.5 h-3.5" /> },
          { key: 'failed'      as Tab, label: t('downloads.page.tabFailed'),      icon: <AlertCircle className="w-3.5 h-3.5" /> },
          { key: 'network'     as Tab, label: t('downloads.page.tabNetwork'),     icon: <Wifi className="w-3.5 h-3.5" /> },
        ]).map(tab => (
          <button
            key={tab.key}
            onClick={() => setActiveTab(tab.key)}
            className={`
              flex items-center gap-1.5 px-3 py-2.5 text-xs font-medium whitespace-nowrap
              border-b-2 transition-all duration-200
              ${activeTab === tab.key
                ? 'border-emerald-400 text-emerald-400'
                : 'border-transparent text-text-secondary hover:text-text-primary hover:border-strong'}
            `}
          >
            {tab.icon}
            {tab.label}
            {tabCounts[tab.key] > 0 && (
              <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-semibold min-w-[18px] text-center ${tabBadgeClass(activeTab, tab.key)}`}>
                {tabCounts[tab.key]}
              </span>
            )}
          </button>
        ))}
      </div>
      <div className="flex items-center gap-2">
        {isAdmin && (
          <button
            onClick={() => {
              if (showAllUsers) { setQuery({ users: null, uid: null }) } // desligar: limpa users + uid órfão
              else { setUsersParam('all'); downloadUsers().then(setAvailableUsers).catch(() => {}) }
            }}
            className={`flex items-center gap-1.5 text-xs px-4 py-2 rounded-xl font-semibold transition-all duration-200 mb-2 md:mb-0 ${
              showAllUsers
                ? 'bg-violet-500 hover:bg-violet-600 text-white shadow-lg shadow-violet-500/10'
                : 'bg-surface-secondary border border-default text-text-secondary hover:text-text-primary'
            }`}
          >
            <Users className="w-4 h-4" />
            {showAllUsers ? t('downloads.page.allUsers') : t('downloads.page.myDownloads')}
          </button>
        )}
        {!isGuest && (
          <button
            onClick={() => {
              setPreloadFiles(null)
              setShowAddModal(true)
            }}
            className="flex items-center gap-1.5 text-xs bg-cyan-500 hover:bg-cyan-600 text-white px-4 py-2 rounded-xl font-semibold transition-all duration-200 shadow-lg shadow-cyan-500/10 mb-2 md:mb-0"
          >
            <Plus className="w-4 h-4" /> {t('downloads.page.addTorrentMagnet')}
          </button>
        )}
      </div>
    </div>
  )
}

function tabBadgeClass(activeTab: Tab, tabKey: string): string {
  if (activeTab === tabKey) return 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300'
  if (tabKey === 'failed') return 'bg-red-500/20 text-red-400'
  return 'bg-surface-tertiary text-text-secondary'
}
