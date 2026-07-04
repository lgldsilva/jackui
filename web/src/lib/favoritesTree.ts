// Árvore de pastas + import de favoritos — extraído do FavoritesPage.tsx (móvel
// puro: types + funções puras/de I/O, sem JSX nem estado de componente).
import type { TFunction } from 'i18next'
import { FavoriteFolder, streamImport } from '../api/client'
import { errMessage } from './errMessage'

export type FolderNode = {
  folder: FavoriteFolder
  children: FolderNode[]
}

// Build a tree from a flat folder list. Each node holds its children sorted
// by position then name. Roots = nodes whose parentId is null.
export function buildTree(folders: FavoriteFolder[]): FolderNode[] {
  const byId = new Map<number, FolderNode>()
  folders.forEach(f => byId.set(f.id, { folder: f, children: [] }))
  const roots: FolderNode[] = []
  folders.forEach(f => {
    const node = byId.get(f.id)!
    if (f.parentId == null) roots.push(node)
    else {
      const parent = byId.get(f.parentId)
      if (parent) parent.children.push(node)
      else roots.push(node) // orphaned (parent deleted) → render at root
    }
  })
  return roots
}

// Achata a árvore em uma lista ordenada (DFS) com a profundidade de cada nó —
// usada pelo dropdown de pasta no mobile pra indentar visualmente as subpastas.
export function flattenTree(nodes: FolderNode[], depth = 0): { folder: FavoriteFolder; depth: number }[] {
  return nodes.flatMap(node => [
    { folder: node.folder, depth },
    ...flattenTree(node.children, depth + 1),
  ])
}

export async function importTorrentB64(files: File[], viewMode: number | null, ALL_VIEW: number): Promise<{ ok: number; fails: string[] }> {
  let ok = 0
  const fails: string[] = []
  for (const file of files) {
    try {
      const buf = await file.arrayBuffer()
      // Byte→binary-string in 32KB chunks. The old char-by-char `bin +=` was
      // O(n²) and stalled (read as "import failed") on real .torrent files.
      const bytes = new Uint8Array(buf)
      let bin = ''
      const CHUNK = 0x8000
      for (let i = 0; i < bytes.length; i += CHUNK) {
        bin += String.fromCodePoint(...bytes.subarray(i, i + CHUNK))
      }
      await streamImport({ torrentB64: btoa(bin), folderId: viewMode === ALL_VIEW ? null : viewMode })
      ok++
    } catch (e: unknown) {
      fails.push(`${file.name}: ${errMessage(e)}`)
    }
  }
  return { ok, fails }
}

export function buildImportMsg(ok: number, failCount: number, firstFail: string | undefined, suffix: string, t: TFunction): { kind: 'ok' | 'err'; text: string } {
  if (failCount === 0) {
    return { kind: 'ok', text: t('favorites.importedOk', { count: ok, suffix }) }
  }
  return { kind: 'err', text: t('favorites.importedErr', { ok, fails: failCount, first: firstFail, suffix }) }
}
