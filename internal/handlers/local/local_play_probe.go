package local

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// LocalPlayResp tells the frontend HOW to load the source — either as a direct
// progressive download (browser handles the container/codec natively) or as an
// HLS playlist (we transcode on the fly). Mirrors the torrent-side decision so
// the player can stay codec-agnostic.
type LocalPlayResp struct {
	Kind      string `json:"kind"`             // "direct" | "hls"
	URL       string `json:"url"`              // ready-to-use URL with ?token= when applicable
	Reason    string `json:"reason,omitempty"` // why HLS was chosen (codec/container) — debugging aid
	VCodec    string `json:"vcodec,omitempty"`
	ACodec    string `json:"acodec,omitempty"`
	Container string `json:"container,omitempty"`
	// LibraryID is the Continue-Watching row for this local file (0 when not
	// tracked). The frontend uses it to save/resume playback position, just like
	// torrents — so local audio/video gets resume + shows in Continue Watching.
	LibraryID int `json:"libraryId,omitempty"`
}

// browserSafeContainers / browserSafeVideoCodecs / browserSafeAudioCodecs:
// conservative whitelists for direct-play. Anything outside requires transcode.
// Goals:
//   - MKV → HLS unconditionally (Safari refuses it; Chrome/Edge tolerate it via
//     experimental support but it's not in any spec, and AC3/DTS audio still
//     breaks playback even when the container loads).
//   - HEVC/AV1/VP9-in-MP4 → HLS (Chrome/Safari progress on AV1 is patchy; HEVC
//     in <video> only works on Safari with hardware decode, never on Chrome
//     desktop).
//   - AC3/EAC3/DTS audio → HLS (no browser plays these inline).
var browserSafeContainers = map[string]bool{
	"mp4":  true,
	"m4v":  true,
	"mov":  true,
	"webm": true,
	"isom": true, // ffprobe sometimes reports the brand
	"mp42": true,
	"qt":   true,
}

var browserSafeVideoCodecs = map[string]bool{
	"h264": true,
	"vp8":  true,
	"vp9":  true, // good Chrome support; Safari 14+ via WebM
}

var browserSafeAudioCodecs = map[string]bool{
	"aac":    true,
	"mp3":    true,
	"opus":   true,
	"vorbis": true,
}

// probeLocal runs ffprobe on a local file and returns the container short name
// plus the FIRST video and audio codec names. Fast (a few hundred ms typical);
// happens once when the user clicks Play.
type localProbe struct {
	Container   string
	VideoCodec  string
	AudioCodec  string
	DurationSec float64 // 0 when ffprobe couldn't determine it
}

func probeLocalFile(ctx context.Context, path string) (localProbe, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Reuse the unified probe (streamer.ProbeLocal → parseProbeOutput) instead of
	// a second ffprobe invocation + parser. localProbe only needs a subset:
	// DurationSec is reused by the HLS session to skip the slow 30s seekable probe
	// (the rclone/Drive latency win); Container/VideoCodec/AudioCodec drive the
	// direct-vs-HLS decision.
	res, err := streamer.ProbeLocal(cctx, path)
	if err != nil {
		return localProbe{}, err
	}
	p := localProbe{
		DurationSec: res.DurationSec,
		Container:   res.Container,
		VideoCodec:  res.VideoCodec,
	}
	// AudioCodec is the FIRST audio stream (res.AudioCodec is the DEFAULT track,
	// which would differ for multi-audio files) — preserve prior semantics.
	if len(res.Audio) > 0 {
		p.AudioCodec = strings.ToLower(res.Audio[0].Codec)
	}
	return p, nil
}

// parseDurationSec parses ffprobe's format.duration (seconds as a string),
// returning 0 when it's empty or unparseable.
func parseDurationSec(s string) float64 {
	if s == "" {
		return 0
	}
	if d, err := strconv.ParseFloat(s, 64); err == nil && d > 0 {
		return d
	}
	return 0
}

// firstFormatName takes ffprobe's comma-separated format_name and returns the
// canonical first entry, lowercased ("matroska,webm" → "matroska").
func firstFormatName(fn string) string {
	if fn == "" {
		return ""
	}
	first := fn
	if i := strings.IndexByte(first, ','); i >= 0 {
		first = first[:i]
	}
	return strings.ToLower(first)
}

// classifyForBrowser decides whether a local file is direct-playable by a
// generic <video> element. Returns (directPlay, reason). The reason is shown to
// the client when HLS is chosen so we can debug why (e.g. "container=matroska").
func classifyForBrowser(p localProbe) (bool, string) {
	if !browserSafeContainers[p.Container] {
		return false, "container=" + p.Container
	}
	if p.VideoCodec != "" && !browserSafeVideoCodecs[p.VideoCodec] {
		return false, "vcodec=" + p.VideoCodec
	}
	if p.AudioCodec != "" && !browserSafeAudioCodecs[p.AudioCodec] {
		return false, "acodec=" + p.AudioCodec
	}
	return true, ""
}
