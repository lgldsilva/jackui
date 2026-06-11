import { useState, useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { ExternalLink, ChevronDown, Check, Copy } from 'lucide-react'
import { usePersistedState } from '../../lib/storage'
import {
  type ExternalPlayer,
  type ExternalPlayerURLs,
  availableExternalPlayers,
  resolveExternalPlayer,
} from './externalPlayers'

// itemIcon: the leading glyph for a menu row. Copied state wins (transient
// tick), then the clipboard item gets a Copy glyph, everything else the
// external-link arrow. Extracted so the JSX has no nested ternary.
function itemIcon(p: ExternalPlayer, copied: boolean) {
  if (copied) return <Check className="w-3.5 h-3.5 text-green-400 flex-shrink-0" />
  const Icon = p.kind === 'clipboard' ? Copy : ExternalLink
  return <Icon className={`w-3.5 h-3.5 flex-shrink-0 ${p.accent}`} />
}

type ExternalPlayerMenuProps = {
  readonly urls: ExternalPlayerURLs
}

// Split button "Open in ▾" that replaces the row of per-app buttons
// (VLC/IINA/Infuse) plus a "Copy URL" item. The primary button activates the
// LAST-CHOSEN player (persisted under jackui:player.externalApp); the chevron
// opens the list to switch. Choosing an item both activates it AND becomes the
// new default — so the common case is a single click next time.
//
// Each item runs the exact action of the standalone button it replaced: a
// 'link' player navigates to its scheme/playlist URL; the 'clipboard' item
// copies the direct stream URL. No URL/scheme logic lives here — it all comes
// from externalPlayers.build() over the media URLs.
export function ExternalPlayerMenu({ urls }: ExternalPlayerMenuProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const [preferredId, setPreferredId] = usePersistedState<string | null>('player.externalApp', null)
  const containerRef = useRef<HTMLDivElement>(null)

  const players = availableExternalPlayers(urls)
  const primary = resolveExternalPlayer(urls, preferredId)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (e: PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [open])

  // activate runs the player's action. Returns to keep complexity flat: the
  // clipboard branch shows a transient "Copied" tick; the link branch opens the
  // app via its scheme (same target the old <a> used).
  const activate = useCallback((p: ExternalPlayer) => {
    setPreferredId(p.id)
    setOpen(false)
    const value = p.build(urls)
    if (!value) return
    if (p.kind === 'clipboard') {
      navigator.clipboard?.writeText(value)
      setCopiedId(p.id)
      globalThis.setTimeout(() => setCopiedId(null), 1500)
      return
    }
    // Open the app via its scheme/playlist. Same effect as clicking the old
    // <a href>: a new tab/navigation hands off to the OS handler.
    globalThis.open(value, '_blank', 'noopener')
  }, [urls, setPreferredId])

  if (!primary || players.length === 0) return null

  return (
    <div ref={containerRef} className="relative inline-flex">
      {/* Split button: primary opens the preferred player, the caret toggles
          the list. role/aria so it behaves as a menu button for AT + keyboard. */}
      <div className="inline-flex rounded-lg overflow-hidden border border-strong">
        <button
          type="button"
          onClick={() => activate(primary)}
          title={t(primary.hintKey)}
          className={`flex items-center gap-1.5 text-xs bg-surface-tertiary hover:bg-surface-secondary px-3 py-1.5 transition-colors ${primary.accent}`}
        >
          {copiedId === primary.id
            ? <Check className="w-3.5 h-3.5" />
            : <ExternalLink className="w-3.5 h-3.5" />}
          {t('player.external.open_in')} {t(primary.labelKey)}
        </button>
        <button
          type="button"
          onClick={() => setOpen(o => !o)}
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={t('player.external.choose')}
          className="flex items-center justify-center bg-surface-tertiary hover:bg-surface-secondary text-text-secondary px-1.5 border-l border-strong transition-colors"
        >
          <ChevronDown className={`w-3.5 h-3.5 transition-transform ${open ? 'rotate-180' : ''}`} />
        </button>
      </div>

      {open && (
        <div
          role="menu"
          className="absolute left-0 bottom-full mb-1 min-w-[200px] bg-surface-secondary border border-default rounded-lg shadow-xl z-50 py-1"
        >
          {players.map(p => (
            <button
              key={p.id}
              type="button"
              role="menuitem"
              onClick={() => activate(p)}
              title={t(p.hintKey)}
              className="w-full flex items-center gap-2 px-3 py-2 text-xs text-left text-text-primary hover:bg-surface-tertiary transition-colors"
            >
              {itemIcon(p, copiedId === p.id)}
              <span className="flex-1">{t(p.labelKey)}</span>
              {preferredId === p.id && p.kind !== 'clipboard' && (
                <span className="text-[10px] text-text-muted uppercase tracking-wide">{t('player.external.default')}</span>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
