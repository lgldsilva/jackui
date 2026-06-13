// EQ presets: 10-band gain curves (dB) matching EQ_FREQUENCIES
// (31,62,125,250,500,1k,2k,4k,8k,16k). Pure data + a tiny matcher → unit-tested.

export type EqPreset = { key: string; gains: number[] }

export const EQ_PRESETS: ReadonlyArray<EqPreset> = [
  { key: 'flat', gains: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0] },
  { key: 'rock', gains: [4, 3, 2, 0, -1, -1, 1, 3, 4, 4] },
  { key: 'pop', gains: [-1, 0, 2, 3, 3, 2, 1, 0, -1, -1] },
  { key: 'jazz', gains: [3, 2, 1, 2, -1, -1, 0, 1, 2, 3] },
  { key: 'bass', gains: [6, 5, 4, 2, 0, 0, 0, 0, 0, 0] },
  { key: 'treble', gains: [0, 0, 0, 0, 0, 1, 2, 4, 5, 6] },
  { key: 'vocal', gains: [-2, -1, 0, 2, 4, 4, 3, 1, 0, -1] },
]

// activePresetKey returns the preset whose curve exactly matches the current
// band gains (so the UI can highlight it), or null for a custom curve.
export function activePresetKey(bandGains: number[]): string | null {
  for (const p of EQ_PRESETS) {
    if (p.gains.every((g, i) => g === (bandGains[i] ?? 0))) return p.key
  }
  return null
}
