import { api } from './http'
import type { TorrentInfo, PromotePreviewEntry, StreamFile } from './client'

// ─── Background downloads ──────────────────────────────────────────────────
// Full-file (not streaming) download queue. Backed by anacrolix file.Download
// which prioritises all pieces; protected from cache eviction until removed.

export type DownloadEntry = {
  id: number
  userId: number
  username?: string
  infoHash: string
  fileIndex: number
  filePath: string
  fileSize: number
  name: string
  magnet: string
  tracker?: string
  category?: string
  status: 'queued' | 'downloading' | 'moving' | 'completed' | 'failed' | 'paused'
  bytesDownloaded: number
  progress: number
  downRate?: number
  upRate?: number     // bytes/sec de upload (seeding), preenchido pelo backend
  seeders?: number    // seeders ao vivo do swarm, preenchido pelo backend
  eta?: number
  startedAt?: string | null
  completedAt?: string | null
  error?: string
  createdAt: string
  promoted?: boolean   // true when file was moved outside the download dir
  // Queue scheduling
  priority?: 'high' | 'normal' | 'low'
  stalls?: number          // times demoted for no-seed
  queuePosition?: number   // 1-based rank among queued rows (0 = not queued)
}

export type DownloadPriority = 'high' | 'normal' | 'low'

export type DownloadsQueueSettings = {
  maxActive: number
  perUserMaxActive: number
  stallThresholdMin: number
  maxStalls: number
  agingStepMin: number
  agingCap: number
  rotationEnabled: boolean
  autoPromoteArr: boolean
  // Modo de concorrência das cópias de promover/mover: 'auto' (detecta HDD/SSD),
  // 'serial' (uma por vez) ou 'parallel' (sempre paralelo).
  transferConcurrencyMode: 'auto' | 'serial' | 'parallel'
}

// One known source (magnet) for a download — the original + alternatives found
// via Jackett re-search (Phase 2 source rotation).
export type DownloadSource = {
  id: number
  downloadId: number
  infoHash: string
  title: string
  tracker: string
  seeders: number
  size: number
  status: 'active' | 'candidate' | 'cooldown' | 'failed'
  tries: number
  lastTried?: string | null
  createdAt: string
}

// WHOLE_TORRENT_FILE_INDEX espelha downloads.FileIndexWholeTorrent no backend:
// UMA linha na fila que baixa o torrent INTEIRO (progresso agregado, conclusão
// move todos os arquivos preservando a estrutura). -1 já significa "auto-pick"
// (shim Transmission RPC), por isso -2.
export const WHOLE_TORRENT_FILE_INDEX = -2

export type DownloadCreateParams = {
  infoHash: string
  fileIndex: number
  magnet: string
  name: string
  filePath: string
  fileSize: number
  tracker?: string
  category?: string
  destBase?: string   // chosen destination (#16); empty = default download dir
  destSubdir?: string // optional subfolder under destBase
}

// DownloadDestination is a writable target the user may pick for a download:
// a mount they're allowed to see, or a promote destination.
export type DownloadDestination = {
  name: string
  path: string
  userSubpath?: boolean
}

export const downloadDestinations = async (): Promise<DownloadDestination[]> => {
  const { data } = await api.get<DownloadDestination[]>('/downloads/destinations')
  return data || []
}

export const downloadDestBrowse = async (base: string, path: string): Promise<{ dirs: string[]; path: string }> => {
  const query = new URLSearchParams({ base, path })
  const { data } = await api.get<{ dirs: string[]; path: string }>(`/downloads/dest/browse?${query.toString()}`)
  return data
}

export type DownloadFilterParams = {
  status?: string
  tracker?: string
  category?: string
  search?: string
  sort?: string
  order?: string
  userId?: string
}

export type DownloadUserEntry = {
  id: number
  username: string
}

export const downloadsListAll = async (params: DownloadFilterParams): Promise<DownloadEntry[]> => {
  const query = new URLSearchParams()
  if (params.status) query.set('status', params.status)
  if (params.tracker) query.set('tracker', params.tracker)
  if (params.category) query.set('category', params.category)
  if (params.search) query.set('search', params.search)
  if (params.sort) query.set('sort', params.sort)
  if (params.order) query.set('order', params.order)
  if (params.userId) query.set('userId', params.userId)
  const { data } = await api.get<DownloadEntry[]>(`/downloads/all?${query.toString()}`)
  return data || []
}

export const downloadUsers = async (): Promise<DownloadUserEntry[]> => {
  const { data } = await api.get<DownloadUserEntry[]>('/downloads/users')
  return data || []
}

export const downloadsList = async (): Promise<DownloadEntry[]> => {
  const { data } = await api.get<DownloadEntry[]>('/downloads')
  return data || []
}

export const downloadCreate = async (params: DownloadCreateParams): Promise<DownloadEntry> => {
  const { data } = await api.post<DownloadEntry>('/downloads', params)
  return data
}

