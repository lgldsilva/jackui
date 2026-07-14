// Detecção de browser para roteamento HLS vs MP4 (Safari/iOS exigem HLS).
// Extraído de stream.ts (R3 follow-up).

export function isIOS(): boolean {
  if (typeof navigator === 'undefined') return false
  const ua = navigator.userAgent
  return /iPhone|iPad|iPod/.test(ua) ||
    (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)
}

export function isSafariBrowser(): boolean {
  if (typeof navigator === 'undefined') return false
  const ua = navigator.userAgent
  if (isIOS()) return true
  if (/Chrome|Chromium|Android|Edg/.test(ua)) return false
  return /Safari/i.test(ua)
}
