import { useEffect, useRef } from 'react'
import {
  SearchResult,
  TorrentInfo,
  TranscodeCapabilities,
  streamAdd,
  streamMetadata,
  pickTorrentSource,
  streamInfo,
  streamViewerOpen,
  streamViewerClose,
  subtitlesEnabled,
  fetchMediaToken,
  transcodeCapabilities,
} from '../../api/client'
import { clientLog } from '../../lib/diag'
import { chooseInitialFile } from './playerEffects'
import type { TFn } from './playerTypes'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// Owns the streaming session lifecycle: media-token fetch, the authoritative
// streamAdd (+ metadata-cache preview) on open, the Cinema↔Música re-send, the
// 2s progress poll, and the viewer lease. Cross-cutting state resets for a new
// result live in `resetForNewResult` (called with the warm-hold flag) so this
// hook doesn't need to thread every setter the reset touches.
export function useStreamSession(deps: {
  result: SearchResult | null
  audioMode: boolean
  initialFileIndex?: number
  t: TFn
  info: TorrentInfo | null
  selectedFile: number
  caps: TranscodeCapabilities | null
  blessed: boolean
  setLoading: Setter<boolean>
  setError: Setter<string>
  setInfo: Setter<TorrentInfo | null>
  setSelectedFile: Setter<number>
  setServerReady: Setter<boolean>
  setMediaToken: Setter<string>
  setSubEnabled: Setter<boolean>
  setCaps: Setter<TranscodeCapabilities | null>
  setBlessed: Setter<boolean>
  resetForNewResult: (warmHold: boolean) => void
}) {
  const {
    result, audioMode, initialFileIndex, t, info, selectedFile, caps, blessed,
    setLoading, setError, setInfo, setSelectedFile, setServerReady, setMediaToken,
    setSubEnabled, setCaps, setBlessed, resetForNewResult,
  } = deps

  // streamAddDoneRef: o streamAdd (autoritativo) já resolveu? Evita que o
  // preview do cache de metadados sobrescreva o resultado autoritativo na corrida.
  const streamAddDoneRef = useRef(false)
  // everReadyRef: vira true assim que o player já mostrou conteúdo (info +
  // arquivo) ao menos uma vez nesta instância. Habilita o "warm hold" na troca
  // de faixa de música (ver o efeito [result]) e suprime o overlay de start.
  const everReadyRef = useRef(false)
  const prevAudioModeRef = useRef(audioMode)
  const pollRef = useRef<ReturnType<typeof globalThis.setInterval> | null>(null)

  // Pede um media token (JWT TTL longo, scope="media") ao abrir o player.
  // Necessário ANTES de montar o <video src> pra que a URL não troque depois
  // (o que faria o browser interpretar como mídia nova e resetar pra 0).
  // Refresh do access token regular em background não afeta este — só vai
  // expirar depois da sessão de playback inteira (6h default).
  useEffect(() => {
    if (!result) return
    let cancelled = false
    fetchMediaToken()
      .then(t => { if (!cancelled) setMediaToken(t) })
      .catch(() => {}) // fallback: streamURL fica vazio, UI mostra "carregando"
    return () => { cancelled = true }
  }, [result?.infoHash])

  // Add the torrent when modal opens
  useEffect(() => {
    if (!result || !pickTorrentSource(result)) return

    // Guard against a slow streamAdd from the PREVIOUS result resolving after
    // we've switched to a new one — without it the old torrent's file list +
    // thumbnails clobber the new video. Flipped by the cleanup below.
    let cancelled = false

    // warmHold: numa troca de faixa de MÚSICA com o player já populado, NÃO
    // desmonta a UI (capa/seekbar/transport) nem corta o áudio atual — segura o
    // `info`/`selectedFile`/`serverReady` antigos (streamURL deriva de `info`, então
    // o <video> continua na faixa atual) até o streamMetadata/streamAdd da nova
    // resolver; aí a troca é atômica. Sem isso a tela "piscava" (overlay
    // "Conectando ao swarm") a cada faixa. Escopo só ÁUDIO (vídeo mantém o reset
    // completo, sem regressão); cold start (1ª faixa) também reseta normal.
    const warmHold = everReadyRef.current && audioMode
    streamAddDoneRef.current = false

    resetForNewResult(warmHold)

    // Try the cached metadata first — if the server has seen this hash before,
    // the file list + name appear instantly. streamAdd still kicks off in
    // parallel to actually load the torrent client (required for playback).
    if (result.infoHash) {
      streamMetadata(result.infoHash).then(cached => {
        // streamAddDoneRef (não `info`): no warm hold o `info` antigo ainda está
        // setado, então o preview do cache PRECISA poder sobrescrevê-lo; só não
        // pode passar por cima do streamAdd autoritativo se este já resolveu.
        if (cancelled || !cached || streamAddDoneRef.current) return
        setInfo(cached)
        setSelectedFile(chooseInitialFile(cached, initialFileIndex))
      })
    }

    streamAdd(pickTorrentSource(result), audioMode ? 'audio' : 'video')
      .then(t => {
        if (cancelled) return
        streamAddDoneRef.current = true
        setInfo(t)
        setSelectedFile(chooseInitialFile(t, initialFileIndex))
        // Streamer now has the torrent active — unblock <video src>.
        setServerReady(true)
      })
      .catch(err => { if (!cancelled) setError(err?.response?.data?.error || err.message || t('player.modal.streamStartFailed')) })
      .finally(() => { if (!cancelled) setLoading(false) })

    // Check whether subtitles backend is configured
    subtitlesEnabled().then(v => { if (!cancelled) setSubEnabled(v) }).catch(() => { if (!cancelled) setSubEnabled(false) })
    // Cache transcode capabilities once per modal — used by HEVC auto-fallback decision.
    if (!caps) {
      transcodeCapabilities().then(c => { if (!cancelled) setCaps(c) }).catch(() => { if (!cancelled) setCaps(null) })
    }

    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result?.infoHash])

  // Cinema ↔ Música com o mesmo torrent: re-envia kind ao backend sem reiniciar
  // o player inteiro (o efeito acima omite audioMode de propósito).
  useEffect(() => {
    if (!result?.infoHash || !everReadyRef.current) {
      prevAudioModeRef.current = audioMode
      return
    }
    if (prevAudioModeRef.current === audioMode) return
    prevAudioModeRef.current = audioMode
    let cancelled = false
    streamAdd(pickTorrentSource(result), audioMode ? 'audio' : 'video')
      .then(t => {
        if (cancelled) return
        streamAddDoneRef.current = true
        setInfo(t)
        setSelectedFile(cur => (cur >= 0 ? cur : chooseInitialFile(t, initialFileIndex)))
      })
      .catch(err => {
        if (!cancelled) setError(err?.response?.data?.error || err.message || t('player.modal.streamStartFailed'))
      })
    return () => { cancelled = true }
  }, [audioMode, result?.infoHash, initialFileIndex, result, t])

  // Marca que o player já renderizou uma faixa nesta instância → habilita o warm
  // hold (troca de faixa sem desmontar a UI) nas próximas trocas.
  useEffect(() => {
    if (info && selectedFile >= 0) everReadyRef.current = true
  }, [info, selectedFile])

  // Poll progress every 2s while modal is open
  useEffect(() => {
    if (!info?.infoHash) return
    const tick = () => {
      // Skip while the tab is hidden (áudio em background é o caso comum): cada
      // streamInfo reconstrói o buildInfo do torrent (dezenas de BytesCompleted
      // num pacote multi-arquivo). Volta a atualizar sozinho ao focar a aba.
      if (document.hidden) return
      streamInfo(info.infoHash).then(setInfo).catch(() => {})
    }
    pollRef.current = globalThis.setInterval(tick, 2000)
    return () => {
      if (pollRef.current) globalThis.clearInterval(pollRef.current)
    }
  }, [info?.infoHash])

  // Viewer lease: hold a lease on the active torrent while it's open and release
  // it when the hash changes (playlist/autoplay reuses this instance) or on
  // unmount. The backend keeps the torrent alive while ≥1 viewer holds a lease
  // (so a co-watcher closing one tab doesn't kill the others) and drops a
  // stream-only torrent shortly after the LAST viewer leaves — instead of
  // seeding idly until the reaper. local- hashes aren't streamer torrents.
  useEffect(() => {
    const hash = info?.infoHash
    if (!hash || hash.startsWith('local-')) return
    streamViewerOpen(hash).catch(() => {})
    return () => { streamViewerClose(hash).catch(() => {}) }
  }, [info?.infoHash])

  // onPlaying do <video>: marca o `blessed` (1ª reprodução iniciada por gesto no
  // iOS) UMA vez e loga. A partir daí o auto-avanço pode tocar programaticamente —
  // o grant per-element da Apple persiste no src-swap ("Auto-play restrictions are
  // granted on a per-element basis" + "change the source... instead of creating
  // multiple media elements"). Idempotente: onPlaying dispara a cada retomada.
  const handlePlaybackStarted = () => {
    if (blessed) return
    clientLog('info', 'player', 'blessed: 1ª reprodução iniciada (auto-avanço liberado)', {})
    setBlessed(true)
  }

  return { handlePlaybackStarted }
}
