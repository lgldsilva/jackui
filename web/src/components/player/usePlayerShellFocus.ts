import { useDialogFocus } from '../../lib/useDialogFocus'

/**
 * Focus management for the player shell, active only while it is expanded
 * (modal or full-viewport) — that is when shellProps declares aria-modal.
 * Minimized (PiP / audio bar) the page behind must stay fully usable, so there
 * is no trap and focus goes back to whatever had it before the player expanded.
 *
 * Focus lands on the shell itself rather than on its first control: a focused
 * header button would swallow Space/Enter meant for playback, and focusing the
 * <video> would hand the keys to the native media handler.
 */
export function usePlayerShellFocus(minimized: boolean) {
  return useDialogFocus(!minimized, { initialFocus: 'container' })
}
