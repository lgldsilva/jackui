// ─── Electron local download ─────────────────────────────────────────────
// Uses the Electron IPC bridge to download a file from the Go server to the
// user's local machine (Save dialog → filesystem). Falls back to browser
// download (anchor element) when not in Electron.
// apiPath: relative path starting with /api/... (withToken() already applied).
// Extraído de local.ts (#417 follow-up).

// Fallback de navegador: dispara o download via <a download>. Compartilhado
// pelas duas funções de download local (sem duplicar — usa globalThis + remove()).
function browserAnchorDownload(apiPath: string, suggestedName: string): { success: true } {
  const a = document.createElement('a')
  a.href = apiPath.startsWith('http') ? apiPath : `${globalThis.location.origin}${apiPath}`
  a.download = suggestedName
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  a.remove()
  return { success: true }
}

export async function downloadLocalFile(
  apiPath: string,
  suggestedName: string,
  category?: string,
  mediaKind?: string,
): Promise<{ success?: boolean; cancelled?: boolean; error?: string; filePath?: string }> {
  if (globalThis.electronAPI) {
    return globalThis.electronAPI.downloadFile(apiPath, suggestedName, category, mediaKind)
  }
  return browserAnchorDownload(apiPath, suggestedName)
}

/** Downloads directly to the configured Electron folder with automatic
 *  categorization (Movies/TV/Music/…). Falls back to showSaveDialog when
 *  no folder is configured, or to browser anchor when not in Electron. */
export async function downloadLocalFileDirect(
  apiPath: string,
  suggestedName: string,
  category?: string,
  mediaKind?: string,
): Promise<{ success?: boolean; error?: string; filePath?: string }> {
  if (globalThis.electronAPI) {
    return globalThis.electronAPI.downloadFileDirect(apiPath, suggestedName, category, mediaKind)
  }
  return browserAnchorDownload(apiPath, suggestedName)
}
