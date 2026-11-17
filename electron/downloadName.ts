import * as nodePath from 'path'

// Download names arrive over IPC from the renderer and ultimately from torrent
// metainfo, so they are attacker-controlled. Everything here is pure and takes
// the path flavour as a parameter so Windows rules are testable on any host.

type PathApi = Pick<typeof nodePath, 'sep' | 'join' | 'relative'>

const FALLBACK_NAME = 'download'
const WIN_RESERVED_CHARS = /[<>:"|?*]/g
const WIN_DEVICE_NAME = /^(con|prn|aux|nul|com\d|lpt\d)(\.|$)/i

function isControlChar(ch: string): boolean {
  const code = ch.charCodeAt(0)
  return code < 0x20 || code === 0x7f
}

// Win32 strips trailing dots/spaces and gives ':' (drive, alternate data
// stream) and device names a meaning, so a name that looks inert can still
// resolve to something else.
function hardenForWindows(name: string): string {
  const cleaned = name.replace(WIN_RESERVED_CHARS, '_').replace(/[. ]+$/, '')
  return WIN_DEVICE_NAME.test(cleaned) ? `_${cleaned}` : cleaned
}

// sanitizeDownloadName reduces an untrusted name to a bare file name: the last
// component under BOTH separators (a POSIX host still must not trust '\\', and
// the renderer only splits on '/'), without control characters.
export function sanitizeDownloadName(name: unknown, p: PathApi = nodePath): string {
  if (typeof name !== 'string') return FALLBACK_NAME
  const base = name.split(/[\\/]/).pop() ?? ''
  const visible = Array.from(base).filter((ch) => !isControlChar(ch)).join('')
  const safe = p.sep === '\\' ? hardenForWindows(visible) : visible
  if (safe.trim() === '' || safe === '.' || safe === '..') return FALLBACK_NAME
  return safe
}

// resolveDownloadPath returns the destination for suggestedName directly inside
// dir, and refuses anything that would still land elsewhere.
export function resolveDownloadPath(dir: string, suggestedName: unknown, p: PathApi = nodePath): string {
  const name = sanitizeDownloadName(suggestedName, p)
  const dest = p.join(dir, name)
  if (p.relative(dir, dest) !== name) throw new Error('invalid download file name')
  return dest
}
