import type { SearchPhase } from '../../lib/searchResultsCache'

// Bolinha de status por aba (amarela=carregando, verde=pronto, vermelha=erro).
// Vive em arquivo próprio para não engordar o SearchPage (god-file).
export function PhaseIndicator({ phase }: { readonly phase: SearchPhase }) {
  if (phase === 'idle') return null
  if (phase === 'cache' || phase === 'live')
    return <span className="w-2 h-2 rounded-full bg-yellow-400 animate-pulse flex-shrink-0" />
  if (phase === 'done')
    return <span className="w-2 h-2 rounded-full bg-green-400 flex-shrink-0" />
  return <span className="w-2 h-2 rounded-full bg-red-400 flex-shrink-0" />
}
