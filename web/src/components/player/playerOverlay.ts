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
  currentTime: number
  bufferedEnd: number
}

// O spinner de start só aparece numa abertura FRIA (primeira faixa da instância),
// enquanto nada tocou nem bufferizou ainda.
export function shouldShowStartOverlay(o: StartOverlayInput): boolean {
  return !o.videoError && !o.engineActive && !o.suppressStartOverlay
    && o.currentTime === 0 && o.bufferedEnd === 0
}
