import { useEffect, useRef } from 'react'
import { TorrentInfo, LibraryEntry, libraryGet, libraryUpdateResume, isIOS } from '../../api/client'
import { clientLog } from '../../lib/diag'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// Resume-position plumbing: loads the per-user library entry, persists the final
// position on real unmount, and drives the seek/auto-resume/autoplay decision on
// canplay. State (libraryEntryID/resumePosition/showResumePrompt) is owned by the
// modal and passed in so the new-result reset can clear it in one place.
export function useResumePlayback(deps: {
  info: TorrentInfo | null
  incognito: boolean
  selectedFile: number
  initialSeek?: number
  audioMode: boolean
  blessed: boolean
  videoRef: React.RefObject<HTMLVideoElement | null>
  libraryEntryID: number | null
  resumePosition: number | null
  setLibraryEntryID: Setter<number | null>
  setResumePosition: Setter<number | null>
  setShowResumePrompt: Setter<boolean>
}) {
  const {
    info, incognito, selectedFile, initialSeek, audioMode, blessed, videoRef,
    libraryEntryID, resumePosition, setLibraryEntryID, setResumePosition, setShowResumePrompt,
  } = deps

  const loadLibraryEntry = (list: LibraryEntry[], infoHash: string) => {
    const entry = list.find(e => e.infoHash === infoHash)
    if (entry) {
      setLibraryEntryID(entry.id)
      if (entry.resumeSeconds > 30 && entry.durationSeconds > 0 && entry.resumeSeconds < entry.durationSeconds - 30) {
        setResumePosition(entry.resumeSeconds)
      }
    }
  }

  // Mirror the values the unmount cleanup needs into a ref, refreshed every
  // render. This lets the cleanup run ONLY on real unmount (deps: []) while
  // still seeing current values — without it, depending on [libraryEntryID]
  // re-ran the cleanup the moment the library entry loaded mid-playback,
  // calling streamDrop() and KILLING the torrent we were actively streaming
  // (ffmpeg then died with "torrent closed" → "Sem seeds").
  const cleanupRef = useRef<{ readonly infoHash: string; readonly libraryEntryID: number | null; readonly fileIndex: number; readonly incognito: boolean }>({ infoHash: '', libraryEntryID: null, fileIndex: -1, incognito: false })
  useEffect(() => {
    cleanupRef.current = { infoHash: info?.infoHash ?? '', libraryEntryID, fileIndex: selectedFile, incognito }
  })

  // Persist final resume position — ONLY when the modal truly unmounts (user
  // closes/navigates), never on intra-playback state changes. Dropping the
  // torrent is handled by the viewer-lease effect below (keyed on the hash), not
  // here, so switching A→B in the same instance releases A as well.
  useEffect(() => {
    return () => {
      const { libraryEntryID: libID, fileIndex, incognito: wasIncognito } = cleanupRef.current
      const v = videoRef.current
      if (!wasIncognito && libID !== null && v && v.currentTime > 1) {
        // Persist which file was watched so reopening a season pack resumes the
        // same episode (not the torrent's primary file).
        libraryUpdateResume(libID, v.currentTime, v.duration || 0, fileIndex >= 0 ? fileIndex : undefined).catch(() => {})
      }
    }
  }, [])

  // After torrent metadata loads, fetch the library entry to know if we have a saved resume position
  useEffect(() => {
    if (!info?.infoHash || incognito) return
    libraryGet(0).catch(() => {})
    const hash = info.infoHash
    import('../../api/client').then(({ libraryList }) => {
      libraryList({ limit: 100 }).then(list => loadLibraryEntry(list, hash)).catch(() => {})
    })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash])

  // One-shot guard for the URL-supplied seek. Without it we'd re-apply the
  // initial seek every time `canplay` fires (which happens on each format
  // negotiation, transcode fallback, etc.), making it impossible to scrub away.
  const appliedInitialSeekRef = useRef(false)
  // Same idea for the library-driven auto-resume — fire once per file selection
  // and then keep `resumePosition` populated so the "Continuar" button can use
  // it after the user goes back to the start.
  const appliedAutoResumeRef = useRef(false)
  // Autoplay nativo (iOS): dispara uma vez por fonte, no canplay sem-resume.
  const autoplayTriedRef = useRef(false)
  useEffect(() => {
    // Reset whenever a new file is selected so a future URL-driven re-play
    // (e.g., navigating to ?play=X&t=...) re-applies the seek instead of
    // remembering "already done" from the previous file.
    appliedInitialSeekRef.current = false
    appliedAutoResumeRef.current = false
    autoplayTriedRef.current = false
    setShowResumePrompt(false)
  }, [selectedFile, info?.infoHash])

  // Seek once the video can play. Priority:
  //   1. URL-supplied initialSeek (explicit, e.g. shared link with `t=120`)
  //   2. per-user library resumeSeconds (background-saved, silent)
  // iosAudio: caminho ÁUDIO no iPhone/iPad. Gate único do "tap-to-play": no iOS o
  // play() de mídia-com-áudio EXIGE um gesto (regra da Apple), então desligamos o
  // autoplay não-gesto e os nudges, mostramos o overlay "Tocar" e deixamos o tap do
  // usuário iniciar. isIOS() (não isSafariBrowser) pra NÃO regredir o macOS-Safari,
  // que toca com autoplay normal. Só depende de audioMode (prop) → válido aqui.
  const iosAudio = audioMode && isIOS()
  // Autoplay no caminho NATIVO (<video> sem hls.js): o iOS ignora o atributo
  // autoPlay quando há áudio, então tentamos play() explicitamente (com fallback
  // mudo). Uma vez por fonte. Não chamado quando vamos exibir o prompt de resume
  // — aí o usuário escolhe continuar/recomeçar. (O caminho hls.js já trata o
  // autoplay no MANIFEST_PARSED; um play() extra aqui seria no-op idempotente.)
  const maybeAutoplayNative = (v: HTMLVideoElement) => {
    if (autoplayTriedRef.current) return
    autoplayTriedRef.current = true
    // iOS-áudio AINDA NÃO iniciado (não blessed): NÃO tentar autoplay. A Apple proíbe
    // play() de mídia-com-áudio fora de um gesto; um play() não-gesto trava o elemento
    // em readyState 1 e aborta em loop. Deixamos pausado e mostramos o overlay "Tocar"
    // — o tap do usuário (gesto) inicia. DEPOIS de iniciado (blessed), a Apple libera
    // o play() programático → caímos no caminho normal abaixo e a faixa seguinte do
    // álbum toca sozinha (auto-avanço).
    if (iosAudio && !blessed) {
      clientLog('info', 'player', 'iOS: autoplay pulado — aguardando gesto (tap-to-play)', { readyState: v.readyState })
      return
    }
    // DIAGNÓSTICO (temporário): registra qual caminho o autoplay tomou no device,
    // pra cravar a intermitência do iOS — tocou com SOM, caiu no MUDO (sem gesto),
    // ou falhou. Mesma lógica do tryAutoplayMutedFallback + logs.
    clientLog('info', 'player', 'autoplay try', { readyState: v.readyState, file: selectedFile })
    v.play()
      .then(() => clientLog('info', 'player', 'autoplay ok (som)', {}))
      .catch((e) => {
        // AbortError ≠ bloqueio de autoplay (NotAllowedError): o play() foi
        // INTERROMPIDO por um load()/troca de src/remontagem do elemento enquanto
        // ainda estava pendente (no iOS a janela de buffering inicial é longa).
        // NÃO encadear um play() mudo num elemento ainda carregando — isso só
        // agrava o abort e mata o som de vez. Em vez disso, libera o guard
        // one-shot pra o PRÓXIMO loadedmetadata/canplay re-tentar limpo no
        // elemento já estabilizado (com SOM). Era a causa do "tocou e parou /
        // sem som" no iPhone.
        if ((e as { name?: string })?.name === 'AbortError') {
          clientLog('warn', 'player', 'autoplay abortado (load interrompeu) — re-tentará', { err: String(e) })
          autoplayTriedRef.current = false
          return
        }
        clientLog('warn', 'player', 'autoplay bloqueado, tentando mudo', { err: String(e) })
        v.muted = true
        v.play()
          .then(() => clientLog('info', 'player', 'autoplay ok (mudo)', {}))
          .catch((error_) => clientLog('error', 'player', 'autoplay falhou (nem mudo)', { err: String(error_) }))
      })
  }
  const handleVideoCanPlay = () => {
    const v = videoRef.current
    if (!v) return
    if (initialSeek !== undefined && initialSeek > 0 && !appliedInitialSeekRef.current) {
      if (v.currentTime < 1) {
        v.currentTime = initialSeek
      }
      appliedInitialSeekRef.current = true
      // Clear DB resume to avoid the second branch firing on the same canplay
      setResumePosition(null)
      maybeAutoplayNative(v)
      return
    }
    if (resumePosition !== null && v.currentTime < 1 && resumePosition > 30 && !appliedAutoResumeRef.current) {
      appliedAutoResumeRef.current = true
      // Ask instead of silently jumping: the user picks "continue" or "restart"
      // via the overlay (see resume prompt). Mark applied so it only asks once.
      // DIAGNÓSTICO (temporário): este caminho NÃO auto-toca (espera o gesto no
      // prompt) — se aparecer muito, é a causa do "não tocou" em faixas c/ posição.
      clientLog('info', 'player', 'resume prompt mostrado (autoplay pulado)', { resumePosition })
      setShowResumePrompt(true)
      return
    }
    // Sem seek explícito nem prompt de resume → começa a tocar sozinho.
    maybeAutoplayNative(v)
  }

  return { handleVideoCanPlay }
}
