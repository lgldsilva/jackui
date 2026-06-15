import { useEffect, useRef, useState, type RefObject } from 'react'

// nextReveal: próximo total de linhas visíveis ao revelar mais um lote, sem passar
// do total. Puro/testável; a parte de DOM (IntersectionObserver) fica no hook.
export function nextReveal(visible: number, step: number, total: number): number {
  return Math.min(visible + step, total)
}

export type IncrementalReveal = {
  visible: number      // quantas linhas renderizar agora (≤ total)
  hasMore: boolean     // ainda há linhas escondidas?
  remaining: number    // quantas faltam revelar
  sentinelRef: RefObject<HTMLDivElement> // marcador no fim da lista
  showMore: () => void // revela mais um lote (botão de fallback)
}

// useIncrementalReveal: renderiza uma lista longa em LOTES (default 100), revelando
// mais conforme o usuário ROLA até o fim (sentinela + IntersectionObserver) e também
// via showMore() (botão). Mantém a proteção de performance — nunca monta milhares de
// linhas de uma vez — SEM esconder o resto atrás de um filtro que o usuário teria de
// adivinhar. `resetKey` muda (novo torrent / filtro / ordenação) → recomeça do 1º lote.
export function useIncrementalReveal(total: number, resetKey: unknown, step = 100): IncrementalReveal {
  const [visible, setVisible] = useState(step)
  useEffect(() => { setVisible(step) }, [resetKey, step])

  const shown = Math.min(visible, total)
  const hasMore = shown < total
  const sentinelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = sentinelRef.current
    if (!el || !hasMore || typeof IntersectionObserver === 'undefined') return
    // rootMargin folgado pra começar a carregar um pouco antes de bater o fim.
    // Como um lote (100 linhas) é bem mais alto que o viewport da sidebar, ao
    // revelar mais a sentinela some da tela → não cascateia tudo de uma vez.
    const io = new IntersectionObserver(
      (entries) => { if (entries.some((e) => e.isIntersecting)) setVisible((v) => nextReveal(v, step, total)) },
      { rootMargin: '240px' },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [hasMore, total, step])

  return { visible: shown, hasMore, remaining: Math.max(0, total - shown), sentinelRef, showMore: () => setVisible((v) => nextReveal(v, step, total)) }
}
