import { app, BrowserWindow, Tray, Menu, ipcMain, dialog, net, nativeImage, Notification, shell } from 'electron'
import { spawn, ChildProcess } from 'child_process'
import { createWriteStream, existsSync, readFileSync, writeFileSync, mkdirSync } from 'fs'
import { join, dirname } from 'path'
import { fileURLToPath } from 'url'
import * as http from 'http'

const __filename = fileURLToPath(import.meta.url)
const __dirname = dirname(__filename)

// Disable GPU acceleration — fixes "Network service crashed" + "GPU process exited"
// on macOS when spawning a detached child process.
app.disableHardwareAcceleration()

const isDev = process.argv.includes('--dev')
const PORT_KEY = 'JACKUI_PORT'
let mainWindow: BrowserWindow | null = null
let tray: Tray | null = null
let goProc: ChildProcess | null = null
let serverPort = isDev ? 8989 : 0
let goReady = isDev
let isQuitting = false

// ─── Version Info ─────────────────────────────────────────────────────────────

interface AppVersion {
  version: string
  commit: string
  date: string
}

function loadVersion(): AppVersion {
  try {
    const p = isDev
      ? join(__dirname, 'version.json')
      : join(process.resourcesPath, 'version.json')
    const raw = readFileSync(p, 'utf-8')
    return JSON.parse(raw)
  } catch {
    return { version: '0.1.0', commit: 'dev', date: new Date().toISOString() }
  }
}

const appVersion = loadVersion()

// ─── Types ──────────────────────────────────────────────────────────────────

interface StreamRate {
  downRate: number
  upRate: number
  activeTorrents: number
}

interface ActiveTorrent {
  infoHash: string
  name: string
  progress: number
  status?: 'downloading' | 'paused' | 'seeding' | 'complete'
  downRate: number
  upRate: number
  seeders: number
  peers: number
}

interface Preferences {
  downloadFolder: string
}

// ─── State ──────────────────────────────────────────────────────────────────

let currentRate: StreamRate = { downRate: 0, upRate: 0, activeTorrents: 0 }
let activeTorrents: ActiveTorrent[] = []
let pollTimer: ReturnType<typeof setInterval> | null = null
let prefs: Preferences = { downloadFolder: '' }
let pollFailures = 0 // consecutive failures → red icon

// ─── Preferences ────────────────────────────────────────────────────────────

function prefsPath(): string {
  const dir = app.getPath('userData')
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true })
  return join(dir, 'preferences.json')
}

function loadPrefs(): void {
  try {
    const raw = readFileSync(prefsPath(), 'utf-8')
    prefs = { ...prefs, ...JSON.parse(raw) }
  } catch { /* use defaults */ }
}

function savePrefs(): void {
  try {
    writeFileSync(prefsPath(), JSON.stringify(prefs, null, 2))
  } catch (e) {
    console.error('[electron] failed to save prefs:', e)
  }
}

// ─── Port Discovery ─────────────────────────────────────────────────────────

function findFreePort(): Promise<number> {
  return new Promise((resolve) => {
    const srv = http.createServer()
    srv.listen(0, '127.0.0.1', () => {
      const port = (srv.address() as any).port
      srv.close(() => resolve(port))
    })
  })
}

function goBinaryPath(): string {
  if (isDev) return ''
  const platform = process.platform
  const ext = platform === 'win32' ? '.exe' : ''
  const paths = [
    join(process.resourcesPath, 'jackui-server' + ext),
    join(process.resourcesPath, 'bin', 'jackui-server' + ext),
  ]
  for (const p of paths) {
    if (existsSync(p)) return p
  }
  return ''
}

async function startGoServer(): Promise<number> {
  if (isDev) return 8989

  const port = await findFreePort()
  const bin = goBinaryPath()
  console.log(`[electron] spawning ${bin} on port ${port}`)
  goProc = spawn(bin, [], {
    env: { ...process.env, [PORT_KEY]: String(port) },
    // ignore stdio — we only need the process alive, logs go to Go's own stderr
    stdio: ['ignore', 'ignore', 'ignore'],
    cwd: process.resourcesPath,
    detached: true,
  })
  goProc.unref() // don't keep the event loop alive for the child alone

  // Mark ready immediately — the process is alive, the server will bind shortly
  goReady = true
  serverPort = port
  console.log(`[electron] Go server ready on :${port}`)

  goProc.on('exit', (code) => {
    console.log(`[electron] Go server exited (code=${code})`)
    goProc = null
    goReady = false
  })

  // Also set ready quickly — the stderr-based detection is a backstop.
  // Give Go 2s to start, then assume it's OK if the process is alive.
  await new Promise((r) => setTimeout(r, 2000))
  if (!goReady) {
    // Process still alive? Mark as ready anyway; the server might have started
    // on the port before we got the log line.
    const alive = goProc && !goProc.killed && goProc.exitCode === null
    if (alive) {
      goReady = true
      serverPort = port
      console.log(`[electron] Go assumed ready on :${port}`)
    }
  }
  if (!goReady) {
    throw new Error('Go server failed to start')
  }
  return port
}

