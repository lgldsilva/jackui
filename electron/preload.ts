import { contextBridge, ipcRenderer } from 'electron'

export interface StreamStatus {
  downRate: number
  upRate: number
  activeTorrents: number
}

export interface Preferences {
  downloadFolder: string
}

export interface AppVersion {
  version: string
  commit: string
  date: string
}

export interface ElectronAPI {
  /** Download a media file from the Go server. apiPath is a relative path
   *  starting with /api/... (including ?token=). Shows a save dialog.
   *  Optional category and mediaKind enable automatic subfolder sorting. */
  downloadFile: (apiPath: string, suggestedName: string, category?: string, mediaKind?: string) => Promise<{
    success?: boolean
    cancelled?: boolean
    error?: string
    filePath?: string
  }>
  /** Download directly to the configured download folder (no dialog).
   *  Uses automatic categorization when category is provided. */
  downloadFileDirect: (apiPath: string, suggestedName: string, category?: string, mediaKind?: string) => Promise<{
    success?: boolean
    error?: string
    filePath?: string
  }>
  getServerPort: () => Promise<number>
  getPlatform: () => Promise<string>
  getStreamStatus: () => Promise<StreamStatus>
  getPreferences: () => Promise<Preferences>
  setPreferences: (p: Partial<Preferences>) => Promise<Preferences>
  selectFolder: () => Promise<string | null>
  onDownloadProgress: (callback: (data: {
    filePath: string
    downloaded: number
    total: number
    done: boolean
  }) => void) => void
  onPrefsUpdated: (callback: (prefs: Preferences) => void) => void
  onNavigate: (callback: (path: string) => void) => void
  onDeepLink: (callback: (magnet: string) => void) => void
  getAutoLaunch: () => Promise<boolean>
  setAutoLaunch: (enable: boolean) => Promise<void>
  openDownloadFolder: () => Promise<void>
  showItemInFolder: (filePath: string) => Promise<void>
  getAppVersion: () => Promise<AppVersion>
}

const api: ElectronAPI = {
  downloadFile: (apiPath, suggestedName, category, mediaKind) =>
    ipcRenderer.invoke('download-file', apiPath, suggestedName, category, mediaKind),
  downloadFileDirect: (apiPath, suggestedName, category, mediaKind) =>
    ipcRenderer.invoke('download-file-direct', apiPath, suggestedName, category, mediaKind),
  getServerPort: () => ipcRenderer.invoke('get-server-port'),
  getPlatform: () => ipcRenderer.invoke('get-platform'),
  getStreamStatus: () => ipcRenderer.invoke('get-stream-status'),
  getPreferences: () => ipcRenderer.invoke('get-preferences'),
  setPreferences: (p) => ipcRenderer.invoke('set-preferences', p),
  selectFolder: () => ipcRenderer.invoke('select-folder'),
  onDownloadProgress: (callback) => {
    ipcRenderer.on('download-progress', (_e, data) => callback(data))
  },
  onPrefsUpdated: (callback) => {
    ipcRenderer.on('prefs-updated', (_e, data) => callback(data))
  },
  onNavigate: (callback) => {
    ipcRenderer.on('navigate', (_e, path) => callback(path))
  },
  onDeepLink: (callback) => {
    ipcRenderer.on('deep-link', (_e, magnet) => callback(magnet))
  },
  getAutoLaunch: () => ipcRenderer.invoke('get-auto-launch'),
  setAutoLaunch: (enable) => ipcRenderer.invoke('set-auto-launch', enable),
  openDownloadFolder: () => ipcRenderer.invoke('open-download-folder'),
  showItemInFolder: (filePath) => ipcRenderer.invoke('show-item-in-folder', filePath),
  getAppVersion: () => ipcRenderer.invoke('get-app-version'),
}

contextBridge.exposeInMainWorld('electronAPI', api)
