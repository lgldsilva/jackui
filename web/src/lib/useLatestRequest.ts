import { useEffect, useMemo, useRef } from 'react'

export interface LatestRequest {
  /** Starts a request, superseding every earlier one. The returned predicate
   *  is true only while that request is still the newest one. */
  begin: () => () => boolean
  /** Supersedes any in-flight request without starting a new one. */
  invalidate: () => void
}

/**
 * Guard against out-of-order responses: a response is applied only if no newer
 * request (or invalidate) happened while it was in flight. Unmount invalidates
 * too, so a late response never sets state on a dead component.
 *
 *   const isLatest = req.begin()
 *   const data = await fetchThing(id)
 *   if (!isLatest()) return
 */
export function useLatestRequest(): LatestRequest {
  const seq = useRef(0)
  useEffect(() => () => { seq.current++ }, [])
  return useMemo<LatestRequest>(() => ({
    begin: () => {
      const mine = ++seq.current
      return () => seq.current === mine
    },
    invalidate: () => { seq.current++ },
  }), [])
}
