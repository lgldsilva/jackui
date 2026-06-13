// audioCaps reports which non-universal audio codecs THIS browser can play
// inline, as capability tokens the backend understands (see
// internal/handlers/local_play.go `normalizeAudioCodec`). The backend uses them
// to decide direct-play vs audio-only HLS transcode: Safari/WebKit refuse
// FLAC/OGG/Opus/WAV, so without this hint they'd silently fail to play. aac/mp3
// are universal (always direct) and are NOT probed/sent.

type CanPlay = (mimeType: string) => string

// Each token maps to the MIME/codec strings that prove support via canPlayType.
// A token is advertised when ANY of its probes returns "probably" or "maybe".
const PROBES: ReadonlyArray<{ token: string; types: readonly string[] }> = [
  { token: 'flac', types: ['audio/flac', 'audio/x-flac'] },
  { token: 'opus', types: ['audio/ogg; codecs="opus"', 'audio/webm; codecs="opus"'] },
  { token: 'vorbis', types: ['audio/ogg; codecs="vorbis"'] },
  { token: 'wav', types: ['audio/wav', 'audio/wave', 'audio/x-wav'] },
  { token: 'alac', types: ['audio/mp4; codecs="alac"'] },
]

// computeAudioCaps is the pure core (testable without a DOM): given a canPlayType
// implementation, return the supported capability tokens.
export function computeAudioCaps(canPlay: CanPlay): string[] {
  const caps: string[] = []
  for (const p of PROBES) {
    if (p.types.some((t) => { const r = canPlay(t); return r === 'probably' || r === 'maybe' })) {
      caps.push(p.token)
    }
  }
  return caps
}

let cached: string | null = null

// audioCapsParam returns the comma-joined token list for the `acaps` query
// param, computed once per session (the browser's codec support is static).
// Empty string when no DOM / no extra codecs supported (the backend then only
// direct-plays the universal aac/mp3 and transcodes the rest — always safe).
export function audioCapsParam(): string {
  if (cached !== null) return cached
  try {
    const el = document.createElement('audio')
    cached = computeAudioCaps((t) => el.canPlayType(t)).join(',')
  } catch {
    cached = ''
  }
  return cached
}
