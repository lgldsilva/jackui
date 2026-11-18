import { useEffect, useRef, type MutableRefObject } from 'react'

/**
 * Swallows the keyup of a Space the player handled on keydown: Firefox clicks a
 * focused button on Space keyup even when keydown was preventDefault-ed, which
 * would re-activate the control the user last clicked.
 *
 * The caller sets `current = true` when it handles a Space keydown and `false`
 * when it sees one it does not handle. The flag is a ref because play()/pause()
 * re-renders the player (timeupdate) between keydown and keyup; it is dropped
 * on window blur and on deactivation so it cannot outlive its key press.
 */
export function useSpaceKeyUpGuard(active: boolean): MutableRefObject<boolean> {
  const pending = useRef(false)
  useEffect(() => {
    if (!active) return
    const onKeyUp = (e: KeyboardEvent) => {
      if (e.key !== ' ' || !pending.current) return
      pending.current = false
      e.preventDefault()
    }
    const forget = () => { pending.current = false }
    globalThis.addEventListener('keyup', onKeyUp)
    globalThis.addEventListener('blur', forget)
    return () => {
      globalThis.removeEventListener('keyup', onKeyUp)
      globalThis.removeEventListener('blur', forget)
      forget()
    }
  }, [active])
  return pending
}
