// Lógica pura de visibilidade do overlay de "carregando" no início de uma faixa.
// Extraída do VideoPlayerElement pra (a) manter a complexidade cognitiva do
// componente abaixo do gate (a cadeia && pesava no corpo) e (b) ser testável.

export type StartOverlayInput = {
  // erro de mídia → a UI de erro assume, não mostra spinner.
  videoError: boolean
  // motor gapless ativo → o <video> está mudo/sem-src (bufferedEnd fica 0 sempre),
  // então o spinner não reflete carregamento real.
  engineActive: boolean
  // troca de faixa numa sessão já populada (warm switch) → a capa/seekbar
  // continuam; suprime o spinner que piscava a cada faixa.
  suppressStartOverlay: boolean
  // iOS-áudio (tap-to-play): nada bufferiza sem gesto, então o spinner giraria pra
  // sempre. Suprime o spinner e dá lugar ao overlay "Tocar".
  disableNativeAutoplay: boolean
  currentTime: number
  bufferedEnd: number
}

// O spinner de start só aparece numa abertura FRIA (primeira faixa da instância),
// enquanto nada tocou nem bufferizou ainda — e nunca no iOS-áudio (lá o overlay
// "Tocar" assume, senão o spinner giraria eternamente à espera de um gesto).
export function shouldShowStartOverlay(o: StartOverlayInput): boolean {
  return !o.videoError && !o.engineActive && !o.suppressStartOverlay && !o.disableNativeAutoplay
    && o.currentTime === 0 && o.bufferedEnd === 0
}

export type StartAudioOverlayInput = {
  // iOS-áudio: a Apple exige gesto pra tocar; o overlay "Tocar" é o gatilho.
  disableNativeAutoplay: boolean
  // o usuário já tocou no overlay (dispensa imediata, antes do playhead andar).
  startOverlayDismissed: boolean
  videoError: boolean
  // prompt de resume tem seus próprios botões (continuar/recomeçar) — não sobrepor.
  showResumePrompt: boolean
  currentTime: number
}

// O overlay "Tocar" (iOS-áudio) aparece quando a faixa abriu mas ainda não tocou:
// só no iOS-áudio, antes do tap (não dispensado), sem erro, sem prompt de resume e
// com o playhead em 0. O tap nele (gesto) é o que de fato inicia no iPhone/iPad.
export function shouldShowStartAudioOverlay(o: StartAudioOverlayInput): boolean {
  return o.disableNativeAutoplay && !o.startOverlayDismissed
    && !o.videoError && !o.showResumePrompt && o.currentTime === 0
}
