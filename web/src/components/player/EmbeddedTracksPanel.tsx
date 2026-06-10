import { AlertCircle, Subtitles, Check, Cpu, Volume2, Flame } from 'lucide-react'
import { StreamProbe, SidecarSubtitle } from '../../api/client'
import { audioTrackTitle, subBtnClass } from './playerFormat'

type EmbeddedTracksPanelProps = {
  readonly probe: StreamProbe
  readonly sidecars: SidecarSubtitle[]
  readonly transcodeAudio: number | null
  readonly forceH264: boolean
  readonly burnSubTrack: number | null
  readonly isTranscoded: boolean
  readonly sidecarIdx: number | null
  readonly embeddedSub: number | null
  readonly clearCustomSub: () => void
  readonly setTranscodeAudio: (v: number | null) => void
  readonly setForceH264: (fn: (prev: boolean) => boolean) => void
  readonly setBurnSubTrack: (v: number | null) => void
  readonly setSidecarIdx: (v: number | null) => void
  readonly setEmbeddedSub: (v: number | null) => void
  readonly setSubActive: (v: string | null) => void
  readonly setAutoSource: (v: 'hash' | 'title' | 'embedded' | null) => void
}

// Embedded tracks panel (audio + subtitles inside the file, plus sidecar .srt
// files shipped alongside the video in the torrent).
export function EmbeddedTracksPanel({
  probe,
  sidecars,
  transcodeAudio,
  forceH264,
  burnSubTrack,
  isTranscoded,
  sidecarIdx,
  embeddedSub,
  clearCustomSub,
  setTranscodeAudio,
  setForceH264,
  setBurnSubTrack,
  setSidecarIdx,
  setEmbeddedSub,
  setSubActive,
  setAutoSource,
}: EmbeddedTracksPanelProps) {
  return (
    <div className="px-3 sm:px-4 py-3 border-b border-default flex flex-col gap-3">
      {/* Audio tracks — clicking a non-default triggers transcoded remux */}
      {probe.audio.length > 1 && (
        <div>
          <p className="text-xs text-text-muted mb-1.5 flex items-center gap-2">
            <Volume2 className="w-3 h-3" />
            Faixas de áudio ({probe.audio.length})
            {transcodeAudio !== null && (
              <span className="text-[10px] text-purple-700 dark:text-purple-300 bg-purple-500/15 border border-purple-500/30 px-1.5 py-0.5 rounded">
                <Cpu className="w-2.5 h-2.5 inline mr-0.5" />GPU encoding
              </span>
            )}
          </p>
          <div className="flex flex-wrap gap-1">
            <button
              onClick={() => setTranscodeAudio(null)}
              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                transcodeAudio === null
                  ? 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/30'
                  : 'bg-surface-secondary text-text-muted border-default hover:text-text-primary'
              }`}
              title="Faixa padrão do arquivo (direct play, com seek completo)"
            >
              Padrão
            </button>
            {probe.audio.map(a => (
              <button
                key={a.index}
                onClick={() => setTranscodeAudio(a.index)}
                title={audioTrackTitle(a)}
                className={`text-[11px] px-2 py-1 rounded border transition-colors ${(() => {
                  if (transcodeAudio === a.index) return 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30'
                  if (a.default) return 'bg-blue-500/10 text-blue-400 border-blue-500/20 hover:bg-blue-500/20'
                  return 'bg-surface-tertiary/40 text-text-secondary border-default hover:text-text-primary'
                })()}`}
              >
                {a.language ? a.language.toUpperCase() : '??'}
                <span className="text-text-muted ml-1">{a.codec}{a.channels ? `·${a.channels}ch` : ''}</span>
                {a.default && <span className="ml-1 text-[9px]">★</span>}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Force H.264 toggle — useful for HEVC files in Chrome */}
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <button
          onClick={() => setForceH264(v => !v)}
          title="Re-encoda vídeo para H.264 — útil quando o codec original é HEVC e o browser não decodifica"
          className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg border transition-colors ${
            forceH264
              ? 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30'
              : 'bg-surface-tertiary/50 text-text-secondary border-default hover:text-text-primary'
          }`}
        >
          <Cpu className="w-3.5 h-3.5" />
          Forçar H.264
          {forceH264 && <Check className="w-3 h-3" />}
        </button>

        {/* Stream mode indicator */}
        {isTranscoded && (
          <span className="text-[11px] text-yellow-400 flex items-center gap-1">
            <AlertCircle className="w-3 h-3" />
            Stream transcoded — seek limitado
          </span>
        )}
      </div>

      {/* Sidecar subtitles (.srt files alongside the video in the torrent) */}
      {sidecars.length > 0 && (
        <div>
          <p className="text-xs text-text-muted mb-1.5 flex items-center gap-2">
            <Subtitles className="w-3 h-3" />
            Legendas no torrent ({sidecars.length}) <span className="text-[10px] text-text-muted italic">— arquivos .srt/.vtt</span>
          </p>
          <div className="flex flex-wrap gap-1">
            <button
              onClick={() => {
                setSidecarIdx(null)
                clearCustomSub()
              }}
              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                sidecarIdx === null
                  ? 'bg-surface-tertiary text-text-primary border-strong'
                  : 'bg-surface-secondary text-text-muted border-default hover:text-text-primary'
              }`}
            >
              Nenhuma
            </button>
            {sidecars.map(s => (
              <button
                key={s.index}
                onClick={() => {
                  setSidecarIdx(s.index)
                  setEmbeddedSub(null)
                  setSubActive(null)
                  setAutoSource('embedded')
                  clearCustomSub()
                }}
                title={s.path}
                className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                  sidecarIdx === s.index
                    ? 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/30'
                    : 'bg-surface-tertiary/40 text-text-secondary border-default hover:text-text-primary'
                }`}
              >
                {(s.language || '??').toUpperCase()}
                <span className="text-text-muted ml-1">.{s.format}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Embedded subtitles — pickable (text subs as track, image subs as burn-in) */}
      {probe.subtitles.length > 0 && (
        <div>
          <p className="text-xs text-text-muted mb-1.5 flex items-center gap-2">
            <Subtitles className="w-3 h-3" />
            Legendas embutidas ({probe.subtitles.length})
            {burnSubTrack !== null && (
              <span className="text-[10px] text-orange-700 dark:text-orange-300 bg-orange-500/15 border border-orange-500/30 px-1.5 py-0.5 rounded">
                <Flame className="w-2.5 h-2.5 inline mr-0.5" />Burn-in
              </span>
            )}
          </p>
          <div className="flex flex-wrap gap-1">
            <button
              onClick={() => {
                setEmbeddedSub(null)
                setBurnSubTrack(null)
                clearCustomSub()
              }}
              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                embeddedSub === null && burnSubTrack === null
                  ? 'bg-surface-tertiary text-text-primary border-strong'
                  : 'bg-surface-elevated text-text-muted border-default hover:text-text-primary'
              }`}
            >
              Nenhuma
            </button>
            {probe.subtitles.map((s, i) => {
              const isActive = embeddedSub === s.index || burnSubTrack === s.index
              // Sem tag de língua (comum em releases tipo MeGusta com N subs
              // sem rótulo), o "??" deixa 34 faixas idênticas — usa o título, ou
              // um ordinal "Faixa N" pra serem ao menos distinguíveis.
              const subLabel = s.language ? s.language.toUpperCase() : (s.title || `Faixa ${i + 1}`)
              return (
                <button
                  key={s.index}
                  onClick={() => {
                    clearCustomSub()
                    if (s.image) {
                      // Image sub → burn-in (forces video re-encode)
                      setBurnSubTrack(s.index)
                      setEmbeddedSub(null)
                    } else {
                      // Text sub → extract as VTT
                      setEmbeddedSub(s.index)
                      setBurnSubTrack(null)
                      setSubActive(null)
                      setAutoSource('embedded')
                    }
                  }}
                  title={
                    s.image
                      ? `${s.codec} (imagem) — burn-in via FFmpeg, vai forçar transcode do vídeo`
                      : s.title || s.codec
                  }
                  className={`text-[11px] px-2 py-1 rounded border transition-colors ${subBtnClass(isActive, s.image)}`}
                >
                  {subLabel}
                  <span className="text-text-muted ml-1">{s.codec}</span>
                  {s.forced && <span className="ml-1 text-[9px] text-yellow-400">FORCED</span>}
                  {s.image && <span className="ml-1 text-[9px] text-orange-400">IMG</span>}
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
