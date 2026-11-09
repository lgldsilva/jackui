import { useEffect, useRef, type MutableRefObject } from 'react'

/**
 * A ref that is true while the component is mounted. Set inside the effect
 * body (not only at init) so React StrictMode's mount→unmount→mount leaves it
 * true — a ref initialised to true and only cleared in the cleanup stays
 * false forever after the dev double-mount.
 */
export function useMountedRef(): MutableRefObject<boolean> {
  const ref = useRef(false)
  useEffect(() => {
    ref.current = true
    return () => { ref.current = false }
  }, [])
  return ref
}
