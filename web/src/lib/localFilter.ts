import type { LocalEntry } from '../api/client'

// Filtro de STATUS na lista de arquivos local: mostrar tudo, só o que está
// baixando (entradas .part / pastas que contêm um .part → e.incomplete), ou só
// o concluído. Aplica a arquivos E pastas, mas só quando ≠ 'all' — no default a
// navegação por pastas permanece livre (não filtrada).
export type LocalStatusFilter = 'all' | 'downloading' | 'done'

export function matchesEntryStatus(e: LocalEntry, f: LocalStatusFilter): boolean {
  if (f === 'all') return true
  return f === 'downloading' ? !!e.incomplete : !e.incomplete
}