// ─── Deep Links ──────────────────────────────────────────────────────────────

// Register magnet: protocol so clicking a magnet link opens in JackUI.
// In production the protocol is registered during installation (via
// electron-builder's protocol config). In dev we register at runtime.
if (!app.isPackaged) {
  app.setAsDefaultProtocolClient('magnet')
  app.setAsDefaultProtocolClient('jackui')
}

// macOS sends open-url events, Windows/Linux use the second-instance protocol.
app.on('open-url', (_e, url) => {
  handleDeepLink(url)
})

const gotTheLock = app.requestSingleInstanceLock()
if (!gotTheLock) {
  app.quit()
} else {
  app.on('second-instance', (_e, argv) => {
    // Windows/Linux: the second instance receives the URL as a command-line arg
    const url = argv.find((a) => a.startsWith('magnet:') || a.startsWith('jackui://'))
    if (url) handleDeepLink(url)
    // Show the existing window
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore()
      mainWindow.show()
    }
  })
}

function handleDeepLink(url: string): void {
  if (url.startsWith('magnet:') || url.startsWith('jackui://')) {
    const magnet = url.startsWith('jackui://')
      ? url.replace(/^jackui:\/\//, 'magnet:')
      : url
    mainWindow?.show()
    mainWindow?.webContents.send('deep-link', magnet)
    mainWindow?.focus()
  }
}

// ─── App Menu ────────────────────────────────────────────────────────────────

function buildAppMenu(): void {
  const isMac = process.platform === 'darwin'
  const template: Electron.MenuItemConstructorOptions[] = [
    ...(isMac
      ? [{
          label: app.name,
          submenu: [
            {
              label: `Sobre JackUI`,
              click: () => showAboutDialog(),
            } as Electron.MenuItemConstructorOptions,
            { type: 'separator' as const },
            { role: 'hide' as const },
            { role: 'hideOthers' as const },
            { role: 'unhide' as const },
            { type: 'separator' as const },
            { role: 'quit' as const },
          ] as Electron.MenuItemConstructorOptions[],
        }]
      : []),
    {
      label: 'File',
      submenu: [
        {
          label: 'Preferences…',
          accelerator: 'CmdOrCtrl+,',
          click: () => {
            mainWindow?.show()
            mainWindow?.webContents.send('navigate', '/settings')
          },
        } as Electron.MenuItemConstructorOptions,
        { type: 'separator' as const },
        isMac ? ({ role: 'close' as const } as Electron.MenuItemConstructorOptions) : ({ role: 'quit' as const } as Electron.MenuItemConstructorOptions),
      ] as Electron.MenuItemConstructorOptions[],
    },
    {
      label: 'Edit',
      submenu: [
        { role: 'undo' as const },
        { role: 'redo' as const },
        { type: 'separator' as const },
        { role: 'cut' as const },
        { role: 'copy' as const },
        { role: 'paste' as const },
        { role: 'selectAll' as const },
      ] as Electron.MenuItemConstructorOptions[],
    },
    {
      label: 'View',
      submenu: [
        { role: 'reload' as const },
        { role: 'forceReload' as const },
        { role: 'toggleDevTools' as const },
        { type: 'separator' as const },
        { role: 'resetZoom' as const },
        { role: 'zoomIn' as const },
        { role: 'zoomOut' as const },
        { type: 'separator' as const },
        { role: 'togglefullscreen' as const },
      ] as Electron.MenuItemConstructorOptions[],
    },
    {
      label: 'Window',
      submenu: [
        { role: 'minimize' as const },
        { role: 'zoom' as const },
        ...(isMac
          ? [
              { type: 'separator' as const } as Electron.MenuItemConstructorOptions,
              { role: 'front' as const } as Electron.MenuItemConstructorOptions,
              { type: 'separator' as const } as Electron.MenuItemConstructorOptions,
              { role: 'window' as const } as Electron.MenuItemConstructorOptions,
            ]
          : [{ role: 'close' as const } as Electron.MenuItemConstructorOptions]),
      ] as Electron.MenuItemConstructorOptions[],
    },
    {
      role: 'help' as const,
      submenu: [
        {
          label: 'JackUI',
          click: () => { /* placeholder */ },
        } as Electron.MenuItemConstructorOptions,
      ] as Electron.MenuItemConstructorOptions[],
    },
  ]
  Menu.setApplicationMenu(Menu.buildFromTemplate(template))
}

// ─── Window ─────────────────────────────────────────────────────────────────

function createWindow(): void {
  const url = `http://localhost:${serverPort}`
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 900,
    minHeight: 600,
    title: 'JackUI',
    webPreferences: {
      preload: join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
    show: false,
  })

  mainWindow.loadURL(url)

  mainWindow.once('ready-to-show', () => {
    mainWindow?.show()
  })

  mainWindow.on('close', (e) => {
    if (!isQuitting) {
      e.preventDefault()
      mainWindow?.hide()
      // Hide dock icon on macOS when minimized to tray
      if (process.platform === 'darwin' && app.dock?.hide) {
        app.dock.hide()
      }
    }
  })

  mainWindow.on('show', () => {
    if (process.platform === 'darwin' && app.dock?.show) {
      app.dock.show()
    }
  })
}

