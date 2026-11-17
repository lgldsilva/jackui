// Keyboard layering rules for the player: which layer owns a key press when
// overlays (sheets, menus, dialogs) sit on top of it. DOM-only, no React.

/** Marks the player shell so it can be told apart from dialogs layered over it. */
export const PLAYER_SHELL_ATTR = 'data-player-shell'

const DIALOG = '[role="dialog"], dialog'
const TYPING_TAGS = new Set(['INPUT', 'TEXTAREA', 'SELECT'])
const EDITABLE = '[contenteditable]:not([contenteditable="false"])'
// Composite widgets that own every key while focus is inside them. role=slider
// is deliberately absent: the seek bar consumes its own keys and lets the rest
// (Space/J/K/L/digits) through.
const KEY_OWNING_WIDGET = '[role="menu"], [role="listbox"]'
const ACTIVATABLE = 'button, a[href], summary, [role="button"], [role="menuitem"], [role="option"]'
const ACTIVATION_KEYS = new Set([' ', 'Enter'])

/** True when `target` sits in a dialog nested inside (layered over) `host`. */
export function isFromNestedDialog(target: EventTarget | null, host: Element): boolean {
  if (!(target instanceof Element)) return false
  const dialog = target.closest(DIALOG)
  return dialog !== null && dialog !== host
}

function isBrowserOrImeKey(e: KeyboardEvent): boolean {
  return e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented || e.isComposing
}

function ownsKey(target: Element, key: string, media: Element | null): boolean {
  if (target === media || TYPING_TAGS.has(target.tagName)) return true
  if (target.closest(EDITABLE) || target.closest(KEY_OWNING_WIDGET)) return true
  return ACTIVATION_KEYS.has(key) && target.matches(ACTIVATABLE)
}

function isLayeredOverPlayer(target: Element): boolean {
  const dialog = target.closest(DIALOG)
  if (dialog && !dialog.hasAttribute(PLAYER_SHELL_ATTR)) return true
  // Focus can fall back to <body> or stay on a player control while an overlay
  // is open; the overlay is still the top layer.
  const shell = target.ownerDocument.querySelector(`[${PLAYER_SHELL_ATTR}]`)
  return shell?.querySelector(DIALOG) != null
}

/**
 * Whether the player's global shortcut listener must leave this key press
 * alone: a browser/OS chord, a key someone already handled, a control that owns
 * the key, or an overlay layered over the player.
 */
export function shouldIgnorePlayerShortcut(e: KeyboardEvent, media: Element | null): boolean {
  if (isBrowserOrImeKey(e)) return true
  const target = e.target
  if (!(target instanceof Element)) return false
  return ownsKey(target, e.key, media) || isLayeredOverPlayer(target)
}
