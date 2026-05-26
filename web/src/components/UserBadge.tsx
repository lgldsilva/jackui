import { useState, useEffect, useRef } from 'react'
import { LogOut, User, Shield, ChevronDown } from 'lucide-react'
import { useAuth } from '../auth/AuthContext'

/**
 * Avatar + role badge with a dropdown menu (logout).
 * Uses a document-level pointer listener for click-outside detection — more reliable
 * than the previous onBlur+setTimeout approach which lost clicks on iOS touch.
 */
export default function UserBadge() {
  const { user, logout, isAdmin, enabled } = useAuth()
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (e: PointerEvent) => {
      // Close only if the pointer landed outside the dropdown wrapper.
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    // pointerdown fires once on touch and mouse — better than click which can be swallowed
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [open])

  if (!enabled || !user) return null

  return (
    <div ref={containerRef} className="relative">
      <button
        onClick={() => setOpen(o => !o)}
        className="flex items-center gap-1.5 text-sm text-gray-300 hover:text-gray-100 bg-gray-800 border border-gray-700 rounded-lg px-2.5 py-1.5 transition-colors"
      >
        {isAdmin ? <Shield className="w-3.5 h-3.5 text-yellow-400" /> : <User className="w-3.5 h-3.5" />}
        <span className="hidden sm:inline">{user.username}</span>
        <ChevronDown className={`w-3 h-3 text-gray-500 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {open && (
        <div className="absolute right-0 mt-1 w-44 bg-gray-800 border border-gray-700 rounded-lg shadow-xl z-50 py-1">
          <div className="px-3 py-2 border-b border-gray-700">
            <p className="text-sm text-gray-200 truncate">{user.username}</p>
            <p className="text-xs text-gray-500 flex items-center gap-1">
              {isAdmin ? (
                <><Shield className="w-3 h-3 text-yellow-400" /> Admin</>
              ) : (
                <><User className="w-3 h-3" /> Usuário</>
              )}
            </p>
          </div>
          <button
            onClick={() => { setOpen(false); logout() }}
            className="w-full flex items-center gap-2 px-3 py-2 text-sm text-gray-300 hover:bg-gray-700 hover:text-red-400 transition-colors"
          >
            <LogOut className="w-3.5 h-3.5" />
            Sair
          </button>
        </div>
      )}
    </div>
  )
}
