package transcode

import "fmt"

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

// bitrateForHeight / levelIdcForHeight: hardcoded per tier (not computed from
// pixels) so BANDWIDTH/CODECS in the master can't drift into values that break
// ABR in hls.js/Safari.
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

func mkVariant(h int) Variant {
	return Variant{Height: h, VBitrateK: bitrateForHeight(h), Level: levelIdcForHeight(h)}
}

// variantLadder returns the ABR ladder for a source of the given height,
// ordered highest→lowest. 4K sources get three rungs (1080/720/480) because the
// browser's built-in H.264 decoder won't play 4K directly; a 1080p source gets
// two (1080/720 — CA-2.1 requires ≥2); a sub-1080p source gets a single rung at
// its native height (no upscale) — the handler serves that as a legacy media
// playlist, not a one-rung master. Unknown height (0) returns the legacy
// single-variant sentinel (Height 0).
func variantLadder(srcHeight int) []Variant {
	switch {
	case srcHeight >= 2160:
		return []Variant{mkVariant(1080), mkVariant(720), mkVariant(480)}
	case srcHeight >= 1080:
		return []Variant{mkVariant(1080), mkVariant(720)}
	case srcHeight > 0:
		return []Variant{mkVariant(srcHeight)}
	default:
		return []Variant{{Height: 0, VBitrateK: 0, Level: 52}}
	}
}
