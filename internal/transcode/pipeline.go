package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
)

// Options describes how to transcode one stream segment.
// All fields are optional — empty means "keep original / passthrough".
type Options struct {
	AudioTrack    int    // absolute stream index for `-map 0:<n>` (-1 = first audio)
	SubBurnTrack  int    // -1 = none; otherwise absolute stream index for hardsub burn-in
	VideoCodec    string // "" = copy; "h264" / "hevc" = transcode video
	AudioCodec    string // "" = copy; "aac" = transcode audio
	Container     string // "mp4" | "matroska" | "webm" — default "mp4"
	SourceVCodec  string // optional hint about source video codec (for hwaccel selection)
}

// Run pipes an input ReadSeeker through ffmpeg with the chosen options and streams to w.
// On failure, the stderr tail is logged so we can diagnose pipeline issues.
//
// Pre-warm: we read a small chunk (~256 KiB) from the input before invoking
// ffmpeg, then concatenate that buffer with the rest of the stream. This catches
// two failure modes early:
//
//   1) anacrolix Reader returns immediately with an error (torrent dropped,
//      reader closed, piece 0 not available). We return 503 with a clear
//      message instead of letting ffmpeg parse-error on EOF.
//   2) Source bytes arrive too late and ffmpeg parses corrupt input. Pre-warm
//      means by the time ffmpeg sees byte 0, we already have a valid prefix.
//
// 256 KiB is enough to cover MKV/MP4 headers + first cluster on most files,
// while staying small enough that the warm-up doesn't add noticeable latency
// to the user-visible "Loading..." spinner.
func Run(ctx context.Context, in io.Reader, w http.ResponseWriter, opts Options) error {
	caps := Cached()
	if caps == nil {
		return errors.New("transcode: capabilities not probed yet")
	}

	const prewarmBytes = 256 * 1024
	prewarm := make([]byte, prewarmBytes)
	n, prewarmErr := io.ReadFull(in, prewarm)
	if n == 0 {
		// Reader gave us nothing — torrent likely dropped or piece 0 stalled.
		// Surface a clean error before ffmpeg gets a chance to misreport.
		if prewarmErr == nil {
			prewarmErr = io.EOF
		}
		log.Printf("transcode: pre-warm got 0 bytes: %v", prewarmErr)
		return fmt.Errorf("transcode: source reader returned no data: %w", prewarmErr)
	}
	if prewarmErr != nil && prewarmErr != io.EOF && prewarmErr != io.ErrUnexpectedEOF {
		log.Printf("transcode: pre-warm error after %d bytes: %v", n, prewarmErr)
	}
	// Re-front the prefix; if file is smaller than prewarmBytes, EOF from the
	// upstream stops ffmpeg naturally.
	in = io.MultiReader(bytes.NewReader(prewarm[:n]), in)

	preferred := caps.Preferred
	if opts.VideoCodec == "hevc" {
		preferred = caps.PreferredHE
	}

	// Container default — mp4 fragmented is the most browser-friendly streaming container
	container := opts.Container
	if container == "" {
		container = "mp4"
	}

	args := []string{"-hide_banner", "-loglevel", "warning"}

	// HW decode: only when (a) we're re-encoding (b) source codec is GPU-decodable
	// (c) chosen encoder is GPU. Skip otherwise to avoid format-mismatch errors.
	if opts.VideoCodec != "" && shouldUseHWDecode(opts.SourceVCodec, preferred) {
		args = append(args, hwaccelDecodeArgs(preferred)...)
	}

	args = append(args, "-i", "pipe:0")

	// Mapping — explicit video + audio only. Negative flags below strip
	// anything ffmpeg would otherwise auto-copy:
	//   -sn          drop subtitle streams (e.g., MKV's embedded SRT)
	//   -dn          drop data streams (e.g., MP4 timed text)
	//   -map_chapters -1 drop chapters (ffmpeg copies them by default as a
	//                   `bin_data text` track in MP4 output, which Safari MSE
	//                   pipeline treats as a malformed extra track and rejects
	//                   the whole file with MediaError.SRC_NOT_SUPPORTED)
	//   -map_metadata -1 strip container-level metadata; some chapter title
	//                   metadata leaks through other paths if we only strip
	//                   the stream itself.
	args = append(args, "-map", "0:v:0")
	if opts.AudioTrack >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", opts.AudioTrack))
	} else {
		args = append(args, "-map", "0:a:0?")
	}
	args = append(args, "-sn", "-dn", "-map_chapters", "-1", "-map_metadata", "-1")

	// Subtitle burn-in (heavy — forces video re-encode regardless of VideoCodec request)
	if opts.SubBurnTrack >= 0 {
		args = append(args, "-filter_complex",
			fmt.Sprintf("[0:v:0][0:%d]overlay[v]", opts.SubBurnTrack),
			"-map", "[v]",
		)
		if opts.VideoCodec == "" {
			opts.VideoCodec = "h264"
		}
	}

	// Video codec
	switch opts.VideoCodec {
	case "":
		args = append(args, "-c:v", "copy")
	case "h264":
		args = append(args, "-c:v", caps.Preferred)
		args = append(args, encoderPresetArgs(caps.Preferred)...)
		// Force 8-bit yuv420p. ALL H.264 encoders we support — libx264, h264_nvenc,
		// h264_vaapi, h264_qsv, h264_videotoolbox — require 8-bit input. The previous
		// version skipped this flag for _nvenc thinking NVENC handles 10-bit natively;
		// in practice NVENC h264 errors with "10 bit encode not supported" on x265
		// 10-bit sources (common in BluRay rips like Breaking Bad). The conversion is
		// effectively free vs the encode cost.
		args = append(args, "-pix_fmt", "yuv420p")
		// Safari-friendly profile/level. Safari macOS validates MP4 codec
		// strings strictly; H.264 High profile with NVENC defaults sometimes
		// emits a profile string Safari doesn't parse. Main profile @ 4.0
		// supports 1080p / 60fps (covers everything we serve) and matches the
		// `avc1.4d402a` codec string Safari accepts unconditionally.
		args = append(args, "-profile:v", "main", "-level:v", "4.0")
		// GOP / B-frame controls for fragmented MP4 streaming.
		// `-g 60` puts a keyframe every ~2 seconds — combined with the
		// `+frag_keyframe` movflag, each MP4 fragment is ~2s long. Without
		// this, NVENC's default GOP (~250 frames = 10s @ 25fps) creates
		// massive fragments and Safari's media buffer can't fill fast enough
		// to start playback before its internal timeout.
		// `-bf 0` disables B-frames; Safari's MSE pipeline mishandles B-frames
		// in fragmented MP4 (audio/video desync, decode order vs presentation
		// order issues). Tiny size hit (~5% bigger), huge compat win.
		args = append(args, "-g", "60", "-bf", "0")
	case "hevc":
		args = append(args, "-c:v", caps.PreferredHE)
		args = append(args, encoderPresetArgs(caps.PreferredHE)...)
	default:
		args = append(args, "-c:v", opts.VideoCodec)
	}

	// Audio codec — when AAC explicitly requested or container is MP4 (only takes AAC reliably)
	audioCodec := opts.AudioCodec
	if audioCodec == "" && container == "mp4" {
		audioCodec = "aac"
	}
	switch audioCodec {
	case "":
		args = append(args, "-c:a", "copy")
	case "aac":
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	default:
		args = append(args, "-c:a", audioCodec)
	}

	// Streaming-friendly flags
	if container == "mp4" {
		// Fragmented MP4 — works in a pipe, no need to seek output to write moov.
		//
		// IMPORTANT: do NOT add `+faststart` here. faststart triggers a second
		// pass that re-opens the output file and shuffles atoms; pipe output
		// has no "re-open", so the result is a malformed MP4. Chrome tolerates
		// it, Safari rejects with MediaError.SRC_NOT_SUPPORTED (networkState=3).
		// The combo below is the documented streaming-fragmented-MP4 set:
		//   +empty_moov     — writes a placeholder moov at start (no seek needed)
		//   +frag_keyframe  — starts a new fragment at every keyframe
		//   +default_base_moof — emits the default_base_is_moof box, required
		//                        by some Safari/iOS versions
		args = append(args,
			"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
			"-f", "mp4",
		)
	} else {
		args = append(args, "-f", container)
	}
	args = append(args, "-y", "pipe:1")

	w.Header().Set("Content-Type", containerMime(container))
	// Lie about range support. Safari's <video> element refuses to play a
	// progressive video source when the server explicitly says
	// `Accept-Ranges: none` — its MSE pipeline treats it as un-seekable junk
	// and surfaces MediaError.SRC_NOT_SUPPORTED (networkState=3) without
	// even fetching bytes. We can't actually honour Range requests on a
	// pipe-fed ffmpeg, but Safari's initial request is `Range: bytes=0-`
	// which we DO satisfy (we always start at byte 0). Subsequent seeks
	// would 416, but at least playback starts. Chrome/Edge ignore the
	// header either way — this only helps Safari and doesn't hurt anyone.
	w.Header().Set("Accept-Ranges", "bytes")

	cmd := exec.CommandContext(ctx, caps.FFmpegPath, args...)
	cmd.Stdin = in
	cmd.Stdout = w
	stderr := &strings.Builder{}
	cmd.Stderr = stderr

	log.Printf("transcode: ffmpeg %s", strings.Join(args, " "))

	if err := cmd.Run(); err != nil {
		tail := lastLines(stderr.String(), 8)
		log.Printf("transcode: ffmpeg FAILED: %v\nstderr:\n%s", err, tail)
		return fmt.Errorf("ffmpeg failed: %w (last stderr: %s)", err, lastLine(stderr.String()))
	}
	return nil
}

