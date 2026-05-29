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

	if opts.VideoCodec != "" && shouldUseHWDecode(opts.SourceVCodec, preferred) {
		args = append(args, hwaccelDecodeArgs(preferred)...)
	}

	args = append(args, "-i", pipe0)

	args = append(args, "-map", "0:v:0")
	if opts.AudioTrack >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", opts.AudioTrack))
	} else {
		args = append(args, "-map", "0:a:0?")
	}
	args = append(args, "-sn", "-dn", "-map_chapters", "-1", "-map_metadata", "-1")

	if opts.SubBurnTrack >= 0 {
		args = append(args, "-filter_complex",
			fmt.Sprintf("[0:v:0][0:%d]overlay[v]", opts.SubBurnTrack),
			"-map", "[v]",
		)
		if opts.VideoCodec == "" {
			opts.VideoCodec = "h264"
		}
	}

	args = appendVideoCodecArgs(args, caps, preferred, opts.VideoCodec)
	args = appendAudioCodecArgs(args, opts.AudioCodec, container)
	args = appendContainerArgs(args, container)
	args = append(args, "-y", pipe1)
	return args
}

func appendVideoCodecArgs(args []string, caps *Capabilities, preferred, videoCodec string) []string {
	switch videoCodec {
	case "":
		return append(args, "-c:v", "copy")
	case "h264":
		args = append(args, "-c:v", caps.Preferred)
		args = append(args, encoderPresetArgs(caps.Preferred)...)
		args = append(args, "-pix_fmt", "yuv420p")
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
