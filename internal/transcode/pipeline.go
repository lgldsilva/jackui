package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrSubtitlesFilterUnavailable is returned when the host ffmpeg build lacks
// the libass-based "subtitles" filter required to burn an EXTERNAL subtitle
// file. ffmpeg without libass only fails at exec time, so the capability probe
// (Capabilities.HasSubtitlesFilter) lets option validation reject the request
// up front: handlers map this sentinel to HTTP 400 and the web player falls
// back to the sidecar <track> path. (Embedded-track burn-in uses the overlay
// filter and is unaffected.)
var ErrSubtitlesFilterUnavailable = errors.New("transcode: ffmpeg build has no libass 'subtitles' filter; external subtitle burn-in unavailable")

// Options describes how to transcode one stream segment.
// All fields are optional — empty means "keep original / passthrough".
type Options struct {
	AudioTrack   int    // absolute stream index for `-map 0:<n>` (-1 = first audio)
	SubBurnTrack int    // -1 = none; otherwise absolute stream index for hardsub burn-in
	SubBurnFile  string // absolute path to an external subtitle file (VTT/SRT) burned in via libass; "" = none. Must be produced by the handler's ValidBurnFilePath guard — never a client-supplied path.
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
	// Option gate BEFORE any args are built (and therefore before exec):
	// invalid burn-file options die here with a typed error that handlers
	// surface as HTTP 400, never as a mid-stream ffmpeg failure.
	if err := ValidateOptions(caps, opts); err != nil {
		return err
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

	// fixed/config binary; user values are operands of -i or integers; exec without shell
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
	// External-file burn-in wins over the embedded-track overlay when both are
	// set: the file is what the user picked last, and overlaying a different
	// track alongside it would render two subtitles (or the wrong one).
	burnFile := opts.SubBurnFile != ""
	burnEmbedded := opts.SubBurnTrack >= 0 && !burnFile
	// Video transcode (not subtitle burn, which uses CPU filter paths): decode
	// on the matching backend so frames feed the scale_* filter added below.
	// Burn paths must ALSO skip HW decode even when a VideoCodec is requested:
	// mixing HW frames with the subtitle filters hits the documented
	// "Error reinitializing filters" failure (comment above).
	transcodeVideo := opts.VideoCodec != "" && !burnEmbedded && !burnFile
	if transcodeVideo {
		args = append(args, hwDecodeArgsFor(encoder)...)
	}

	args = append(args, "-i", pipe0)

	if burnEmbedded {
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
		// No burn, or external-file burn: map the primary video. The external
		// subtitle is applied by the simple -vf subtitles=… filter below, which
		// (unlike the overlay filter_complex, which consumes a demuxed BITMAP
		// stream) renders a text VTT/SRT file onto the mapped video.
		args = append(args, "-map", "0:v:0")
		if burnFile && opts.VideoCodec == "" {
			opts.VideoCodec = "h264"
		}
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
		// (transcodeVideo is false on the burn paths, which scale via the overlay
		// / render text via the subtitles filter below.)
		args = append(args, "-vf", videoScaleFilter(encoder))
	} else if burnFile {
		// External subtitle file burn-in via libass. -sn/-dn above still drop
		// every demuxed subtitle stream — the rendered video IS the subtitle
		// output. Path escaping keeps the filtergraph specials (':' ',' ';'
		// '[' ']' '\'' '\') from breaking the option/graph parser.
		args = append(args, "-vf", subtitlesFilterArg(opts.SubBurnFile))
	}

	args = appendVideoCodecArgs(args, caps, preferred, opts.VideoCodec)
	args = appendAudioCodecArgs(args, opts.AudioCodec, container)
	args = appendContainerArgs(args, container)
	args = append(args, "-y", pipe1)
	return args
}

// ValidBurnFilePath reports whether candidate resolves strictly INSIDE root.
// It is the security barrier for external-subtitle burn-in: the HTTP client
// only ever sends an OpenSubtitles numeric file ID, the server joins it onto
// the subtitles cache root, and this function proves the result cannot escape
// that root (".." traversal, absolute escapes, empty values, candidate ==
// root). Symlink escape is out of scope here: cache contents are written by
// the server itself (see internal/subtitles cachePath). Registered as a CodeQL
// barrier (.github/codeql/extensions/barriers.yml); the unit tests in
// pipeline_burnfile_test.go pin these semantics — barrier without test = PR
// rejected (repo rule).
func ValidBurnFilePath(root, candidate string) bool {
	if root == "" || candidate == "" {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absCand, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absCand)
	if err != nil {
		return false
	}
	if rel == "." {
		return false // candidate IS the root itself, not a file under it
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ValidateOptions gates an option set before any ffmpeg args are built — the
// single choke point between HTTP input and exec. Run calls it as the last
// line of defense; handlers call it up front so invalid burnsub requests fail
// with a typed 400 instead of a mid-stream 500. Nil caps defers to Run's own
// "capabilities not probed" error (existing behavior for unprobed hosts).
func ValidateOptions(caps *Capabilities, opts Options) error {
	if opts.SubBurnFile == "" {
		return nil
	}
	if caps == nil {
		return nil
	}
	if !caps.HasSubtitlesFilter {
		return fmt.Errorf("burn file %s: %w", opts.SubBurnFile, ErrSubtitlesFilterUnavailable)
	}
	st, err := os.Stat(opts.SubBurnFile)
	if err != nil {
		return fmt.Errorf("burn file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("burn file %s: not a regular file", opts.SubBurnFile)
	}
	return nil
}

// subtitlesFilterArg builds the -vf value that renders an external text
// subtitle file (VTT/SRT) onto the mapped video with libass.
func subtitlesFilterArg(path string) string {
	return "subtitles=" + escapeSubtitlesFilterPath(path)
}

// escapeSubtitlesFilterPath makes a filesystem path safe inside an ffmpeg
// filtergraph option value: '\\' must be escaped first (single-pass replacer
// keeps later rules from double-escaping it), ':' separates filter options,
// a quote is escaped as \', ',' ';' split filters/chains and '[' ']' delimit
// link labels, so they get the same treatment.
// Escaping (not whole-value '...' quoting) is the chosen mechanism: inside
// quotes the backslash and the quote itself still need escapes, so one
// uniform escape pass covers every filtergraph special.
func escapeSubtitlesFilterPath(path string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`:`, `\:`,
		`'`, `\'`,
		`,`, `\,`,
		`;`, `\;`,
		`[`, `\[`,
		`]`, `\]`,
	).Replace(path)
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
// the encoder needs (the legacy single-variant default). See videoScaleFilterH.
func videoScaleFilter(encoder string) string { return videoScaleFilterH(encoder, 1080) }

// videoScaleFilterH caps height at maxH AND converts to the 8-bit pixel format
// the encoder needs. The 8-bit conversion is REQUIRED for 10-bit HDR sources
// (p010 — 4K HEVC Dolby Vision): the HW h264 encoders only take 8-bit NV12, and
// feeding p010 crashes ffmpeg with "Error reinitializing filters". The downscale
// also keeps realtime transcodes light. min(maxH,ih) never upscales smaller
// sources. maxH parameterises the ABR ladder (Phase 2): 1080/720/480 rungs each
// pass their own height; maxH ≤ 0 falls back to 1080 (the default cap).
func videoScaleFilterH(encoder string, maxH int) string {
	if maxH <= 0 {
		maxH = 1080
	}
	switch {
	case strings.HasSuffix(encoder, "_vaapi"):
		// Frames are on the GPU (vaapi surfaces) → scale + convert on the GPU.
		return fmt.Sprintf(`scale_vaapi=w=-2:h=min(%d\,ih):format=nv12`, maxH)
	case strings.HasSuffix(encoder, "_qsv"):
		return fmt.Sprintf(`scale_qsv=w=-2:h=min(%d\,ih):format=nv12`, maxH)
	default:
		// NVENC (frames downloaded to sysmem), libx264/libx265, videotoolbox:
		// software scale + 8-bit yuv420p. h264_nvenc uploads sysmem frames itself,
		// so this avoids scale_cuda's `format=` option (missing on ffmpeg 4.4.2).
		return fmt.Sprintf(`scale=-2:'min(%d,ih)',format=yuv420p`, maxH)
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
