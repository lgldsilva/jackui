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
	AudioTrack   int    // absolute stream index for `-map 0:<n>` (-1 = first audio)
	SubBurnTrack int    // -1 = none; otherwise absolute stream index for hardsub burn-in
	VideoCodec   string // "" = copy; "h264" / "hevc" = transcode video
	AudioCodec   string // "" = copy; "aac" = transcode audio
	Container    string // "mp4" | "matroska" | "webm" — default "mp4"
	SourceVCodec string // optional hint about source video codec (for hwaccel selection)
}

// Run pipes an input ReadSeeker through ffmpeg with the chosen options and streams to w.
// On failure, the stderr tail is logged so we can diagnose pipeline issues.
//
// Pre-warm: we read a small chunk (~256 KiB) from the input before invoking
// ffmpeg, then concatenate that buffer with the rest of the stream. This catches
// two failure modes early:
//
//  1. anacrolix Reader returns immediately with an error (torrent dropped,
//     reader closed, piece 0 not available). We return 503 with a clear
//     message instead of letting ffmpeg parse-error on EOF.
//  2. Source bytes arrive too late and ffmpeg parses corrupt input. Pre-warm
//     means by the time ffmpeg sees byte 0, we already have a valid prefix.
//
// 256 KiB is enough to cover MKV/MP4 headers + first cluster on most files,
// while staying small enough that the warm-up doesn't add noticeable latency
// to the user-visible "Loading..." spinner.
func Run(ctx context.Context, in io.Reader, w http.ResponseWriter, opts Options) error {
	caps := Cached()
	if caps == nil {
		return errors.New("transcode: capabilities not probed yet")
	}

	in = prewarmReader(in)
	if in == nil {
		return fmt.Errorf("transcode: source reader returned no data")
	}

	preferred := resolvePreferredEncoder(caps, opts.VideoCodec)
	container := resolveContainer(opts.Container)

	args := buildTranscodeArgs(caps, preferred, container, opts)

	w.Header().Set("Content-Type", containerMime(container))
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

func prewarmReader(in io.Reader) io.Reader {
	const prewarmBytes = 256 * 1024
	prewarm := make([]byte, prewarmBytes)
	n, prewarmErr := io.ReadFull(in, prewarm)
	if n == 0 {
		if prewarmErr == nil {
			prewarmErr = io.EOF
		}
		log.Printf("transcode: pre-warm got 0 bytes: %v", prewarmErr)
		return nil
	}
	if prewarmErr != nil && prewarmErr != io.EOF && prewarmErr != io.ErrUnexpectedEOF {
		log.Printf("transcode: pre-warm error after %d bytes: %v", n, prewarmErr)
	}
	return io.MultiReader(bytes.NewReader(prewarm[:n]), in)
}

func resolvePreferredEncoder(caps *Capabilities, videoCodec string) string {
	if videoCodec == "hevc" {
		return caps.PreferredHE
	}
	return caps.Preferred
}

func resolveContainer(container string) string {
	if container == "" {
		return "mp4"
	}
	return container
}

func buildTranscodeArgs(caps *Capabilities, preferred, container string, opts Options) []string {
	args := []string{ffHideBanner, ffLogLevel, "warning"}

	// VAAPI encoders (h264_vaapi/hevc_vaapi) need their input as VAAPI surfaces.
	// We decode on the GPU and scale_vaapi to ≤1080p + NV12 (8-bit) below. This is
	// REQUIRED so a 10-bit HDR source (p010 — e.g. a 4K HEVC Dolby Vision file) can
	// feed the 8-bit h264_vaapi encoder; without it ffmpeg dies with
	// "Error reinitializing filters" / "Invalid argument". The downscale also keeps
	// realtime 4K transcodes light. SubBurn uses a CPU overlay path, so skip there.
	encoder := encoderForCodec(caps, opts.VideoCodec)
	// Video transcode (not subtitle-burn, which uses a CPU overlay path): decode
	// on the matching backend so frames feed the scale_* filter added below.
	transcodeVideo := opts.VideoCodec != "" && opts.SubBurnTrack < 0
	if transcodeVideo {
		args = append(args, hwDecodeArgsFor(encoder)...)
	}

	args = append(args, "-i", pipe0)

	if opts.SubBurnTrack >= 0 {
		// Burn-in: the output video is the filtergraph result ([v]) and ONLY it.
		// Mapping 0:v:0 as well produced an MP4 with TWO video tracks — the
		// browser's <video> played the first (the one WITHOUT the burned
		// subtitle) after paying the full re-encode cost (audit #411).
		args = append(args, "-filter_complex",
			fmt.Sprintf("[0:v:0][0:%d]overlay[v]", opts.SubBurnTrack),
			"-map", "[v]",
		)
		if opts.VideoCodec == "" {
			opts.VideoCodec = "h264"
		}
	} else {
		args = append(args, "-map", "0:v:0")
	}
	if opts.AudioTrack >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", opts.AudioTrack))
	} else {
		args = append(args, "-map", "0:a:0?")
	}
	args = append(args, "-sn", "-dn", "-map_chapters", "-1", "-map_metadata", "-1")

	if transcodeVideo {
		// Downscale ≤1080p + 8-bit pixel format for the chosen backend (fixes 10-bit
		// HDR sources that the HW h264 encoders can't ingest, and keeps 4K light).
		// (transcodeVideo is false on the burn path, which scales via the overlay.)
		args = append(args, "-vf", videoScaleFilter(encoder))
	}

	args = appendVideoCodecArgs(args, caps, preferred, opts.VideoCodec)
	args = appendAudioCodecArgs(args, opts.AudioCodec, container)
	args = appendContainerArgs(args, container)
	args = append(args, "-y", pipe1)
	return args
}

