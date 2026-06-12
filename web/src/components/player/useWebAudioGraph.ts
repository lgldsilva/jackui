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

export type WebAudioGraph = {
  ready: boolean
  analyser: AnalyserNode | null
  bandGains: number[]
  setBandGain: (index: number, db: number) => void
  resetBands: () => void
}

// useWebAudioGraph builds the EQ/analyser graph on the player's <video> element,
// but ONLY when enabled (audio mode). The video path never enables it, so the
// native video element is never tapped (the EQ can't introduce A/V lag). The
// PlayerProvider remounts the modal by kind, so the audio element is always a
// fresh element that has never played video — the createMediaElementSource
// one-shot constraint is safe.
export function useWebAudioGraph(
  videoRef: React.RefObject<HTMLVideoElement | null>,
  enabled: boolean,
): WebAudioGraph {
  const [bandGains, setBandGains] = usePersistedState<number[]>('audio:eq', flatBands())
  const ctxRef = useRef<AudioContext | null>(null)
  const filtersRef = useRef<BiquadFilterNode[]>([])
  const analyserRef = useRef<AnalyserNode | null>(null)
  const [ready, setReady] = useState(false)

  useEffect(() => {
    if (!enabled || analyserRef.current) return
    const el = videoRef.current
    const AC = window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
    if (!el || !AC) return
    const ctx = ctxRef.current ?? new AC()
    ctxRef.current = ctx
    let graph: Graph
    try {
      graph = buildGraph(ctx, getOrCreateSource(ctx, el), bandGains)
    } catch {
      return // element already bound to another graph — bail rather than crash
    }
    filtersRef.current = graph.filters
    analyserRef.current = graph.analyser
    setReady(true)

    // AudioContext is born suspended under the autoplay policy; resume on the
    // first play (a user gesture started it) so sound actually flows.
    const resume = () => { if (ctx.state === 'suspended') ctx.resume().catch(() => {}) }
    el.addEventListener('play', resume)
    resume()
    return () => el.removeEventListener('play', resume)
  }, [enabled, videoRef, bandGains])

  // Apply gain edits live (cheap AudioParam writes) without rebuilding the graph.
  useEffect(() => {
    filtersRef.current.forEach((f, i) => { if (f) f.gain.value = bandGains[i] ?? 0 })
  }, [bandGains])

  const setBandGain = useCallback((index: number, db: number) => {
    setBandGains((prev) => { const next = [...prev]; next[index] = db; return next })
  }, [setBandGains])

  const resetBands = useCallback(() => setBandGains(flatBands()), [setBandGains])

  return { ready, analyser: analyserRef.current, bandGains, setBandGain, resetBands }
}
