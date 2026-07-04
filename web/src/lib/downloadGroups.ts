import type { DownloadEntry, TorrentInfo } from '../api/client'

// CompletedGroup bundles all files of one torrent (same infoHash). The name is
// kept generic ("Group") since groupByHash now feeds every lifecycle section
// (downloading/queued/paused), not just completed.
export type CompletedGroup = {
  key: string
  name: string
  files: DownloadEntry[]
  seeding: boolean
}

// Ordena arquivos do MESMO torrent em ordem natural (numérica) pelo caminho, pra
// que episódios fiquem S01E01, S01E02, … S01E10 em vez de ordem alfabética crua
// ou de chegada. A ordem ENTRE grupos segue o sort global (ordem de chegada da
// lista já ordenada pelo backend).
function naturalFileCompare(a: DownloadEntry, b: DownloadEntry): number {
  return (a.filePath || a.name).localeCompare(b.filePath || b.name, undefined, { numeric: true, sensitivity: 'base' })
}

// countTorrents counts DISTINCT torrents in a set of download rows. A multi-file
// torrent is N rows (one per file) but ONE torrent — the badges/counters must
// count torrents, not files, so a 778-file pack reads as 1 (matching the grouped
// card view). Hashless rows (pre-metadata) count individually, mirroring the
// backend grpKey id-fallback.
export function countTorrents(rows: readonly DownloadEntry[]): number {
  const seen = new Set<string>()
  for (const d of rows) seen.add(d.infoHash || `id:${d.id}`)
  return seen.size
}

// groupByHash groups downloads by infoHash, preserving first-seen order, with no
// status filter — so it works for ANY lifecycle section (Baixando/Fila/Pausados/
// Concluídos). A single-file torrent and a whole-torrent (-2) item each land in a
// group of one (rendered as a plain card, no wrapper). `seeding` is true when the
// torrent is still live in the streamer. Pure + exported for unit tests.
export function groupByHash(items: readonly DownloadEntry[], torrents: readonly TorrentInfo[]): CompletedGroup[] {
  const byKey = new Map<string, CompletedGroup>()
  const order: string[] = []
  for (const d of items) {
    const key = d.infoHash || `id:${d.id}`
    let g = byKey.get(key)
    if (!g) {
      g = { key, name: d.name || d.filePath, files: [], seeding: !!d.infoHash && torrents.some(t => t.infoHash === d.infoHash) }
      byKey.set(key, g)
      order.push(key)
    }
    g.files.push(d)
  }
  // Episódios em ordem dentro de cada torrent multi-arquivo.
  for (const g of byKey.values()) {
    if (g.files.length > 1) g.files.sort(naturalFileCompare)
  }
  return order.map(k => byKey.get(k) as CompletedGroup)
}

// groupCompleted is the completed-only view kept for completedViewCounts: same
// grouping, the caller pre-filters to status==='completed'.
export function groupCompleted(items: readonly DownloadEntry[], torrents: readonly TorrentInfo[]): CompletedGroup[] {
  return groupByHash(items, torrents)
}

// groupProgress aggregates a multi-file group's progress: sum(bytes_downloaded) /
// sum(file_size), clamped to [0,1]. A group with no known sizes reports 0.
export function groupProgress(g: CompletedGroup): { downloaded: number; total: number; pct: number } {
  let downloaded = 0
  let total = 0
  for (const f of g.files) {
    downloaded += f.bytesDownloaded || 0
    total += f.fileSize || 0
  }
  const pct = total > 0 ? Math.max(0, Math.min(1, downloaded / total)) * 100 : 0
  return { downloaded, total, pct }
}

// completedViewCounts derives the chip counts for the completed view: "seeding"
// = live torrents not yet on a completed row + completed groups still seeding;
// "onDisk" = completed groups whose torrent is no longer live. Pure + exported so
// the filter-chip behaviour is unit-testable without rendering the page.
export function completedViewCounts(
  downloads: readonly DownloadEntry[],
  torrents: readonly TorrentInfo[],
): { seeding: number; onDisk: number } {
  const completed = downloads.filter(d => d.status === 'completed')
  const groups = groupCompleted(completed, torrents)
  const streamingOnly = torrents.filter(t => !completed.some(d => d.infoHash === t.infoHash))
  return {
    seeding: streamingOnly.length + groups.filter(g => g.seeding).length,
    onDisk: groups.filter(g => !g.seeding).length,
  }
}
