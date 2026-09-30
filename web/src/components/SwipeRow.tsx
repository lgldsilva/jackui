import { ReactNode, useRef, useState } from 'react'
import { Trash2 } from 'lucide-react'
import { useSwipe } from '../lib/useSwipe'
import { useIsMobile } from '../lib/useMediaQuery'

export type SwipeRowProps = {
  readonly children: ReactNode
  readonly onDelete: () => void
  readonly deleteLabel?: string
  readonly disabled?: boolean
}

/**
 * Wraps a list item and reveals a "Delete" action when swiped to the
 * left (iOS style). Only armed on mobile — on desktop the gesture is moot and the
 * item's own hover actions keep working. Reuses `useSwipe`.
 */
export function SwipeRow({ children, onDelete, deleteLabel = 'Delete', disabled = false }: SwipeRowProps) {
  const [revealed, setRevealed] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const isMobile = useIsMobile()
  const armed = isMobile && !disabled

  useSwipe(
    ref,
    { onLeft: () => setRevealed(true), onRight: () => setRevealed(false) },
    { enabled: armed, threshold: 48, restraint: 40 },
  )

  return (
    <div className="relative overflow-hidden">
      {/* Action revealed underneath, on the right */}
      <div className="absolute inset-y-0 right-0 flex">
        <button
          onClick={() => { onDelete(); setRevealed(false) }}
          tabIndex={revealed ? 0 : -1}
          aria-hidden={!revealed}
          className="flex items-center gap-1.5 bg-red-500 text-white px-4 text-sm font-medium"
        >
          <Trash2 className="w-4 h-4" />
          {deleteLabel}
        </button>
      </div>
      {/* Content that slides. No onClick here (accessibility): to collapse the
          revealed action, just swipe back (onRight) — the content keeps
          receiving its own clicks/keyboard normally. */}
      <div
        ref={ref}
        className="relative bg-surface-secondary transition-transform duration-200 ease-out"
        style={{ transform: revealed ? 'translateX(-6rem)' : 'translateX(0)' }}
      >
        {children}
      </div>
    </div>
  )
}
