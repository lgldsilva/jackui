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
  /** Save dialog download (á la carte). category/mediaKind enable subfolder. */
  downloadFile: (apiPath: string, suggestedName: string, category?: string, mediaKind?: string) => Promise<{
    success?: boolean
    cancelled?: boolean
    error?: string
    filePath?: string
  }>
  /** Silent download to configured folder (no dialog). Categorizes into subfolders. */
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

declare global {
  interface Window {
    electronAPI?: ElectronAPI
  }
}
