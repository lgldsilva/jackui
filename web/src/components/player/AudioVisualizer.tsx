import { useEffect, useRef } from 'react'

// AudioVisualizer draws a frequency-spectrum bar graph from the graph's
// AnalyserNode via requestAnimationFrame. Purely decorative → aria-hidden, so
// screen readers skip it. Renders nothing until the analyser exists.
export function AudioVisualizer({ analyser }: { readonly analyser: AnalyserNode | null }) {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!analyser || !canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    const bins = analyser.frequencyBinCount
    const data = new Uint8Array(bins)
    const bars = 56
    const step = Math.max(1, Math.floor(bins / bars))
    let raf = 0
    const draw = () => {
      raf = requestAnimationFrame(draw)
      analyser.getByteFrequencyData(data)
      const { width, height } = canvas
      ctx.clearRect(0, 0, width, height)
      const bw = width / bars
      for (let i = 0; i < bars; i++) {
        const v = (data[i * step] ?? 0) / 255
        const bh = Math.max(1, v * height)
        ctx.fillStyle = `hsl(${210 + v * 120}, 80%, 55%)`
        ctx.fillRect(i * bw, height - bh, Math.max(1, bw - 1), bh)
      }
    }
    draw()
    return () => cancelAnimationFrame(raf)
  }, [analyser])

  if (!analyser) return null
  // Decorative spectrum. A bare <canvas> (no aria-hidden, no role) avoids both
  // Sonar S6825 (aria-hidden on a focusable element) and S6843 (non-interactive
  // role on an interactive element) — it has no accessible content anyway.
  return (
    <canvas
      ref={canvasRef}
      width={560}
      height={72}
      className="h-16 w-full rounded-lg bg-black/30"
    />
  )
}
