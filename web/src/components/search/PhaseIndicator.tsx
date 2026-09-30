import type { SearchPhase } from '../../lib/searchResultsCache'

// Status dot per tab (yellow=loading, green=ready, red=error).
// Lives in its own file to avoid bloating the SearchPage (god-file).
export function PhaseIndicator({ phase }: { readonly phase: SearchPhase }) {
  if (phase === 'idle') return null
  if (phase === 'cache' || phase === 'live')
    return <span className="w-2 h-2 rounded-full bg-yellow-400 animate-pulse flex-shrink-0" />
  if (phase === 'done')
    return <span className="w-2 h-2 rounded-full bg-green-400 flex-shrink-0" />
  return <span className="w-2 h-2 rounded-full bg-red-400 flex-shrink-0" />
}