// shouldUseHWDecode returns true only when GPU decode is safe + compatible with chosen encoder.
// For most cases (already-H.264 source going through h264_nvenc), CPU decode is safer.
func shouldUseHWDecode(sourceCodec, encoder string) bool {
	// We only set up CUDA decode when source is clearly HEVC and encoder is NVENC.
	// For H.264 sources, software decode pipes into NVENC just fine.
	if !strings.HasSuffix(encoder, "_nvenc") {
		return false
	}
	switch strings.ToLower(sourceCodec) {
	case "hevc", "h265", "vp9", "av1":
		return true
	}
	return false
}

func hwaccelDecodeArgs(encoder string) []string {
	switch {
	case strings.HasSuffix(encoder, "_nvenc"):
		return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
	case strings.HasSuffix(encoder, "_vaapi"):
		return []string{"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi"}
	case strings.HasSuffix(encoder, "_qsv"):
		return []string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv"}
	case strings.HasSuffix(encoder, "_videotoolbox"):
		return []string{"-hwaccel", "videotoolbox"}
	}
	return nil
}

func encoderPresetArgs(encoder string) []string {
	switch {
	case strings.HasSuffix(encoder, "_nvenc"):
		return []string{"-preset", "p4", "-cq", "23"}
	case strings.HasSuffix(encoder, "_vaapi"):
		return []string{"-compression_level", "7", "-qp", "23"}
	case strings.HasSuffix(encoder, "_qsv"):
		return []string{"-preset", "medium", "-global_quality", "23"}
	case encoder == "libx264" || encoder == "libx265":
		return []string{"-preset", "veryfast", "-crf", "23"}
	}
	return nil
}

func containerMime(c string) string {
	switch c {
	case "mp4":
		return "video/mp4"
	case "matroska":
		return "video/x-matroska"
	case "webm":
		return "video/webm"
	}
	return "application/octet-stream"
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.LastIndex(s, "\n"); idx >= 0 {
		s = s[idx+1:]
	}
	if len(s) > 200 {
		s = s[len(s)-200:]
	}
	return s
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
