// Snapshot da playlist ativa, persistido em localStorage. A URL só carrega o
// ITEM atual (?play=hash&f=idx) — ao recarregar/reabrir o app, o player restaurava
// só esse item via playSingle e PERDIA a lista (anteriores/próximos) e a posição.
// Aqui guardamos a lista inteira + qual item tocava, pra reabrir restaurando o
// contexto completo da playlist (prev/next funcionando).
import { load, save, remove } from '../../lib/storage'
import { isIncognito } from '../../lib/incognito'
import type { PlaylistItem } from '../../api/client'

const KEY = 'player.playlistSnapshot'
// Não ressuscitar playlists muito antigas: um deep-link cujo hash por acaso bata
// com um item de uma sessão de semanas atrás não deve reabrir aquela lista.
const TTL_MS = 7 * 24 * 60 * 60 * 1000 // 7 dias

export type PlaylistSnapshot = {
  readonly name: string
  readonly items: readonly PlaylistItem[]
  // Índice (na array `items` original, não no `order` embaralhado) do item que
  // estava tocando — vira o startIndex ao restaurar.
  readonly currentItemIndex: number
  readonly savedAt: number
}

export function savePlaylistSnapshot(name: string, items: readonly PlaylistItem[], currentItemIndex: number): void {
  if (items.length === 0) return
  // Never persist playlists while incognito — that would leave titles/magnets
  // in localStorage after the session ends (and after logout for the next user).
  if (isIncognito()) return
  const snap: PlaylistSnapshot = { name, items, currentItemIndex, savedAt: Date.now() }
  save(KEY, snap)
}

export function loadPlaylistSnapshot(): PlaylistSnapshot | null {
  const snap = load<PlaylistSnapshot | null>(KEY, null)
  if (!snap || !Array.isArray(snap.items) || snap.items.length === 0) return null
  if (typeof snap.savedAt !== 'number' || Date.now() - snap.savedAt > TTL_MS) return null
  return snap
}

export function clearPlaylistSnapshot(): void {
  remove(KEY)
}

// Acha o índice do item da playlist que corresponde a um info_hash de deep-link.
// Retorna -1 quando o hash não pertence à playlist salva (→ cai no play single).
export function snapshotIndexOfHash(snap: PlaylistSnapshot, hash: string): number {
  return snap.items.findIndex(it => it.infoHash === hash)
}
