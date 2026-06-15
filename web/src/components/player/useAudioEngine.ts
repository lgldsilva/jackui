import { useCallback, useEffect, useRef, useState } from 'react'
import { usePersistedState } from '../../lib/storage'
import {
  EQ_FREQUENCIES, flatBands, clampDb, sharedAudioContext, getOrCreateSource,
  buildDualGraph, type DualGraph, type WebAudioGraph,
} from './useWebAudioGraph'
import { crossfadeDue } from './audioEngineLogic'
import { clientLog } from '../../lib/diag'
import type { TransitionMode } from './transition'

// useAudioEngine: gapless/crossfade entre faixas DIRECT-PLAY (álbum do mesmo
// torrent OU itens distintos da playlist — tudo via URL resolvida). Mantém DOIS
// <audio> persistentes (A/B em ping-pong) roteados por GainNode → EQ compartilhado
// → analyser → destination (buildDualGraph). A faixa atual toca no elemento
// ATIVO; a próxima (nextSrc, resolvida pelo caller) é pré-carregada no ocioso;
// perto do fim faz crossfade (rampa de ganho no clock do AudioContext) ou troca
// seca (gapless), e chama onAdvance() pra o player sincronizar SEM reiniciar a
// reprodução. O caller garante o gate (áudio direct-play + transition≠off) e
// passa nextSrc=null quando a próxima não é direct-play/resolvível → hard-cut.
//
// Os 2 elementos são renderizados pelo PAI (refs elARef/elBRef → <audio>) e NUNCA
// recriados (createMediaElementSource é irreversível por elemento). Estar no DOM
// (mesmo hidden) é mais seguro p/ o tap do Web Audio no iOS que um new Audio().

export type AudioEngine = {
  active: boolean
  activeElRef: React.RefObject<HTMLAudioElement | null>
  // elA/elBRef vão no `ref` de <audio> (renderizados pelo pai) — tipados como o
  // videoRef do modal pra casar com o que o JSX `ref` espera.
  elARef: React.RefObject<HTMLAudioElement>
  elBRef: React.RefObject<HTMLAudioElement>
  graph: WebAudioGraph | null
  // Estado play/pause do elemento ATIVO (React state, re-sincroniza no swap) — o
  // transport usa isto pro ícone, já que ler activeElRef.current num efeito não é
  // reativo (ref preenchido depois, sem re-render).
  paused: boolean
}

type EngineOpts = {
  enabled: boolean
  currentSrc: string        // URL direct-play da faixa atual ('' = nenhuma)
  nextSrc: string | null    // URL direct-play da próxima, ou null → hard-cut
  mode: TransitionMode
  crossfadeSec: number      // lido via optsRef nos effects
  onAdvance: () => void     // idem
}

const FADE_GUARD = 0.05 // evita rampa de duração zero

