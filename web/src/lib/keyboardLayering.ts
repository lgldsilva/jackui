// Keyboard layering rules for the player: which layer owns a key press when
// overlays (sheets, menus, dialogs) sit on top of it. DOM-only, no React.

const DIALOG = '[role="dialog"], dialog'

/** True when `target` sits in a dialog nested inside (layered over) `host`. */
export function isFromNestedDialog(target: EventTarget | null, host: Element): boolean {
  if (!(target instanceof Element)) return false
  const dialog = target.closest(DIALOG)
  return dialog !== null && dialog !== host
}
