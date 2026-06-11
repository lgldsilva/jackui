// viewerKind — single seam for "what can the universal viewer open?".
// Extension allowlists (never sniffing): a file from a torrent is hostile by
// default, so each kind maps to a renderer that treats content as inert data.

export type ViewerKind = 'text' | 'image' | 'pdf' | 'comic' | 'archive' | 'epub' | 'unknown'

// Text/code: rendered inside <pre> (never interpreted), so the list can be
// generous — source code, configs, subtitles, checksums, playlists.
const TEXT_EXTS = new Set([
  'txt', 'md', 'markdown', 'rst', 'tex', 'nfo', 'diz', 'info', 'log',
  'srt', 'vtt', 'ass', 'ssa', 'sub', 'smi',
  'json', 'xml', 'csv', 'tsv', 'cue', 'sfv', 'md5', 'sha1', 'sha256', 'm3u', 'm3u8', 'pls',
  'yaml', 'yml', 'ini', 'conf', 'cfg', 'toml', 'env', 'properties',
  'py', 'js', 'mjs', 'ts', 'tsx', 'jsx', 'go', 'rs', 'rb', 'php', 'java', 'kt',
  'c', 'h', 'cpp', 'hpp', 'cs', 'swift', 'lua', 'pl', 'sql',
  'sh', 'bash', 'zsh', 'bat', 'cmd', 'ps1',
  'html', 'htm', 'xhtml', 'css', 'scss', 'less', 'svelte', 'vue',
  'diff', 'patch', 'lock', 'gitignore', 'dockerfile', 'makefile', 'editorconfig',
  'readme', 'license',
])
const IMAGE_EXTS = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'avif', 'svg', 'ico'])
const PDF_EXTS = new Set(['pdf'])
const COMIC_EXTS = new Set(['cbz', 'cbr'])
const ARCHIVE_EXTS = new Set(['zip', 'tar', 'rar', 'tgz'])
const EPUB_EXTS = new Set(['epub'])

// Basenames without extension that are still readable text.
const TEXT_BASENAMES = new Set(['readme', 'license', 'changelog', 'authors', 'install', 'news', 'copying', 'notice', 'makefile', 'dockerfile'])

export function detectViewerKind(path: string): ViewerKind {
  const lowered = path.toLowerCase()
  // Compound extension first — ".tar.gz" must not classify as generic ".gz".
  if (lowered.endsWith('.tar.gz')) return 'archive'
  const base = lowered.split('/').at(-1) ?? lowered
  const lastDot = base.lastIndexOf('.')
  if (lastDot <= 0) {
    return TEXT_BASENAMES.has(base) ? 'text' : 'unknown'
  }
  const ext = base.slice(lastDot + 1)
  if (TEXT_EXTS.has(ext)) return 'text'
  if (IMAGE_EXTS.has(ext)) return 'image'
  if (PDF_EXTS.has(ext)) return 'pdf'
  if (COMIC_EXTS.has(ext)) return 'comic'
  if (ARCHIVE_EXTS.has(ext)) return 'archive'
  if (EPUB_EXTS.has(ext)) return 'epub'
  return 'unknown'
}

// isViewable: should the UI offer the eye/preview affordance for this file?
export function isViewable(path: string): boolean {
  return detectViewerKind(path) !== 'unknown'
}

// isNfoLike: candidates for CP437 decoding (classic scene NFO ASCII art).
export function isNfoLike(path: string): boolean {
  const lowered = path.toLowerCase()
  return lowered.endsWith('.nfo') || lowered.endsWith('.diz')
}