export function useAudioEngine(opts: EngineOpts): AudioEngine {
  const { enabled, currentSrc, nextSrc, mode } = opts
  const [bandGains, setBandGains] = usePersistedState<number[]>('audio:eq', flatBands())
  const elARef = useRef<HTMLAudioElement>(null)
  const elBRef = useRef<HTMLAudioElement>(null)
  const graphRef = useRef<DualGraph | null>(null)
  const activeIsA = useRef(true)
  const fadingRef = useRef(false)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [ready, setReady] = useState(false)
  const [paused, setPaused] = useState(true)
  const activeElRef = useRef<HTMLAudioElement | null>(null)
  const gainsRef = useRef(bandGains)
  gainsRef.current = bandGains
  const optsRef = useRef(opts)
  optsRef.current = opts

  // active = elemento que toca agora; idle = o que pré-carrega a próxima.
  const els = useCallback(() => {
    const active = activeIsA.current ? elARef.current : elBRef.current
    const idle = activeIsA.current ? elBRef.current : elARef.current
    return { active, idle }
  }, [])

  const setGains = useCallback((g: DualGraph | null, activeGain: number) => {
    if (!g) return
    // cancelScheduledValues: descarta rampas de crossfade pendentes antes de fixar
    // o ganho — senão o setValue é sobrescrito pela automação ainda agendada (ex.:
    // salto manual de faixa no meio do fade).
    const now = sharedAudioContext()?.currentTime ?? 0
    const a = activeIsA.current ? activeGain : 1 - activeGain
    const b = activeIsA.current ? 1 - activeGain : activeGain
    g.gainA.gain.cancelScheduledValues(now); g.gainA.gain.value = a
    g.gainB.gain.cancelScheduledValues(now); g.gainB.gain.value = b
  }, [])

  // Constrói o grafo dual quando o contexto está running (mesmo gate de gesto do EQ).
  useEffect(() => {
    if (!enabled || graphRef.current) return
    const a = elARef.current, b = elBRef.current
    const ctx = sharedAudioContext()
    if (!a || !b || !ctx) return
    const build = (): boolean => {
      if (graphRef.current) return true
      if (ctx.state !== 'running') return false
      try {
        graphRef.current = buildDualGraph(ctx, getOrCreateSource(ctx, a), getOrCreateSource(ctx, b), gainsRef.current)
        setReady(true)
        clientLog('info', 'audioengine', 'graph built (dual)')
      } catch (e) { clientLog('warn', 'audioengine', 'graph build failed', { err: String(e) }); return false }
      return true
    }
    if (build()) return
    ctx.resume().then(build).catch(() => {})
    // No gesto que destrava o ctx, também (re)tenta tocar o elemento ativo: no
    // iOS o play() do effect (fora de gesto) pode ser bloqueado e o <video> está
    // mudo no modo-motor — sem isto a faixa não começaria até outro gesto.
    const resume = () => {
      ctx.resume().then(build).catch(() => {})
      ;(activeIsA.current ? elARef.current : elBRef.current)?.play().catch(() => {})
    }
    // Gatilho CONFIÁVEL (igual ao grafo single do #247): o evento 'play' dos
    // <audio> — disparado dentro da reprodução iniciada por gesto, é o que de fato
    // destrava o AudioContext no Chrome (o resume() no mount, fora de handler, é
    // não-confiável e os gestos no document só vêm num PRÓXIMO clique). Sem isto o
    // ctx ficava 'suspended' e o grafo dual nunca montava.
    const onElPlay = () => { ctx.resume().then(build).catch(() => {}) }
    a.addEventListener('play', onElPlay)
    b.addEventListener('play', onElPlay)
    const gestures: Array<keyof DocumentEventMap> = ['pointerdown', 'touchend', 'keydown']
    gestures.forEach(ev => document.addEventListener(ev, resume, { passive: true }))
    ctx.addEventListener('statechange', build)
    return () => {
      a.removeEventListener('play', onElPlay)
      b.removeEventListener('play', onElPlay)
      gestures.forEach(ev => document.removeEventListener(ev, resume))
      ctx.removeEventListener('statechange', build)
    }
  }, [enabled])

  // Aplica edições de EQ ao vivo nos biquads compartilhados.
  useEffect(() => {
    graphRef.current?.filters.forEach((f, i) => { if (f) f.gain.value = bandGains[i] ?? 0 })
  }, [bandGains])

  // Carrega a faixa ATUAL no elemento ativo. Idempotente: após um swap o (novo)
  // ativo JÁ tem currentSrc → não recarrega (é o que torna a transição contínua).
  useEffect(() => {
    if (!enabled || !currentSrc) return
    const { active } = els()
    if (!active) return
    activeElRef.current = active
    if (active.src !== currentSrc) { active.src = currentSrc; active.load() }
    clientLog('info', 'audioengine', 'engine track', { mode, hasNext: !!nextSrc })
    setGains(graphRef.current, 1)
    // Cancela qualquer crossfade pendente (ex.: salto manual de faixa no meio da
    // rampa) — sem isso o setTimeout antigo dispararia um swap/advance espúrio.
    if (fadeTimerRef.current !== null) { clearTimeout(fadeTimerRef.current); fadeTimerRef.current = null }
    fadingRef.current = false
    active.play().catch(() => {})
  }, [enabled, currentSrc, els, setGains])

  // Motor desligou (transição→off, probe-flip de HLS, troca p/ vídeo): para os
  // DOIS <audio> e cancela fade pendente, senão eles seguem tocando enquanto o
  // <video> volta a ter som → áudio dobrado.
  useEffect(() => {
    if (enabled) return
    if (fadeTimerRef.current !== null) { clearTimeout(fadeTimerRef.current); fadeTimerRef.current = null }
    fadingRef.current = false
    elARef.current?.pause()
    elBRef.current?.pause()
  }, [enabled])

  // Pré-carrega a próxima faixa no elemento ocioso. NÃO recarrega durante um fade
  // em curso (fadingRef) — o ocioso está audivelmente subindo de ganho; trocar o
  // src dele no meio cortaria o crossfade. Pós-swap o currentSrc muda e o efeito
  // re-roda com o novo ocioso.
  useEffect(() => {
    if (!enabled || !nextSrc || mode === 'off' || fadingRef.current) return
    const { idle } = els()
    if (idle && idle.src !== nextSrc) { idle.src = nextSrc; idle.load() }
  }, [enabled, nextSrc, mode, els])

  // Espelha play/pause do elemento ATIVO no estado React (pro ícone do transport).
  // Re-anexa quando a faixa muda (swap troca o elemento ativo). Lê els() — os
  // <audio> já estão montados (refs do pai) quando este efeito roda.
  useEffect(() => {
    if (!enabled) return
    const { active } = els()
    if (!active) return
    setPaused(active.paused)
    const onPlay = () => setPaused(false)
    const onPause = () => setPaused(true)
    active.addEventListener('play', onPlay)
    active.addEventListener('pause', onPause)
    return () => {
      active.removeEventListener('play', onPlay)
      active.removeEventListener('pause', onPause)
    }
  }, [enabled, currentSrc, els])

  // Agenda gapless/crossfade no elemento ativo.
  useEffect(() => {
    if (!enabled) return
    const { active, idle } = els()
    const ctx = sharedAudioContext()
    if (!active || !idle || !ctx) return

    const swap = () => {
      fadeTimerRef.current = null
      activeIsA.current = !activeIsA.current
      activeElRef.current = activeIsA.current ? elARef.current : elBRef.current
      optsRef.current.onAdvance()
    }

    // DIAGNÓSTICO (temporário): loga 1× por faixa quando entra na janela de
    // crossfade, com o estado que decide se dispara — pra achar por que não cruza.
    let loggedDue = false
    const onTime = () => {
      const o = optsRef.current
      const g = graphRef.current
      const due = crossfadeDue(active.currentTime, active.duration, o.crossfadeSec)
      if (due && !loggedDue) {
        loggedDue = true
        clientLog('info', 'audioengine', 'crossfade window', {
          mode: o.mode, hasNext: !!o.nextSrc, graphReady: !!g,
          idleReady: idle.readyState, dur: active.duration, fading: fadingRef.current,
        })
      }
      if (fadingRef.current || o.mode !== 'crossfade' || !o.nextSrc || !g) return
      if (idle.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) return
      if (!due) return
      clientLog('info', 'audioengine', 'crossfade firing', { sec: o.crossfadeSec, dur: active.duration })
      // Janela de crossfade: rampa sample-accurate nos dois ganhos.
      fadingRef.current = true
      const now = ctx.currentTime
      const sec = Math.max(FADE_GUARD, o.crossfadeSec)
      const gActive = activeIsA.current ? g.gainA : g.gainB
      const gIdle = activeIsA.current ? g.gainB : g.gainA
      idle.play().catch(() => {})
      gActive.gain.setValueAtTime(gActive.gain.value, now)
      gActive.gain.linearRampToValueAtTime(0, now + sec)
      gIdle.gain.setValueAtTime(gIdle.gain.value, now)
      gIdle.gain.linearRampToValueAtTime(1, now + sec)
      fadeTimerRef.current = globalThis.setTimeout(swap, sec * 1000)
    }

    const onEnded = () => {
      const o = optsRef.current
      if (o.mode === 'off' || !o.nextSrc || fadingRef.current) return
      // Gapless (corte seco, sem silêncio): troca instantânea de ganho + play.
      setGains(graphRef.current, 0)
      idle.play().catch(() => {})
      swap()
    }

    active.addEventListener('timeupdate', onTime)
    active.addEventListener('ended', onEnded)
    return () => {
      active.removeEventListener('timeupdate', onTime)
      active.removeEventListener('ended', onEnded)
      // Cancela um fade pendente ao re-rodar o efeito / desmontar — evita o swap
      // disparar em refs órfãs (avanço fantasma).
      if (fadeTimerRef.current !== null) { clearTimeout(fadeTimerRef.current); fadeTimerRef.current = null }
    }
  }, [enabled, currentSrc, els])

  const setBandGain = useCallback((index: number, db: number) => {
    setBandGains(prev => { const next = [...prev]; next[index] = clampDb(db); return next })
  }, [setBandGains])
  const setBands = useCallback((gains: number[]) => {
    setBandGains(EQ_FREQUENCIES.map((_, i) => clampDb(gains[i] ?? 0)))
  }, [setBandGains])
  const resetBands = useCallback(() => setBandGains(flatBands()), [setBandGains])

  const graph: WebAudioGraph | null = ready && graphRef.current
    ? { ready, analyser: graphRef.current.analyser, bandGains, setBandGain, setBands, resetBands }
    : null

  return { active: enabled && ready, activeElRef, elARef, elBRef, graph, paused }
}
