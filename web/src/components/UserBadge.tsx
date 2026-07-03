import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { LogOut, User, Shield, ChevronDown } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'

/**
 * Avatar + role badge with a dropdown menu (logout).
 * Uses a document-level pointer listener for click-outside detection — more reliable
 * than the previous onBlur+setTimeout approach which lost clicks on iOS touch.
 */
export default function UserBadge() {
  const { t } = useTranslation()
  const { user, logout, isAdmin, enabled } = useAuth()
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handlePointerDown = (e: PointerEvent) => {
      // Close only if the pointer landed outside the dropdown wrapper.
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    // pointerdown fires once on touch and mouse — better than click which can be swallowed
    document.addEventListener('pointerdown', handlePointerDown)
    return () => document.removeEventListener('pointerdown', handlePointerDown)
  }, [open])

  if (!enabled || !user) return null

  return (
    <div ref={containerRef} className="relative">
      <button
        onClick={() => setOpen(o => !o)}
        className="flex items-center gap-1.5 max-w-full min-w-0 text-sm text-text-primary hover:text-text-primary bg-surface-secondary border border-default rounded-lg px-2.5 py-1.5 transition-colors"
      >
        {isAdmin ? <Shield className="w-3.5 h-3.5 flex-shrink-0 text-yellow-400" /> : <User className="w-3.5 h-3.5 flex-shrink-0" />}
        {/* truncate + min-w-0 so a narrow container (collapsed sidebar rail)
            shrinks the name to nothing instead of overflowing the rail. */}
        <span className="hidden sm:inline truncate min-w-0">{user.username}</span>
        <ChevronDown className={`w-3 h-3 flex-shrink-0 text-text-muted transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {open && (
        // Opens UPWARD + anchored left: the badge sits at the bottom of the
        // left sidebar, so a downward/right menu would fall off-screen.
        <div className="absolute left-0 bottom-full mb-1 w-44 bg-surface-secondary border border-default rounded-lg shadow-xl z-50 py-1">
          <div className="px-3 py-2 border-b border-default">
            <p className="text-sm text-text-primary truncate">{user.username}</p>
            <p className="text-xs text-text-muted flex items-center gap-1">
              {isAdmin ? (
                <><Shield className="w-3 h-3 text-yellow-400" /> {t('misc.admin_role')}</>
              ) : (
                <><User className="w-3 h-3" /> {t('misc.user_role')}</>
              )}
            </p>
          </div>
          <button
            onClick={() => { setOpen(false); logout() }}
            className="w-full flex items-center gap-2 px-3 py-2 text-sm text-text-primary hover:bg-surface-tertiary hover:text-red-400 transition-colors"
          >
            <LogOut className="w-3.5 h-3.5" />
            {t('misc.logout')}
          </button>
        </div>
      )}
    </div>
  )
}