// Um arquivo do torrent dentro de um enqueue batch. Só os campos por-arquivo;
// infoHash/magnet/name/tracker/category/destino são compartilhados no corpo.
export type BatchFile = {
  fileIndex: number
  filePath: string
  fileSize: number
}

export type DownloadBatchCreateParams = {
  infoHash: string
  magnet: string
  name: string
  tracker?: string
  category?: string
  destBase?: string
  destSubdir?: string
  files: BatchFile[]
}

export type DownloadBatchCreateResult = {
  created: DownloadEntry[]
  requeued: number
}

// buildBatchFiles mapeia os arquivos escolhidos (StreamFile do preview) para o
// formato por-arquivo do corpo do batch. Função PURA — testável sem rede.
export function buildBatchFiles(picks: readonly StreamFile[]): BatchFile[] {
  return picks.map(f => ({ fileIndex: f.index, filePath: f.path, fileSize: f.size }))
}

// isWholeTorrentSelection: true quando TODOS os arquivos do torrent estão
// marcados. Nesse caso enfileira-se UMA linha "torrent inteiro" (fileIndex=-2,
// file priorities do anacrolix) em vez de N linhas por-arquivo — um pack de 778
// arquivos vira 1 linha (fim da explosão que inflava a lista e o /api/downloads).
// Subconjunto (o usuário desmarcou algo) continua batch, preservando a
// granularidade. Função PURA — testável sem rede.
export function isWholeTorrentSelection(
  files: readonly StreamFile[],
  selected: ReadonlySet<number>,
): boolean {
  return files.length > 0 && files.every(f => selected.has(f.index))
}

// downloadBatchCreate enfileira N arquivos de UM torrent numa ÚNICA request
// (substitui o Promise.allSettled de 1 POST por arquivo). O backend resolve o
// destino uma vez e insere tudo numa transação (tudo-ou-nada, idempotente).
export const downloadBatchCreate = async (
  params: DownloadBatchCreateParams,
): Promise<DownloadBatchCreateResult> => {
  const { data } = await api.post<DownloadBatchCreateResult>('/downloads/batch', params)
  return data
}

export const downloadDelete = async (id: number): Promise<void> => {
  await api.delete(`/downloads/${id}`)
}

export const downloadPause = async (id: number): Promise<void> => {
  await api.patch(`/downloads/${id}/pause`)
}

export const downloadResume = async (id: number): Promise<void> => {
  await api.patch(`/downloads/${id}/resume`)
}

export const downloadSetPriority = async (id: number, priority: DownloadPriority): Promise<void> => {
  await api.patch(`/downloads/${id}/priority`, { priority })
}

export const getDownloadsQueueSettings = async (): Promise<DownloadsQueueSettings> => {
  const { data } = await api.get<DownloadsQueueSettings>('/downloads/settings')
  return data
}

export const downloadSources = async (id: number): Promise<DownloadSource[]> => {
  const { data } = await api.get<DownloadSource[]>(`/downloads/${id}/sources`)
  return data || []
}

export const updateDownloadsQueueSettings = async (
  s: DownloadsQueueSettings,
): Promise<{ restartRequired: boolean }> => {
  const { data } = await api.put<{ restartRequired: boolean }>('/downloads/settings', s)
  return data
}

// downloadRecheck força um "Force Recheck" (estilo qBittorrent) — re-hasha
// todos os pieces do arquivo no disco e reseta bytes_downloaded pro worker
// reconciliar depois. UI mostra spinner enquanto o backend processa
// (chamada retorna assim que o hash check inicia; o progresso aparece
// no próximo tick do worker).
export const downloadsListFiltered = async (params: DownloadFilterParams): Promise<DownloadEntry[]> => {
  const query = new URLSearchParams()
  if (params.status) query.set('status', params.status)
  if (params.tracker) query.set('tracker', params.tracker)
  if (params.category) query.set('category', params.category)
  if (params.search) query.set('search', params.search)
  if (params.sort) query.set('sort', params.sort)
  if (params.order) query.set('order', params.order)
  const { data } = await api.get<DownloadEntry[]>(`/downloads/filtered?${query.toString()}`)
  return data || []
}

export const downloadPauseAll = async (): Promise<{ affected: number }> => {
  const { data } = await api.patch<{ affected: number }>('/downloads/pause-all')
  return data
}

export const downloadResumeAll = async (): Promise<{ affected: number }> => {
  const { data } = await api.patch<{ affected: number }>('/downloads/resume-all')
  return data
}

export const downloadBatchPause = async (ids: number[]): Promise<{ affected: number }> => {
  const { data } = await api.patch<{ affected: number }>('/downloads/batch/pause', { ids })
  return data
}

export const downloadBatchResume = async (ids: number[]): Promise<{ affected: number }> => {
  const { data } = await api.patch<{ affected: number }>('/downloads/batch/resume', { ids })
  return data
}

