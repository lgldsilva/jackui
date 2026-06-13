// Package transcode probes the host environment for hardware-accelerated video
// encoding/decoding options and exposes a runtime capability matrix.
//
// Design goal: portability — swap GPUs, drop GPU entirely, install a different
// driver, and the probe re-evaluates. Never hard-code encoder names in handlers.
package transcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	backendNvidia      = "nvidia"
	backendAMDVAAPI    = "amd-vaapi"
	backendAMDAMF      = "amd-amf"
	backendIntelQSV    = "intel-qsv"
	backendAppleVT     = "apple-vt"
	backendCPU         = "cpu"
	hwDeviceVAAPI      = "vaapi=va:/dev/dri/renderD128"
	ffBinary           = "ffmpeg"
	ffHideBanner       = "-hide_banner"
	ffLogLevel         = "-loglevel"
	ffHWAccel          = "-hwaccel"
	ffHWAccelOutFormat = "-hwaccel_output_format"
	ffPreset           = "-preset"
	pipe0              = "pipe:0"
	pipe1              = "pipe:1"
	// ffmpeg flags/values shared by the HLS encode specs (video + audio-only) and
	// the duration probe — kept as constants so the variants don't duplicate the
	// literals (SonarQube go:S1192).
	ffSeekable        = "-seekable"
	ffMultipleReq     = "-multiple_requests"
	ffProbesize       = "-probesize"
	ffAnalyzeDuration = "-analyzeduration"
	ffAfAsetptsZero   = "asetpts=PTS-STARTPTS"
	hlsPlaylistFile   = "index.m3u8"
)

// Encoder identifies one transcoding backend.
type Encoder struct {
	ID          string  `json:"id"`                 // stable identifier (e.g. "h264_nvenc")
	Codec       string  `json:"codec"`              // "h264" | "hevc"
	Backend     string  `json:"backend"`            // "nvidia" | "amd-vaapi" | "intel-qsv" | "cpu"
	Available   bool    `json:"available"`          // listed by ffmpeg as compiled in
	Functional  bool    `json:"functional"`         // smoke-test encode succeeded
	BenchFPS    float64 `json:"benchFps,omitempty"` // frames/sec on 480p test clip
	Description string  `json:"description"`
	Error       string  `json:"error,omitempty"`
}

// Decoder is the same shape but for decoding.
type Decoder struct {
	ID         string `json:"id"`
	Codec      string `json:"codec"`
	Backend    string `json:"backend"`
	Available  bool   `json:"available"`
	Functional bool   `json:"functional"`
	Error      string `json:"error,omitempty"`
}

// Capabilities is the full probe result.
type Capabilities struct {
	ProbedAt    time.Time `json:"probedAt"`
	OS          string    `json:"os"`
	FFmpegPath  string    `json:"ffmpegPath"`
	FFmpegVer   string    `json:"ffmpegVersion"`
	HasNVIDIA   bool      `json:"hasNvidia"`
	HasVAAPI    bool      `json:"hasVaapi"`
	HasQSV      bool      `json:"hasQsv"`
	Encoders    []Encoder `json:"encoders"`
	Decoders    []Decoder `json:"decoders"`
	Preferred   string    `json:"preferred"`     // chosen encoder ID for H.264 transcoding
	PreferredHE string    `json:"preferredHevc"` // chosen for HEVC transcoding (if any)
}

