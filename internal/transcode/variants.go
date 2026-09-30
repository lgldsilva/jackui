package transcode

import "fmt"

// VariantWidth derives a rung's pixel width from the source aspect ratio,
// rounded to an even number (yuv420p requires it). 0 when the source dims are
// unknown. Shared by the ladder (level selection) and the master's RESOLUTION
// so both always agree on the output frame size.
func VariantWidth(srcW, srcH, variantH int) int {
	if srcW <= 0 || srcH <= 0 || variantH <= 0 {
		return 0
	}
	w := srcW * variantH / srcH
	if w%2 != 0 {
		w++
	}
	return w
}

// h264Level is one row of the H.264 level table, restricted to the levels this
// ladder emits. MaxFS caps macroblocks per frame (frame SIZE); MaxMBPS caps
// macroblocks per second (size × frame rate).
type h264Level struct {
	idc     int
	maxFS   int
	maxMBPS int
}

// h264Levels is ordered ascending so the first row that fits is the minimum
// valid level. Frames are assumed ≤30fps (the probe does not expose frame
// rate; MaxMBPS is checked at 30 — the common film/TV ceiling).
var h264Levels = []h264Level{
	{idc: 30, maxFS: 1620, maxMBPS: 40500},    // L3.0
	{idc: 31, maxFS: 3600, maxMBPS: 108000},   // L3.1
	{idc: 40, maxFS: 8192, maxMBPS: 245760},   // L4.0
	{idc: 42, maxFS: 8704, maxMBPS: 522240},   // L4.2
	{idc: 50, maxFS: 22080, maxMBPS: 589824},  // L5.0
	{idc: 51, maxFS: 36864, maxMBPS: 983040},  // L5.1
	{idc: 52, maxFS: 36864, maxMBPS: 2073600}, // L5.2
}

// levelIdcForSize picks the minimum H.264 level whose limits fit the OUTPUT
// frame. A level constrains the whole frame (ceil(w/16)×ceil(h/16)
// macroblocks), not just the height: a cinemascope 1920×804 is ~6120 MBs and
// needs L4.0, while 1280×720 (3600 MBs) still fits L3.1. Hardware encoders
// (NVENC) validate this and abort with "Invalid Level" when it doesn't match.
// Unknown width (0) falls back to the height-only mapping.
func levelIdcForSize(w, h int) int {
	if w <= 0 {
		return levelIdcForHeight(h)
	}
	mbs := ((w + 15) / 16) * ((h + 15) / 16)
	for _, l := range h264Levels {
		if l.maxFS >= mbs && l.maxMBPS >= mbs*30 {
			return l.idc
		}
	}
	return 52 // beyond L5.2 — unreachable with the rung heights this ladder emits
}

// Variant is one rung of the HLS ABR ladder (multi-resolution master, Phase 2).
// Height caps the scale (never upscales); VBitrateK is the video -maxrate in
// kbit/s; Level is the H.264 level_idc (e.g. 40 = L4.0, 31 = L3.1) fed both to
// ffmpeg (-level:v) and advertised in the master's CODECS — so the browser's
// pre-download compatibility check (Safari/hls.js) matches the actual bitstream.
//
// Height == 0 is the LEGACY single-variant sentinel: default cap 1080p, level
// 5.2, no explicit bitrate cap — byte-for-byte the pre-Phase-2 behaviour. It is
// only produced when the source height is unknown and is never placed in a
// master (a master is built solely for a ladder of ≥2 variants).
type Variant struct {
	Height    int
	VBitrateK int
	Level     int
}

// IsDefault reports the legacy single-variant sentinel (unknown source height).
func (v Variant) IsDefault() bool { return v.Height == 0 }

// LevelStr renders the H.264 level for ffmpeg's -level:v (e.g. 40 → "4.0").
func (v Variant) LevelStr() string { return fmt.Sprintf("%d.%d", v.Level/10, v.Level%10) }

// Codecs is the RFC 6381 CODECS attribute for the master's EXT-X-STREAM-INF:
// H.264 Main profile (0x4d) + constraint flags (0x40) + this level, plus AAC-LC
// (mp4a.40.2). Matching the advertised level to the encoded -level:v is what
// keeps a low-end device from skipping a rung it could actually decode.
func (v Variant) Codecs() string { return fmt.Sprintf("avc1.4d40%02x,mp4a.40.2", v.Level) }

// Bandwidth is the EXT-X-STREAM-INF BANDWIDTH (peak bits/s): video cap + AAC
// (~192k) + ~10% container/overhead. Deterministic so ABR selection is stable.
func (v Variant) Bandwidth() int { return (v.VBitrateK + 192) * 1100 }

// bitrateForHeight / levelIdcForHeight: hardcoded per height tier (bitrate
// only; the level fallback below covers unknown widths) so BANDWIDTH in the
// master can't drift into values that break ABR in hls.js/Safari.
func bitrateForHeight(h int) int {
	switch {
	case h >= 1080:
		return 5000
	case h >= 720:
		return 2800
	case h >= 480:
		return 1400
	default:
		return 800
	}
}

// levelIdcForHeight is the height-only level mapping, used only when the
// source width is unknown (probe failed to report dims). It assumes 16:9 —
// the width-aware path (levelIdcForSize) is the correct one for widescreen
// sources, whose frames carry far more macroblocks than their height tier
// suggests.
func levelIdcForHeight(h int) int {
	switch {
	case h >= 1080:
		return 40 // L4.0
	case h >= 720:
		return 31 // L3.1
	default:
		return 30 // L3.0 (≤480p)
	}
}

func mkVariant(srcW, srcH, h int) Variant {
	// Level must be derived from the rung's OUTPUT frame (width comes from the
	// source aspect ratio), not from the height tier alone — see levelIdcForSize.
	return Variant{
		Height:    h,
		VBitrateK: bitrateForHeight(h),
		Level:     levelIdcForSize(VariantWidth(srcW, srcH, h), h),
	}
}

// VariantLadder is the exported entry point handlers use to turn a probed
// source (width + height) into the ABR ladder (the master builder + variant/
// segment handlers live in the handlers package). See variantLadder.
func VariantLadder(srcW, srcH int) []Variant { return variantLadder(srcW, srcH) }

// variantLadder returns the ABR ladder for a source of the given size,
// ordered highest→lowest. 4K sources get three rungs (1080/720/480) because the
// browser's built-in H.264 decoder won't play 4K directly; a 1080p source gets
// two (1080/720 — CA-2.1 requires ≥2); a sub-1080p source gets a single rung at
// its native height (no upscale) — the handler serves that as a legacy media
// playlist, not a one-rung master. Unknown height (0) returns the legacy
// single-variant sentinel (Height 0).
func variantLadder(srcW, srcH int) []Variant {
	switch {
	case srcH >= 2160:
		return []Variant{mkVariant(srcW, srcH, 1080), mkVariant(srcW, srcH, 720), mkVariant(srcW, srcH, 480)}
	case srcH >= 1080:
		return []Variant{mkVariant(srcW, srcH, 1080), mkVariant(srcW, srcH, 720)}
	case srcH > 0:
		return []Variant{mkVariant(srcW, srcH, srcH)}
	default:
		return []Variant{{Height: 0, VBitrateK: 0, Level: 52}}
	}
}
