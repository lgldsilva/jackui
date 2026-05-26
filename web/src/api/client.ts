import axios from 'axios'

const api = axios.create({
  baseURL: '/api',
  headers: {
    'Content-Type': 'application/json',
  },
})

export interface SearchResult {
  title: string
  tracker: string
  categoryId: number
  category: string
  size: number
  seeders: number
  leechers: number
  age: string
  magnetUri: string
  link: string
  infoHash: string
  publishDate: string
}

export interface Indexer {
  id: string
  name: string
  description: string
  language: string
  type: string
  configured: boolean
}

export interface DownloadClient {
  id: string
  name: string
  type: string
  default: boolean
}

export interface DownloadClientFull extends DownloadClient {
  url: string
  username: string
  password: string
}

export interface JackettConfig {
  url: string
  apiKey: string
}

export interface AppConfig {
  port: number
  jackett: JackettConfig
  downloadClients: DownloadClientFull[]
}

export const searchTorrents = async (
  q: string,
  indexers: string[],
  category: string
): Promise<SearchResult[]> => {
  const params = new URLSearchParams({ q })
  if (indexers.length > 0 && indexers[0] !== 'all') {
    params.set('indexers', indexers.join(','))
  }
  if (category && category !== 'all') {
    params.set('category', category)
  }
  const { data } = await api.get<SearchResult[]>(`/search?${params}`)
  return data
}

export const getIndexers = async (): Promise<Indexer[]> => {
  const { data } = await api.get<Indexer[]>('/indexers')
  return data
}

export const getClients = async (): Promise<DownloadClient[]> => {
  const { data } = await api.get<DownloadClient[]>('/clients')
  return data
}

export const downloadTorrent = async (
  clientId: string,
  magnetUri: string,
  torrentUrl: string,
  savePath?: string
): Promise<void> => {
  await api.post('/download', { clientId, magnetUri, torrentUrl, savePath })
}

export const getConfig = async (): Promise<AppConfig> => {
  const { data } = await api.get<AppConfig>('/config')
  return data
}

export const saveConfig = async (config: AppConfig): Promise<void> => {
  await api.put('/config', config)
}

export const testJackettConnection = async (): Promise<{ success: boolean; message?: string; error?: string }> => {
  const { data } = await api.post('/config/test')
  return data
}

export default api