export const downloadBatchDelete = async (
  ids: number[],
): Promise<{ deleted: number; total: number; failed?: number[] }> => {
  const { data } = await api.post<{ deleted: number; total: number; failed?: number[] }>(
    '/downloads/batch/delete',
    { ids },
  )
  return data
}

export const downloadTrackers = async (): Promise<string[]> => {
  const { data } = await api.get<string[]>('/downloads/trackers')
  return data || []
}

export const downloadCategories = async (): Promise<string[]> => {
  const { data } = await api.get<string[]>('/downloads/categories')
  return data || []
}

export const downloadRecheck = async (id: number): Promise<DownloadEntry> => {
  const { data } = await api.post<DownloadEntry>(`/downloads/${id}/recheck`)
  return data
}

// DownloadDetails: row do download + lista completa de arquivos do torrent
// + sizes reais (sparse vs apparent). Backend só preenche torrent quando o
// info_hash está active no streamer; null quando dropado (post-completed
// sem seed).
export type DownloadDetails = {
  download: DownloadEntry
  file: { apparent: number; onDisk: number; exists: boolean }
  torrent: TorrentInfo | null
}
export const downloadDetails = async (id: number): Promise<DownloadDetails> => {
  const { data } = await api.get<DownloadDetails>(`/downloads/${id}/details`)
  return data
}

// PeerInfo: um peer conectado do torrent. `availability` é a fração (0..1) das
// peças que o peer tem. `sending`/`receiving` são INFERIDOS das taxas (a lib
// anacrolix não expõe choke/interest). `addr` pode repetir entre polls.
export type PeerInfo = {
  addr: string
  client?: string
  network?: string
  availability: number
  downRate: number
  upRate: number
  downloaded: number
  uploaded: number
  isSeeder: boolean
  receiving: boolean
  sending: boolean
  encrypted?: boolean
}

// DownloadPeers: snapshot ao vivo dos peers. `active=false` quando o torrent não
// está carregado no streamer (foi dropado / nunca aberto) — peers vem vazio.
export type DownloadPeers = {
  peers: PeerInfo[]
  active: boolean
}
export const downloadPeers = async (id: number): Promise<DownloadPeers> => {
  const { data } = await api.get<DownloadPeers>(`/downloads/${id}/peers`)
  return data
}

export type PromoteDestination = {
  name: string
  path: string
}

// Move um download concluído para o diretório compartilhado (JACKUI_SHARED_DIR
// no servidor) ou outro destino (targetBase), opcionalmente numa subpasta. Após
// mover, opcionalmente continua seedando (keepSeeding=true).
export const downloadPromote = async (
  id: number,
  opts: { keepSeeding: boolean; targetSubdir?: string; targetBase?: string },
): Promise<DownloadEntry> => {
  const { data } = await api.post<DownloadEntry>(`/downloads/${id}/promote`, opts)
  return data
}

// Promove N downloads pra mesma subpasta de destino. Falhas individuais não
// abortam o batch; retorna { promoted, failed }.
export type PromoteBatchResult = {
  promoted: DownloadEntry[]
  failed: { id: number; error: string }[]
}
export const downloadPromoteBatch = async (
  ids: number[],
  opts: { keepSeeding: boolean; targetSubdir?: string; targetBase?: string; renameIA?: boolean },
): Promise<PromoteBatchResult> => {
  const { data } = await api.post<PromoteBatchResult>('/downloads/promote', { ids, ...opts })
  return data
}

export const downloadPromotePreview = async (
  ids: number[],
  opts: { targetSubdir?: string; targetBase?: string },
): Promise<{ previews: PromotePreviewEntry[] }> => {
  const { data } = await api.post<{ previews: PromotePreviewEntry[] }>('/downloads/promote/preview', {
    ids,
    ...opts,
  })
  // Go nil slice serializes as JSON null; the UI does previews.length → guard.
  return { previews: data?.previews ?? [] }
}

// Lista subpastas no {base}/<path> pra alimentar o navegador da PromoteModal.
// base vazio = sharedDir (default).
export const downloadPromoteBrowse = async (path: string, base?: string): Promise<{ dirs: string[]; path: string }> => {
  const params = new URLSearchParams({ path })
  if (base) params.set('base', base)
  const { data } = await api.get<{ dirs: string[]; path: string }>(
    `/downloads/promote/browse?${params}`,
  )
  // A subpasta-folha (sem subdirs) volta com dirs=null (nil slice no Go); o
  // navegador faz dirs.length → null crasha. Normaliza para [] sempre.
  return { dirs: data?.dirs ?? [], path: data?.path ?? path }
}

// Lista destinos de promoção disponíveis (nome + path).
export const fetchPromoteDestinations = async (): Promise<PromoteDestination[]> => {
  const { data } = await api.get<PromoteDestination[]>('/promote/destinations')
  return data ?? []
}

// Para de seedar sem mover o arquivo.
export const downloadStopSeed = async (id: number): Promise<void> => {
  await api.post(`/downloads/${id}/stop-seed`)
}
