import type { MouseEvent } from 'react'

// Helpers for "open a card in a new tab". The app mirrors playback into the URL
// (`/?play=HASH&f=IDX&t=SEC`, read by PlayerProvider on load) and routes are
// plain paths, so a fresh tab opened at the right URL reproduces the click.
//
// Cards are mostly <button>/<div> (not <a>), which do NOT open new tabs natively
// on middle-click. newTabProps adds that: middle-click and ctrl/cmd-click open
// the target in a new tab; a plain left-click runs the in-app action (SPA nav or
// open the player) exactly as before.

// openInNewTab opens a same-origin SPA URL in a background tab.
export function openInNewTab(href: string): void {
  globalThis.open(href, '_blank', 'noopener')
}

// playHref builds the player deep-link. Works for torrents (40-hex info hash)
// and local files (`local-<base64>` pseudo-hash) — both are valid `play` values.
export function playHref(hash: string, fileIdx?: number, seekSec?: number): string {
  const p = new URLSearchParams({ play: hash })
  if (fileIdx && fileIdx > 0) p.set('f', String(fileIdx))
  if (seekSec && seekSec > 0) p.set('t', String(Math.floor(seekSec)))
  return `/?${p.toString()}`
}

// searchHref builds the "seed the search" deep-link used by trending/history
// cards (`/?q=QUERY`).
export function searchHref(query: string): string {
  return `/?q=${encodeURIComponent(query)}`
}

// newTabProps returns { onClick, onAuxClick } to spread on a clickable card.
// `href` is where a new tab should open; `onActivate` is the existing in-app
// action for a plain click. Modified/middle clicks never run onActivate.
export function newTabProps(href: string, onActivate: () => void) {
  return {
    onClick: (e: MouseEvent) => {
      if (e.ctrlKey || e.metaKey || e.shiftKey) {
        e.preventDefault()
        openInNewTab(href)
        return
      }
      onActivate()
    },
    onAuxClick: (e: MouseEvent) => {
      if (e.button === 1) {
        e.preventDefault()
        openInNewTab(href)
      }
    },
  }
}
