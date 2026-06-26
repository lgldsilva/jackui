import type { DownloadEntry } from '../api/downloads'

// Filtro + ordenação dos arquivos DENTRO de um torrent multi-arquivo (o
// DownloadGroupCard expandido). Só faz sentido para grupos com 2+ arquivos —
// um torrent de arquivo único não tem o que filtrar/ordenar. Lógica pura,
// testável, fora do god-file DownloadsPage.

export type GroupFileStatusFilter = 'all' | 'active' | 'completed'
export type GroupFileSortKey = 'name' | 'size' | 'progress'
export type GroupFileSortDir = 'asc' | 'desc'

const naturalCmp = (a: string, b: string) =>
  a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' })

// 'active' = tudo que ainda não concluiu (downloading/queued/paused/failed/
// moving); 'completed' = só os finalizados. Cobre o pedido "listar só os que
// estão baixando" vs "só os concluídos".
function matchesStatus(d: DownloadEntry, f: GroupFileStatusFilter): boolean {
  if (f === 'all') return true
  if (f === 'completed') return d.status === 'completed'
  return d.status !== 'completed'
}

// Progresso por arquivo em [0,1]: concluído = 1; senão bytes/size (ou o
// progress do backend quando o tamanho é desconhecido).
function fileProgress(d: DownloadEntry): number {
  if (d.status === 'completed') return 1
  if (d.fileSize > 0) return Math.min(1, d.bytesDownloaded / d.fileSize)
  return d.progress || 0
}

// viewGroupFiles filtra por status e ordena por nome/tamanho/progresso. Estável
// nos empates (preserva a ordem natural de chegada). Retorna um array NOVO.
export function viewGroupFiles(
  files: readonly DownloadEntry[],
  statusFilter: GroupFileStatusFilter,
  sortKey: GroupFileSortKey,
  sortDir: GroupFileSortDir,
): DownloadEntry[] {
  const sign = sortDir === 'asc' ? 1 : -1
  return files
    .filter((d) => matchesStatus(d, statusFilter))
    .map((d, i) => ({ d, i }))
    .sort((a, b) => {
      let r = 0
      if (sortKey === 'name') r = naturalCmp(a.d.filePath || a.d.name, b.d.filePath || b.d.name)
      else if (sortKey === 'size') r = a.d.fileSize - b.d.fileSize
      else r = fileProgress(a.d) - fileProgress(b.d)
      if (r === 0) return a.i - b.i // estável
      return sign * r
    })
    .map((x) => x.d)
}

// groupStatusCounts alimenta os badges da barra (Todos N · Baixando N · Concluído N).
export function groupStatusCounts(
  files: readonly DownloadEntry[],
): { all: number; active: number; completed: number } {
  let completed = 0
  for (const d of files) if (d.status === 'completed') completed++
  return { all: files.length, active: files.length - completed, completed }
}