// ─── Tray ───────────────────────────────────────────────────────────────────

function formatSpeed(bytesPerSec: number): string {
  if (bytesPerSec === 0) return '0 B/s'
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s']
  const i = Math.floor(Math.log(bytesPerSec) / Math.log(1024))
  const v = bytesPerSec / Math.pow(1024, i)
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

function trayIconColor(): string {
  if (serverPort === 0 || !goReady || pollFailures >= 3) return '#ef4444' // red
  if (currentRate.activeTorrents === 0) return '#9ca3af' // gray — idle
  const hasDownload = activeTorrents.some((t) => t.downRate > 0)
  if (hasDownload) return '#22c55e' // green — active
  return '#f59e0b' // amber — seeding only
}

function createTrayIcon(): Electron.NativeImage {
  const color = trayIconColor()
  const r = parseInt(color.slice(1, 3), 16)
  const g = parseInt(color.slice(3, 5), 16)
  const b = parseInt(color.slice(5, 7), 16)

  // 20×20 RGBA buffer — círculo branco com ponto colorido.
  // Anti-aliasing suave via alpha blending nas bordas.
  const S = 20, cx = S / 2, cy = S / 2
  const outerR = 8.5, innerR = 5, borderR = 6.5
  const buf = Buffer.alloc(S * S * 4, 0)

  for (let y = 0; y < S; y++) {
    for (let x = 0; x < S; x++) {
      const dx = x - cx + 0.5, dy = y - cy + 0.5
      const dist = Math.sqrt(dx * dx + dy * dy)
      if (dist > outerR + 0.5) continue
      const i = (y * S + x) * 4

      if (dist < innerR) {
        // Colored dot
        const alpha = Math.min(1, Math.max(0, (innerR - dist + 0.5)))
        buf[i] = r; buf[i + 1] = g; buf[i + 2] = b
        buf[i + 3] = Math.round(alpha * 255)
      } else if (dist < borderR) {
        // White ring between dot and outer edge
        const edge = Math.abs(dist - borderR)
        const alpha = Math.min(1, Math.max(0, 1 - edge + 0.5))
        const brightness = 220 + Math.round(35 * (1 - alpha))
        buf[i] = brightness; buf[i + 1] = brightness; buf[i + 2] = brightness
        buf[i + 3] = Math.round(alpha * 255)
      } else {
        // Outer edge — anti-aliased white
        const edge = dist - (outerR - 0.5)
        const alpha = Math.min(1, Math.max(0, 1 - edge))
        const brightness = 220 + Math.round(35 * (1 - alpha))
        buf[i] = brightness; buf[i + 1] = brightness; buf[i + 2] = brightness
        buf[i + 3] = Math.round(alpha * 255)
      }
    }
  }

  return nativeImage.createFromBuffer(buf, { width: S, height: S })
}

function buildTrayTooltip(): string {
  if (serverPort === 0 || !goReady) return 'JackUI — disconnected'
  const { activeTorrents: n, downRate, upRate } = currentRate
  const lines = [`JackUI — ${n} torrent(s) ativo(s)`]
  if (n > 0) {
    lines.push(`▼ ${formatSpeed(downRate)}`)
    lines.push(`▲ ${formatSpeed(upRate)}`)
  }
  const seeding = activeTorrents.filter((t) => t.downRate === 0 && t.upRate > 0).length
  const downloading = activeTorrents.filter((t) => t.downRate > 0).length
  if (downloading > 0) lines.push(`⬇ ${downloading} baixando`)
  if (seeding > 0) lines.push(`⬆ ${seeding} semeando`)
  return lines.join('\n')
}

function buildTrayMenu(): Electron.Menu {
  const items: Electron.MenuItemConstructorOptions[] = []

  // Status section
  if (serverPort > 0 && goReady) {
    const { activeTorrents: n, downRate, upRate } = currentRate
    items.push({
      label: `Torrents: ${n} ativo(s)`,
      enabled: false,
    })
    items.push({
      label: `▼ Download: ${formatSpeed(downRate)}`,
      enabled: false,
    })
    items.push({
      label: `▲ Upload: ${formatSpeed(upRate)}`,
      enabled: false,
    })
    if (n > 0) {
      const top = activeTorrents.slice(0, 5)
      items.push({ type: 'separator' })
      for (const t of top) {
        const icon = t.downRate > 0 ? '⬇' : t.upRate > 0 ? '⬆' : '⏸'
        items.push({
          label: `${icon} ${t.name.length > 40 ? t.name.slice(0, 40) + '…' : t.name}`,
          enabled: false,
        })
        if (t.downRate > 0 || t.upRate > 0) {
          items.push({
            label: `   ▼ ${formatSpeed(t.downRate)}  ▲ ${formatSpeed(t.upRate)}  ${Math.round(t.progress * 100)}%`,
            enabled: false,
          })
        }
      }
      if (activeTorrents.length > 5) {
        items.push({
          label: `… e mais ${activeTorrents.length - 5}`,
          enabled: false,
        })
      }
    }
  } else {
    items.push({ label: 'Desconectado', enabled: false })
  }

  items.push({ type: 'separator' })

  // Preferences
  items.push({
    label: 'Preferências…',
    click: () => {
      mainWindow?.show()
      mainWindow?.webContents.send('navigate', '/settings')
    },
  })

  items.push({
    label: 'Selecionar pasta de downloads…',
    click: async () => {
      const result = await dialog.showOpenDialog(mainWindow!, {
        properties: ['openDirectory'],
        defaultPath: prefs.downloadFolder || undefined,
      })
      if (!result.canceled && result.filePaths[0]) {
        prefs.downloadFolder = result.filePaths[0]
        savePrefs()
        mainWindow?.webContents.send('prefs-updated', { ...prefs })
      }
    },
  })

  items.push({ type: 'separator' })

  items.push({
    label: 'Mostrar JackUI',
    click: () => {
      mainWindow?.show()
      mainWindow?.focus()
    },
  })

  items.push({
    label: 'Sair',
    click: () => {
      isQuitting = true
      app.quit()
    },
  })

  return Menu.buildFromTemplate(items)
}

function createTray(): void {
  tray = new Tray(createTrayIcon())
  tray.setToolTip('JackUI — iniciando…')
  tray.setContextMenu(buildTrayMenu())
  tray.on('double-click', () => {
    mainWindow?.show()
    mainWindow?.focus()
  })
  updateTray()
}

function updateTray(): void {
  if (!tray) return
  try {
    tray.setImage(createTrayIcon())
    tray.setToolTip(buildTrayTooltip())
    tray.setContextMenu(buildTrayMenu())
  } catch { /* silent */ }
}

// ─── Polling ────────────────────────────────────────────────────────────────

async function fetchJSON<T>(path: string): Promise<T | null> {
  try {
    const url = `http://localhost:${serverPort}${path}`
    const resp = await net.fetch(url, { method: 'GET' })
    if (!resp.ok) return null
    return await resp.json() as T
  } catch {
    return null
  }
}

async function pollStatus(): Promise<void> {
  if (serverPort === 0 || !goReady) return

  const [rate, active] = await Promise.all([
    fetchJSON<StreamRate>('/api/stream/rate'),
    fetchJSON<ActiveTorrent[]>('/api/stream/active'),
  ])
  if (rate) {
    currentRate = rate
    pollFailures = 0
  } else {
    pollFailures++
  }
  if (active) activeTorrents = active

  updateTray()
}

function startPolling(): void {
  pollTimer = setInterval(pollStatus, 2000)
  pollStatus() // immediate first poll
}

function stopPolling(): void {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function resolveCategoryFolder(category: string, mediaKind: string): string {
  const cat = (category || mediaKind || '').toLowerCase()
  if (/movie|film|cinema|4k.*uhd|uhd.*4k/i.test(cat)) return 'Movies'
  if (/tv|series|episode|season|show|anime/i.test(cat)) return 'TV'
  if (/music|audio|song|album|flac|mp3/i.test(cat)) return 'Music'
  if (/book|ebook|pdf|mobi|epub/i.test(cat)) return 'Books'
  if (/game|ps4|ps5|xbox|switch|pc.*game/i.test(cat)) return 'Games'
  if (/xxx|adult|porn|18\+/i.test(cat)) return 'Adult'
  return 'Other'
}

function downloadFromServer(
  destPath: string,
  apiPath: string,
): Promise<{ success?: boolean; error?: string }> {
  const srcUrl = `http://localhost:${serverPort}${apiPath.startsWith('/') ? '' : '/'}${apiPath}`
  return new Promise((resolve, reject) => {
    const req = net.fetch(srcUrl)
    req.then((response) => {
      if (!response.ok) {
        resolve({ error: `HTTP ${response.status}` })
        return
      }
      const total = Number(response.headers.get('content-length') || '0')
          const file = createWriteStream(destPath)
          let downloaded = 0

          if (!response.body) {
            resolve({ error: 'no response body' })
            return
          }

          const reader = response.body.getReader()
          function pump(): void {
            reader.read().then(({ done, value }) => {
              if (done) {
                file.end()
                // Native notification on completion
                if (Notification.isSupported()) {
                  const n = new Notification({
                    title: 'Download concluído',
                    body: destPath.split('/').pop() || destPath.split('\\').pop(),
                    silent: true,
                  })
                  n.on('click', () => {
                    shell.showItemInFolder(destPath)
                  })
                  n.show()
                }
            mainWindow?.webContents.send('download-progress', {
              filePath: destPath,
              downloaded: total,
              total,
              done: true,
            })
            resolve({ success: true })
            return
          }
          file.write(Buffer.from(value))
          downloaded += value.length
          mainWindow?.webContents.send('download-progress', {
            filePath: destPath,
            downloaded,
            total,
            done: false,
          })
          pump()
        }).catch(reject)
      }
      pump()
    }).catch(reject)
  })
}

// ─── About ────────────────────────────────────────────────────────────────────

function showAboutDialog(): void {
  const opts: Electron.MessageBoxOptions = {
    type: 'info' as const,
    title: `Sobre JackUI`,
    message: `JackUI v${appVersion.version}`,
    detail: [
      `Versão: ${appVersion.version}`,
      `Commit: ${appVersion.commit}`,
      `Build: ${appVersion.date}`,
      `Electron: ${process.versions.electron}`,
      `Chrome: ${process.versions.chrome}`,
      `Node.js: ${process.versions.node}`,
      `Go: (embutido)`,
      `Plataforma: ${process.platform} ${process.arch}`,
    ].join('\n'),
  }
  if (mainWindow) {
    dialog.showMessageBox(mainWindow, opts)
  } else {
    dialog.showMessageBox(opts)
  }
}

// ─── IPC Handlers ───────────────────────────────────────────────────────────

ipcMain.handle('get-server-port', () => serverPort)
ipcMain.handle('get-platform', () => process.platform)
ipcMain.handle('get-app-version', (): AppVersion => ({ ...appVersion }))

ipcMain.handle('get-stream-status', (): StreamRate => currentRate)

ipcMain.handle('get-preferences', (): Preferences => ({ ...prefs }))

ipcMain.handle('set-preferences', (_e, p: Partial<Preferences>) => {
  prefs = { ...prefs, ...p }
  savePrefs()
  return { ...prefs }
})

ipcMain.handle('select-folder', async () => {
  if (!mainWindow) return null
  const result = await dialog.showOpenDialog(mainWindow, {
    properties: ['openDirectory'],
    defaultPath: prefs.downloadFolder || undefined,
  })
  if (result.canceled || !result.filePaths[0]) return null
  return result.filePaths[0]
})

ipcMain.handle('get-auto-launch', (): boolean =>
  app.getLoginItemSettings().openAtLogin,
)

ipcMain.handle('set-auto-launch', (_e, enable: boolean) => {
  app.setLoginItemSettings({
    openAtLogin: enable,
    path: app.getPath('exe'),
  })
})

ipcMain.handle('open-download-folder', () => {
  if (prefs.downloadFolder) {
    shell.openPath(prefs.downloadFolder)
  }
})

ipcMain.handle('show-item-in-folder', (_e, filePath: string) => {
  shell.showItemInFolder(filePath)
})

// downloadFileWithDialog: shows a save dialog, then streams the file. Shared by
// the 'download-file' handler AND used as the fallback for 'download-file-direct'
// when no default folder is configured.
async function downloadFileWithDialog(
  apiPath: string, suggestedName: string, category?: string, mediaKind?: string,
): Promise<{ success?: boolean; cancelled?: boolean; error?: string; filePath?: string }> {
  if (!mainWindow) return { error: 'no window' }

  const folder = prefs.downloadFolder || app.getPath('downloads')
  const sub = category ? resolveCategoryFolder(category, mediaKind || '') : ''
  const dir = sub ? join(folder, sub) : folder
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true })

  const defaultPath = join(dir, suggestedName)

  const result = await dialog.showSaveDialog(mainWindow, {
    defaultPath,
    filters: [
      { name: 'Media', extensions: ['mp4', 'mkv', 'avi', 'mov', 'webm', 'm4v', 'mp3', 'flac', 'm4a'] },
      { name: 'All Files', extensions: ['*'] },
    ],
  })
  if (result.canceled || !result.filePath) return { cancelled: true }

  // Remember this folder as the new default
  const chosenDir = dirname(result.filePath)
  if (chosenDir !== prefs.downloadFolder) {
    prefs.downloadFolder = chosenDir
    savePrefs()
  }

  return downloadFromServer(result.filePath, apiPath)
}

