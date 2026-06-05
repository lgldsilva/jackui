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
 * Envolve um item de lista e revela uma ação "Apagar" ao deslizar para a
 * esquerda (estilo iOS). Só arma no mobile — no desktop o gesto é inócuo e as
 * ações de hover do próprio item continuam valendo. Reusa o `useSwipe`.
 */
export function SwipeRow({ children, onDelete, deleteLabel = 'Apagar', disabled = false }: SwipeRowProps) {
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
      {/* Ação revelada por baixo, à direita */}
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
      {/* Conteúdo que desliza. Sem onClick aqui (acessibilidade): pra recolher a
          ação revelada, basta deslizar de volta (onRight) — o conteúdo segue
          recebendo seus próprios cliques/teclado normalmente. */}
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