// candidates we try, in priority order. First functional one wins as Preferred.
var encoderCandidates = []struct {
	id          string
	codec       string
	backend     string
	description string
	hwflag      string // -init_hw_device flag if needed
}{
	// NVIDIA
	{"h264_nvenc", "h264", backendNvidia, "NVIDIA NVENC H.264", ""},
	{"hevc_nvenc", "hevc", backendNvidia, "NVIDIA NVENC HEVC", ""},
	// AMD via VAAPI (Linux)
	{"h264_vaapi", "h264", backendAMDVAAPI, "AMD/Intel VAAPI H.264", hwDeviceVAAPI},
	{"hevc_vaapi", "hevc", backendAMDVAAPI, "AMD/Intel VAAPI HEVC", hwDeviceVAAPI},
	// AMD via AMF (Windows mostly; rarely on Linux)
	{"h264_amf", "h264", backendAMDAMF, "AMD AMF H.264", ""},
	{"hevc_amf", "hevc", backendAMDAMF, "AMD AMF HEVC", ""},
	// Intel QuickSync
	{"h264_qsv", "h264", backendIntelQSV, "Intel QuickSync H.264", ""},
	{"hevc_qsv", "hevc", backendIntelQSV, "Intel QuickSync HEVC", ""},
	// Apple VideoToolbox (macOS)
	{"h264_videotoolbox", "h264", backendAppleVT, "Apple VideoToolbox H.264", ""},
	{"hevc_videotoolbox", "hevc", backendAppleVT, "Apple VideoToolbox HEVC", ""},
	// CPU fallbacks (always functional if ffmpeg has them)
	{"libx264", "h264", backendCPU, "libx264 (CPU)", ""},
	{"libx265", "hevc", backendCPU, "libx265 (CPU)", ""},
}

var decoderCandidates = []struct {
	id      string
	codec   string
	backend string
}{
	{"h264_cuvid", "h264", backendNvidia},
	{"hevc_cuvid", "hevc", backendNvidia},
	{"h264_vaapi", "h264", backendAMDVAAPI},
	{"hevc_vaapi", "hevc", backendAMDVAAPI},
	{"h264_qsv", "h264", backendIntelQSV},
	{"hevc_qsv", "hevc", backendIntelQSV},
}

var (
	cacheMu sync.RWMutex
	cached  *Capabilities
)

// Probe runs the full detection + smoke-test sequence and caches the result.
// Pass force=true to re-probe (e.g. after a GPU upgrade).
func Probe(ctx context.Context, force bool) (*Capabilities, error) {
	cacheMu.RLock()
	if !force && cached != nil {
		c := *cached
		cacheMu.RUnlock()
		return &c, nil
	}
	cacheMu.RUnlock()

	cacheMu.Lock()
	defer cacheMu.Unlock()
	if !force && cached != nil {
		c := *cached
		return &c, nil
	}

	caps := &Capabilities{
		ProbedAt:   time.Now().UTC(),
		OS:         runtime.GOOS,
		FFmpegPath: findFFmpegPath(),
	}

	if caps.FFmpegPath == "" {
		return nil, fmt.Errorf("ffmpeg not found in PATH")
	}

	caps.FFmpegVer = readFFmpegVersion(ctx, caps.FFmpegPath)

	// Step 1 — discover compiled-in encoders/decoders
	compiledEncs := listEncoders(ctx, caps.FFmpegPath)
	compiledDecs := listDecoders(ctx, caps.FFmpegPath)

	// Step 2 — flag broad hardware signals
	caps.HasNVIDIA = compiledEncs["h264_nvenc"] || compiledDecs["h264_cuvid"]
	caps.HasVAAPI = compiledEncs["h264_vaapi"]
	caps.HasQSV = compiledEncs["h264_qsv"]

	// Step 3 — smoke-test each candidate encoder
	for _, c := range encoderCandidates {
		enc := Encoder{
			ID:          c.id,
			Codec:       c.codec,
			Backend:     c.backend,
			Description: c.description,
			Available:   compiledEncs[c.id],
		}
		if enc.Available {
			testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			ok, fps, err := smokeTestEncoder(testCtx, caps.FFmpegPath, c.id)
			cancel()
			enc.Functional = ok
			enc.BenchFPS = fps
			if err != nil {
				enc.Error = err.Error()
			}
		}
		caps.Encoders = append(caps.Encoders, enc)
	}

	// Decoders: just check listed; runtime probe per decoder needs real input
	for _, c := range decoderCandidates {
		caps.Decoders = append(caps.Decoders, Decoder{
			ID:         c.id,
			Codec:      c.codec,
			Backend:    c.backend,
			Available:  compiledDecs[c.id],
			Functional: compiledDecs[c.id], // optimistic — verify when used
		})
	}

	// Step 4 — pick preferred
	caps.Preferred = pickPreferred(caps.Encoders, "h264")
	caps.PreferredHE = pickPreferred(caps.Encoders, "hevc")

	cached = caps
	c := *caps
	return &c, nil
}

