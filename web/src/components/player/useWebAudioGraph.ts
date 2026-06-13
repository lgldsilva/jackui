import { useCallback, useEffect, useRef, useState } from 'react'
import { usePersistedState } from '../../lib/storage'

// 10-band graphic EQ, one octave apart. lowshelf on the first band and highshelf
// on the last is the conventional shape (peaking at the extremes sounds worse);
// the middle bands are peaking with Q≈1.41 (~1 octave) so they tile the spectrum
// without big holes or excessive overlap.
export const EQ_FREQUENCIES = [31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000]
export const EQ_BANDS = EQ_FREQUENCIES.length
export const EQ_MIN_DB = -12
export const EQ_MAX_DB = 12
const EQ_Q = 1.41
const flatBands = (): number[] => new Array(EQ_BANDS).fill(0)

// createMediaElementSource throws InvalidStateError if called twice on the same
// element, and the resulting node can never be detached. Cache it per element so
// a StrictMode double-effect (or any re-run) reuses the node instead of crashing.
const sourceNodes = new WeakMap<HTMLMediaElement, MediaElementAudioSourceNode>()

// One AudioContext for the whole app. iOS caps the number of contexts and only
// unlocks them inside a user gesture, so we create ONE, unlock it on the first
// interaction, and reuse it across every audio session.
let sharedCtx: AudioContext | null = null
function sharedAudioContext(): AudioContext | null {
  const AC = globalThis.AudioContext || (globalThis as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
  if (!AC) return null
  sharedCtx ??= new AC()
  return sharedCtx
}

function getOrCreateSource(ctx: AudioContext, el: HTMLMediaElement): MediaElementAudioSourceNode {
  const existing = sourceNodes.get(el)
  if (existing) return existing
  const node = ctx.createMediaElementSource(el)
  sourceNodes.set(el, node)
  return node
}

function makeFilter(ctx: AudioContext, freq: number, index: number, gainDb: number): BiquadFilterNode {
  const f = ctx.createBiquadFilter()
  if (index === 0) f.type = 'lowshelf'
  else if (index === EQ_BANDS - 1) f.type = 'highshelf'
  else f.type = 'peaking'
  f.frequency.value = freq
  f.Q.value = EQ_Q
  f.gain.value = gainDb
  return f
}

type Graph = { filters: BiquadFilterNode[]; analyser: AnalyserNode }

// buildGraph wires source → 10 biquads → analyser → destination. Once a
// MediaElementSource exists, the element's audio MUST flow through the graph, so
// analyser→destination is always connected (it's the only output path).
function buildGraph(ctx: AudioContext, source: MediaElementAudioSourceNode, gains: number[]): Graph {
  const filters = EQ_FREQUENCIES.map((freq, i) => makeFilter(ctx, freq, i, gains[i] ?? 0))
  const analyser = ctx.createAnalyser()
  analyser.fftSize = 2048
  let prev: AudioNode = source
  for (const f of filters) { prev.connect(f); prev = f }
  prev.connect(analyser)
  analyser.connect(ctx.destination)
  return { filters, analyser }
}

const clampDb = (db: number): number => Math.max(EQ_MIN_DB, Math.min(EQ_MAX_DB, db))

export type WebAudioGraph = {
  ready: boolean
  analyser: AnalyserNode | null
  bandGains: number[]
  setBandGain: (index: number, db: number) => void
  setBands: (gains: number[]) => void
  resetBands: () => void
}

// useWebAudioGraph builds the EQ/analyser graph on the player's <video> element,
// but ONLY when enabled (audio mode) AND the AudioContext is actually running.
//
// iOS/Safari silence fix: createMediaElementSource makes the graph the ONLY
// output path, and a SUSPENDED context outputs nothing. iOS only unlocks a
// context inside a user gesture, so we (a) resume on real gestures
// (pointerdown/touchend/keydown), not just the async 'play' event, and (b) defer
// createMediaElementSource until the context is 'running'. Until then the element
// plays NATIVELY (no source node) → sound is never lost; the EQ/visualizer simply
// activate the moment the context unlocks (the user's first interaction).
export function useWebAudioGraph(
  videoRef: React.RefObject<HTMLVideoElement | null>,
  enabled: boolean,
): WebAudioGraph {
  const [bandGains, setBandGains] = usePersistedState<number[]>('audio:eq', flatBands())
  const filtersRef = useRef<BiquadFilterNode[]>([])
  const analyserRef = useRef<AnalyserNode | null>(null)
  const [ready, setReady] = useState(false)
  const gainsRef = useRef(bandGains)
  gainsRef.current = bandGains

  useEffect(() => {
    if (!enabled || analyserRef.current) return
    const el = videoRef.current
    const ctx = sharedAudioContext()
    if (!el || !ctx) return

    // Build only when the context is running (see the hook doc). Returns true
    // once built so the gesture listeners can stop.
    const build = (): boolean => {
      if (analyserRef.current) return true
      if (ctx.state !== 'running') return false
      try {
        const g = buildGraph(ctx, getOrCreateSource(ctx, el), gainsRef.current)
        filtersRef.current = g.filters
        analyserRef.current = g.analyser
        setReady(true)
      } catch {
        return false // element already bound to another graph — leave native audio
      }
      return true
    }
    if (build()) return

    // Try to unlock right away: on DESKTOP the page already had a gesture (the
    // click that opened/maximised the player), so resume() resolves even outside
    // a gesture handler and we build the moment the state flips to 'running'.
    // iOS/Safari ignores resume() outside a gesture — so we ALSO listen for real
    // gestures (the only thing it honours) and rebuild on statechange. Without
    // this immediate resume the EQ/visualizer stayed inert until the user's NEXT
    // click, which read as "unavailable".
    ctx.resume().then(build).catch(() => {})
    const resume = () => { ctx.resume().then(build).catch(() => {}) }
    const gestures: Array<keyof DocumentEventMap> = ['pointerdown', 'touchend', 'keydown']
    gestures.forEach(ev => document.addEventListener(ev, resume, { passive: true }))
    el.addEventListener('play', resume)
    ctx.addEventListener('statechange', build)
    return () => {
      gestures.forEach(ev => document.removeEventListener(ev, resume))
      el.removeEventListener('play', resume)
      ctx.removeEventListener('statechange', build)
    }
  }, [enabled, videoRef])

  // Apply gain edits live (cheap AudioParam writes) without rebuilding the graph.
  useEffect(() => {
    filtersRef.current.forEach((f, i) => { if (f) f.gain.value = bandGains[i] ?? 0 })
  }, [bandGains])

  const setBandGain = useCallback((index: number, db: number) => {
    setBandGains((prev) => { const next = [...prev]; next[index] = db; return next })
  }, [setBandGains])

  const setBands = useCallback((gains: number[]) => {
    setBandGains(EQ_FREQUENCIES.map((_, i) => clampDb(gains[i] ?? 0)))
  }, [setBandGains])

  const resetBands = useCallback(() => setBandGains(flatBands()), [setBandGains])

  return { ready, analyser: analyserRef.current, bandGains, setBandGain, setBands, resetBands }
}