// encoderForCodec returns the concrete ffmpeg encoder that will be used for the
// requested transcode codec ("" if no video transcode), so callers can branch on
// the backend (e.g. VAAPI needs a hwupload/scale_vaapi filter chain).
func encoderForCodec(caps *Capabilities, videoCodec string) string {
	switch videoCodec {
	case "h264":
		return caps.Preferred
	case "hevc":
		return caps.PreferredHE
	}
	return ""
}

// hwDecodeArgsFor returns the `-hwaccel` decode flags matching the encoder's
// backend, so decoded frames land as the HW surface type its scale_* filter
// expects. Empty for CPU encoders (software decode).
//   - AMD VAAPI:  tested on Radeon RX 6700.
//   - NVIDIA/Intel: analogous to VAAPI but NOT validated on real hardware here.
func hwDecodeArgsFor(encoder string) []string {
	switch {
	case strings.HasSuffix(encoder, "_vaapi"):
		// Keep frames on the GPU (vaapi surfaces) for scale_vaapi.
		return []string{ffHWAccel, "vaapi", "-hwaccel_device", "/dev/dri/renderD128", ffHWAccelOutFormat, "vaapi"}
	case strings.HasSuffix(encoder, "_nvenc"):
		// HW-decode on the GPU but let frames download to system memory (NO
		// -hwaccel_output_format cuda): the container's ffmpeg 4.4.2 lacks
		// scale_cuda's `format=` option, so we scale + convert to 8-bit in
		// software (cheap at ≤1080p) and h264_nvenc re-uploads to encode. Validated
		// inside jackui:nvidia on a GTX 1070.
		return []string{ffHWAccel, "cuda"}
	case strings.HasSuffix(encoder, "_qsv"):
		return []string{ffHWAccel, "qsv", ffHWAccelOutFormat, "qsv"}
	}
	return nil
}

// isHWEncoder reports whether the encoder runs on a GPU/ASIC (so its frames are
// HW surfaces and -pix_fmt yuv420p must NOT be forced).
func isHWEncoder(enc string) bool {
	return strings.HasSuffix(enc, "_vaapi") || strings.HasSuffix(enc, "_nvenc") ||
		strings.HasSuffix(enc, "_qsv") || strings.HasSuffix(enc, "_videotoolbox")
}

// videoScaleFilter caps height at 1080p AND converts to the 8-bit pixel format
// the encoder needs. This is REQUIRED for 10-bit HDR sources (p010 — 4K HEVC
// Dolby Vision): the HW h264 encoders only take 8-bit NV12, and feeding p010
// crashes ffmpeg with "Error reinitializing filters". The downscale also keeps
// realtime 4K transcodes light. min(1080,ih) never upscales smaller sources.
func videoScaleFilter(encoder string) string {
	switch {
	case strings.HasSuffix(encoder, "_vaapi"):
		// Frames are on the GPU (vaapi surfaces) → scale + convert on the GPU.
		return `scale_vaapi=w=-2:h=min(1080\,ih):format=nv12`
	case strings.HasSuffix(encoder, "_qsv"):
		return `scale_qsv=w=-2:h=min(1080\,ih):format=nv12`
	default:
		// NVENC (frames downloaded to sysmem), libx264/libx265, videotoolbox:
		// software scale + 8-bit yuv420p. h264_nvenc uploads sysmem frames itself,
		// so this avoids scale_cuda's `format=` option (missing on ffmpeg 4.4.2).
		return `scale=-2:'min(1080,ih)',format=yuv420p`
	}
}

func appendVideoCodecArgs(args []string, caps *Capabilities, preferred, videoCodec string) []string {
	switch videoCodec {
	case "":
		return append(args, "-c:v", "copy")
	case "h264":
		args = append(args, "-c:v", caps.Preferred)
		args = append(args, encoderPresetArgs(caps.Preferred)...)
		// HW encoders receive NV12 surfaces from their scale_* filter; forcing
		// -pix_fmt yuv420p would clash with the hardware surface format. CPU keeps it.
		if !isHWEncoder(caps.Preferred) {
			args = append(args, "-pix_fmt", "yuv420p")
		}
		args = append(args, "-profile:v", "main", "-level:v", "4.0")
		args = append(args, "-g", "60", "-bf", "0")
		return args
	case "hevc":
		args = append(args, "-c:v", caps.PreferredHE)
		args = append(args, encoderPresetArgs(caps.PreferredHE)...)
		return args
	default:
		return append(args, "-c:v", videoCodec)
	}
}

func appendAudioCodecArgs(args []string, audioCodec, container string) []string {
	if audioCodec == "" && container == "mp4" {
		audioCodec = "aac"
	}
	switch audioCodec {
	case "":
		return append(args, "-c:a", "copy")
	case "aac":
		return append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	default:
		return append(args, "-c:a", audioCodec)
	}
}

func appendContainerArgs(args []string, container string) []string {
	if container == "mp4" {
		return append(args,
			"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
			"-f", "mp4",
		)
	}
	return append(args, "-f", container)
}

func encoderPresetArgs(encoder string) []string {
	switch {
	case strings.HasSuffix(encoder, "_nvenc"):
		return []string{ffPreset, "p4", "-cq", "23"}
	case strings.HasSuffix(encoder, "_vaapi"):
		return []string{"-compression_level", "7", "-qp", "23"}
	case strings.HasSuffix(encoder, "_qsv"):
		return []string{ffPreset, "medium", "-global_quality", "23"}
	case encoder == "libx264" || encoder == "libx265":
		return []string{ffPreset, "veryfast", "-crf", "23"}
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
