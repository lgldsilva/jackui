import { useEffect, useRef, useState } from 'react'
import { BellRing, Copy, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { AppNotification, notificationsList, notificationsMarkRead } from '../api/client'
import { enablePush, isPushEnabled, isPushSupported } from '../lib/push'

// NotificationsBell shows the in-app feed (watchlist hits) with an unread
// badge, and hosts the "enable Web Push for this browser" CTA. Lives in the
// NavHeader on desktop (sidebar footer) and mobile (top bar).
export default function NotificationsBell() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [items, setItems] = useState<AppNotification[]>([])
  const [unread, setUnread] = useState(0)
  const [pushOn, setPushOn] = useState(false)
  const [busy, setBusy] = useState(false)
  const panelRef = useRef<HTMLDivElement>(null)

  const refresh = async () => {
    try {
      const r = await notificationsList()
      setItems(r.items)
      setUnread(r.unread)
    } catch { /* feed is best-effort */ }
  }

  useEffect(() => {
    refresh()
    isPushEnabled().then(setPushOn)
  }, [])

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (panelRef.current && !panelRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const toggleOpen = async () => {
    const next = !open
    setOpen(next)
    if (!next) return
    try {
      const r = await notificationsList()
      setItems(r.items)
      setUnread(r.unread)
      if (r.unread > 0) {
        await notificationsMarkRead()
        setUnread(0)
      }
    } catch { /* feed is best-effort */ }
  }

  const onEnablePush = async () => {
    setBusy(true)
    try {
      setPushOn(await enablePush())
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative" ref={panelRef}>
      <button
        onClick={toggleOpen}
        className="relative flex items-center justify-center w-9 h-9 rounded-lg text-text-secondary hover:text-text-primary hover:bg-surface-tertiary/40 transition-colors"
        title={t('notifications.title')}
        aria-label={t('notifications.title')}
      >
        <BellRing className="w-5 h-5" />
        {unread > 0 && (
          <span className="absolute -top-0.5 -right-0.5 min-w-[16px] h-4 px-1 rounded-full bg-amber-500 text-[10px] font-semibold text-black flex items-center justify-center">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute bottom-full left-0 mb-2 w-80 max-w-[85vw] z-50 card !p-0 shadow-xl border border-default max-md:fixed max-md:left-3 max-md:right-3 max-md:top-14 max-md:bottom-auto max-md:w-auto">
          <div className="flex items-center justify-between px-3 py-2 border-b border-default/60">
            <p className="text-sm font-medium text-text-primary">{t('notifications.title')}</p>
            {isPushSupported() && !pushOn && (
              <button
                onClick={onEnablePush}
                disabled={busy}
                className="text-xs text-amber-400 hover:text-amber-300 flex items-center gap-1"
              >
                {busy ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <BellRing className="w-3.5 h-3.5" />}
                {t('notifications.enable_push')}
              </button>
            )}
            {pushOn && <span className="text-[10px] text-emerald-400">{t('notifications.push_on')}</span>}
          </div>
          <div className="max-h-80 overflow-y-auto">
            {items.length === 0 && (
              <p className="text-xs text-text-muted text-center py-6">{t('notifications.empty')}</p>
            )}
            {items.map(n => <FeedItem key={n.id} n={n} />)}
          </div>
        </div>
      )}
    </div>
  )
}

function FeedItem({ n }: { readonly n: AppNotification }) {
  return (
    <div className={`px-3 py-2 border-b border-default/40 last:border-b-0 ${n.read ? 'opacity-60' : ''}`}>
      <div className="flex items-start justify-between gap-2">
        <p className="text-xs text-text-primary line-clamp-2" title={n.title}>{n.title}</p>
        {n.magnet && (
          <button
            onClick={() => navigator.clipboard?.writeText(n.magnet!)}
            className="flex-shrink-0 text-text-muted hover:text-text-primary p-0.5"
            title="Copiar magnet"
          >
            <Copy className="w-3.5 h-3.5" />
          </button>
        )}
      </div>
      <p className="text-[11px] text-text-muted mt-0.5">
        {n.body} · {new Date(n.createdAt).toLocaleString('pt-BR')}
      </p>
    </div>
  )
}