// download-file: save dialog download.
ipcMain.handle(
  'download-file',
  (_e, apiPath: string, suggestedName: string, category?: string, mediaKind?: string) =>
    downloadFileWithDialog(apiPath, suggestedName, category, mediaKind),
)

// download-file-direct: Download directly to the configured folder (no dialog).
// Uses automatic categorization when category is provided.
ipcMain.handle(
  'download-file-direct',
  async (_e, apiPath: string, suggestedName: string, category?: string, mediaKind?: string) => {
    const baseFolder = prefs.downloadFolder
    if (!baseFolder) {
      // No folder configured → fall back to the save dialog. (ipcMain.emit would
      // return a boolean, not the handler's Promise/result — so call the fn.)
      return downloadFileWithDialog(apiPath, suggestedName, category, mediaKind)
    }

    const sub = category ? resolveCategoryFolder(category, mediaKind || '') : ''
    const dir = sub ? join(baseFolder, sub) : baseFolder
    if (!existsSync(dir)) mkdirSync(dir, { recursive: true })

    const destPath = join(dir, suggestedName)
    const result = await downloadFromServer(destPath, apiPath)
    return { ...result, filePath: destPath }
  }
)

// ─── App Lifecycle ─────────────────────────────────────────────────────────

app.on('ready', async () => {
  loadPrefs()
  buildAppMenu()

  // Auto-launch at login (user preference, default off)
  app.setLoginItemSettings({
    openAtLogin: false, // user enables via Preferences
    path: app.getPath('exe'),
  })

  if (!isDev) {
    try {
      await startGoServer()
    } catch (err) {
      console.error('[electron] failed to start Go server:', err)
      app.quit()
      return
    }
  }
  createWindow()
  createTray()
  startPolling()
})

app.on('window-all-closed', () => {
  // Keep running in tray
})

// Mata o servidor Go embutido. Spawnado com detached+unref (sobrevive ao pai),
// então PRECISA ser morto explicitamente no quit — senão fica órfão segurando a
// porta (sintoma: "address already in use" na próxima abertura).
function killGoServer(): void {
  if (goProc && !goProc.killed) {
    try { goProc.kill() } catch { /* já morreu */ }
    goProc = null
    goReady = false
  }
}

app.on('before-quit', () => {
  isQuitting = true
  stopPolling()
  killGoServer()
})

app.on('activate', () => {
  mainWindow?.show()
})

app.on('will-quit', () => {
  stopPolling()
  killGoServer()
})
