import type { DownloadEntry, TorrentInfo } from '../../api/client'

export type Tab = 'all' | 'downloading' | 'paused' | 'completed' | 'failed' | 'network'
// Allowed tab values for the ?tab= URL param (validated by useEnumQueryParam).
export const DOWNLOAD_TABS: readonly Tab[] = ['all', 'downloading', 'paused', 'completed', 'failed', 'network']

// Per-tab lookups shared between the page and the tab-content component.
export type TabDownloads = Record<Tab, DownloadEntry[]>
export type TabTorrents = Record<Tab, TorrentInfo[]>
