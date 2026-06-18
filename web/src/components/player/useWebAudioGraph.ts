import { useCallback, useEffect, useRef, useState } from 'react'
import { usePersistedState } from '../../lib/storage'
import { webAudioBlocked } from './playerFormat'

// 10-band graphic EQ, one octave apart. lowshelf on the first band and highshelf
// on the last is the conventional shape (peaking at the extremes sounds worse);
// the middle bands are peaking with Q≈1.41 (~1 octave) so they tile the spectrum
// without big holes or excessive overlap.
export const EQ_FREQUENCIES = [31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000]
export const EQ_BANDS = EQ_FREQUENCIES.length
export const EQ_MIN_DB = -12
export const EQ_MAX_DB = 12
const EQ_Q = 1.41
export const flatBands = (): number[] => new Array(EQ_BANDS).fill(0)

// createMediaElementSource throws InvalidStateError if called twice on the same
// element, and the resulting node can never be detached. Cache it per element so
// a StrictMode double-effect (or any re-run) reuses the node instead of crashing.
const sourceNodes = new WeakMap<HTMLMediaElement, MediaElementAudioSourceNode>()

// One AudioContext for the whole app. iOS caps the number of contexts and only
// unlocks them inside a user gesture, so we create ONE, unlock it on the first
// interaction, and reuse it across every audio session.
let sharedCtx: AudioContext | null = null
export function sharedAudioContext(): AudioContext | null {
  const AC = globalThis.AudioContext || (globalThis as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
  if (!AC) return null
  sharedCtx ??= new AC()
  return sharedCtx
}

export function getOrCreateSource(ctx: AudioContext, el: HTMLMediaElement): MediaElementAudioSourceNode {
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

export const clampDb = (db: number): number => Math.max(EQ_MIN_DB, Math.min(EQ_MAX_DB, db))

// disconnectGraph detaches a graph's nodes from the AudioContext when its element
// is gone (remounted). The source node dies with the element; these were left
// wired to destination (silent, but they'd accumulate across track switches).
function disconnectGraph(filters: BiquadFilterNode[], analyser: AnalyserNode | null): void {
  analyser?.disconnect()
  filters.forEach((f) => f.disconnect())
}

export type DualGraph = { filters: BiquadFilterNode[]; analyser: AnalyserNode; gainA: GainNode; gainB: GainNode }

// buildDualGraph wires TWO element sources (A=current, B=next) through their own
// GainNode into a SHARED 10-band EQ → analyser → destination. The per-source
// gains drive the crossfade (sample-accurate ramps on the AudioContext clock);
// the shared EQ + analyser keep the equalizer/visualizer working across the fade.
// B starts silent. Used by the gapless/crossfade engine (useAudioEngine). The
// single-source buildGraph above (the EQ path of useWebAudioGraph) is left
// untouched. This path is for DIRECT-PLAY audio only — never HLS on WebKit, where
// createMediaElementSource yields zero data (the engine's caller guards that).
export function buildDualGraph(
  ctx: AudioContext,
  sourceA: MediaElementAudioSourceNode,
  sourceB: MediaElementAudioSourceNode,
  gains: number[],
): DualGraph {
  const filters = EQ_FREQUENCIES.map((freq, i) => makeFilter(ctx, freq, i, gains[i] ?? 0))
  const analyser = ctx.createAnalyser()
  analyser.fftSize = 2048
  const gainA = ctx.createGain()
  const gainB = ctx.createGain()
  gainA.gain.value = 1
  gainB.gain.value = 0
  sourceA.connect(gainA)
  sourceB.connect(gainB)
  gainA.connect(filters[0])
  gainB.connect(filters[0])
  let prev: AudioNode = filters[0]
  for (let i = 1; i < filters.length; i++) { prev.connect(filters[i]); prev = filters[i] }
  prev.connect(analyser)
  analyser.connect(ctx.destination)
  return { filters, analyser, gainA, gainB }
}

export type WebAudioGraph = {
  ready: boolean
  analyser: AnalyserNode | null
  bandGains: number[]
  setBandGain: (index: number, db: number) => void
  setBands: (gains: number[]) => void
  resetBands: () => void
}

// useWebAudioGraph builds the EQ/analyser graph on the player's <video> element,
// but ONLY when enabled (audio mode), the source is NOT a transcoded HLS track on
// WebKit (see webAudioBlocked), AND the AudioContext is actually running.
//
// iOS/Safari silence fix: createMediaElementSource makes the graph the ONLY
// output path, and a SUSPENDED context outputs nothing. iOS only unlocks a
// context inside a user gesture, so we (a) resume on real gestures
// (pointerdown/touchend/keydown), not just the async 'play' event, and (b) defer
// createMediaElementSource until the context is 'running'. Until then the element
// plays NATIVELY (no source node) → sound is never lost; the EQ/visualizer simply
// activate the moment the context unlocks (the user's first interaction).
//
// `isHls` gates the one combination WebKit cannot tap (HLS → zero data, mute
// element). When the track's transport class flips on WebKit the parent remounts
// the <video> (a fresh element with no bound source — see VideoPlayerElement's
// key), so we detect the element swap here and drop the stale graph before
// (re)building on the new one — otherwise the analyserRef guard would keep a dead
// analyser and never rebuild.
export function useWebAudioGraph(
  videoRef: React.RefObject<HTMLVideoElement | null>,
  enabled: boolean,
  isHls: boolean,
): WebAudioGraph {
  const [bandGains, setBandGains] = usePersistedState<number[]>('audio:eq', flatBands())
  const filtersRef = useRef<BiquadFilterNode[]>([])
  const analyserRef = useRef<AnalyserNode | null>(null)
  const builtElRef = useRef<HTMLMediaElement | null>(null)
  const [ready, setReady] = useState(false)
  const gainsRef = useRef(bandGains)
  gainsRef.current = bandGains

  useEffect(() => {
    const el = videoRef.current
    // The previous element was remounted (track changed transport class on
    // WebKit): its graph died with it, so reset the refs to rebuild on the new one.
    if (builtElRef.current && builtElRef.current !== el) {
      disconnectGraph(filtersRef.current, analyserRef.current)
      builtElRef.current = null
      filtersRef.current = []
      analyserRef.current = null
      setReady(false)
    }
    // Block ONLY a transcoded HLS track on WebKit (Safari/iOS): there
    // createMediaElementSource yields zero audio data and mutes the element
    // irreversibly. Direct-play files work on iOS; non-WebKit routes HLS via MSE.
    if (!enabled || analyserRef.current || !el || webAudioBlocked(isHls)) return
    const ctx = sharedAudioContext()
    if (!ctx) return

    // Build only when the context is running (see the hook doc). Returns true
    // once built so the gesture listeners can stop.
    const build = (): boolean => {
      if (analyserRef.current) return true
      if (ctx.state !== 'running') return false
      // SAFEGUARD (iOS direct-play): never tap an element that hasn't decoded any
      // data yet. createMediaElementSource makes the graph the only output path; on
      // WebKit, doing it while the element is still empty freezes it (readyState 2,
      // mute — the regression d0e8b9e disabled all iOS EQ to avoid). Waiting for
      // HAVE_CURRENT_DATA means the media is already flowing, so the tap is safe.
      // Re-tried on loadeddata/canplay (listeners below) once data arrives.
      if (el.readyState < HTMLMediaElement.HAVE_CURRENT_DATA) return false
      try {
        const g = buildGraph(ctx, getOrCreateSource(ctx, el), gainsRef.current)
        filtersRef.current = g.filters
        analyserRef.current = g.analyser
        builtElRef.current = el
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
    // loadeddata/canplay: retry once the element has data (the readyState gate
    // above may have deferred the build until the media decoded its first frames).
    el.addEventListener('loadeddata', resume)
    el.addEventListener('canplay', resume)
    ctx.addEventListener('statechange', build)
    return () => {
      gestures.forEach(ev => document.removeEventListener(ev, resume))
      el.removeEventListener('play', resume)
      el.removeEventListener('loadeddata', resume)
      el.removeEventListener('canplay', resume)
      ctx.removeEventListener('statechange', build)
    }
  }, [enabled, isHls, videoRef])

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