// ResetCachedForTesting zera o cache de capabilities. Só para testes que
// precisam exercitar o caminho "caps ainda não probadas".
func ResetCachedForTesting() {
	cacheMu.Lock()
	cached = nil
	cacheMu.Unlock()
}

// SetCachedForTesting injeta uma matriz de capabilities fake (ex.: um script
// stub no lugar do ffmpeg) para testes — inclusive de OUTROS pacotes — que
// precisam iniciar sessões HLS sem probar o host. Parear com
// ResetCachedForTesting no cleanup.
func SetCachedForTesting(c *Capabilities) {
	cacheMu.Lock()
	cached = c
	cacheMu.Unlock()
}

// Cached returns the last probe result without re-running it. nil if never probed.
func Cached() *Capabilities {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	if cached == nil {
		return nil
	}
	c := *cached
	return &c
}

// ─── implementation details ─────────────────────────────────────────────────

func findFFmpegPath() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

func readFFmpegVersion(ctx context.Context, ff string) string {
	out, _ := exec.CommandContext(ctx, ff, "-version").Output()
	line := strings.SplitN(string(out), "\n", 2)[0]
	return strings.TrimSpace(line)
}

func listEncoders(ctx context.Context, ff string) map[string]bool {
	out, _ := exec.CommandContext(ctx, ff, ffHideBanner, "-encoders").Output()
	return parseCodecList(out)
}

func listDecoders(ctx context.Context, ff string) map[string]bool {
	out, _ := exec.CommandContext(ctx, ff, ffHideBanner, "-decoders").Output()
	return parseCodecList(out)
}

// parseCodecList extracts identifiers from ffmpeg's "-encoders" / "-decoders" output.
// Lines look like: "  V....D h264_nvenc           NVIDIA NVENC H.264 encoder ..."
func parseCodecList(out []byte) map[string]bool {
	m := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// First non-flag token is the codec id
		flags := fields[0]
		if !strings.ContainsAny(flags, "VAS") {
			continue
		}
		m[fields[1]] = true
	}
	return m
}

// smokeTestEncoder pipes a synthetic 5-frame 320x240 test source through the encoder.
// Returns (ok, fps, err). Encoder works if exit=0 in <15s.
func smokeTestEncoder(ctx context.Context, ff, encoder string) (bool, float64, error) {
	args := []string{
		ffHideBanner, ffLogLevel, "error",
		"-f", "lavfi", "-i", "testsrc=duration=0.5:size=320x240:rate=10",
	}
	// Some encoders need format conversions / hw uploads
	if strings.HasSuffix(encoder, "_vaapi") {
		args = append([]string{
			ffHideBanner, ffLogLevel, "error",
			"-init_hw_device", hwDeviceVAAPI,
			"-filter_hw_device", "va",
			"-f", "lavfi", "-i", "testsrc=duration=0.5:size=320x240:rate=10",
			"-vf", "format=nv12,hwupload",
		}, []string{}...)
	}
	args = append(args, "-c:v", encoder, "-f", "null", "-")

	start := time.Now()
	cmd := exec.CommandContext(ctx, ff, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	elapsed := time.Since(start).Seconds()

	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		return false, 0, fmt.Errorf("%v: %s", err, msg)
	}
	fps := 0.0
	if elapsed > 0 {
		fps = 5.0 / elapsed // 5 frames in elapsed seconds
	}
	return true, fps, nil
}

// pickPreferred returns the ID of the best functional encoder for the codec,
// favoring hardware backends (nvenc > vaapi > qsv > amf > videotoolbox > cpu).
func pickPreferred(encs []Encoder, codec string) string {
	priority := map[string]int{
		backendNvidia: 1, backendIntelQSV: 2, backendAMDVAAPI: 3, backendAMDAMF: 4, backendAppleVT: 5, backendCPU: 99,
	}
	best := ""
	bestPri := 1000
	for _, e := range encs {
		if e.Codec != codec || !e.Functional {
			continue
		}
		p, ok := priority[e.Backend]
		if !ok {
			p = 50
		}
		if p < bestPri {
			bestPri = p
			best = e.ID
		}
	}
	return best
}

// String returns a one-line summary suitable for logs.
func (c *Capabilities) String() string {
	b, _ := json.Marshal(c)
	return string(b)
}
