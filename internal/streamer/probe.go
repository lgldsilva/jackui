package streamer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
)

const (
	pipe0        = "pipe:0"
	pipe1        = "pipe:1"
	ffBinary     = "ffmpeg"
	ffHideBanner = "-hide_banner"
	ffLogLevel   = "-loglevel"
)

// Track describes one audio or subtitle stream inside a container.
type Track struct {
	Index    int    `json:"index"`              // absolute stream index in the container (use with `-map 0:N`)
	Type     string `json:"type"`               // "audio" | "subtitle"
	Codec    string `json:"codec"`              // e.g. "aac", "ac3", "subrip", "ass", "hdmv_pgs_subtitle"
	Language string `json:"language,omitempty"` // ISO 639-2 (e.g. "por", "eng") from container tags
	Title    string `json:"title,omitempty"`    // human-friendly title set by uploader, if any
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Image    bool   `json:"image,omitempty"` // true if subtitle is image-based (PGS, DVD) — needs burn-in
}

// Chapter is one chapter marker embedded in the media (MKV/MP4). Times are in
// seconds. The player navigates by setting video.currentTime to StartSec — this
// works for both direct-play and HLS (the transcode drops embedded chapters, so
// a <track kind="chapters"> would be empty; the probe list is the source).
type Chapter struct {
	Index    int     `json:"index"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec,omitempty"`
	Title    string  `json:"title,omitempty"`
}

// ProbeResult lists all switchable tracks in a torrent file.
type ProbeResult struct {
	Audio     []Track   `json:"audio"`
	Subtitles []Track   `json:"subtitles"`
	Chapters  []Chapter `json:"chapters"`
	// DurationSec is the total media duration in seconds, 0 when ffprobe
	// couldn't determine it (e.g. MP4 with moov-at-end whose tail isn't
	// downloaded yet). Callers must treat 0 as "unknown" and fall back.
	DurationSec float64 `json:"durationSec"`
	// VideoCodec / Container / AudioCodec são os fatos da fonte; NeedsTranscode é
	// a DECISÃO (navegador-agnóstica): MKV/HEVC/AV1/AC3/DTS não tocam direto em
	// browser nenhum → tem que transcodificar pra HLS. O front decide por isto
	// (não mais pelo NOME do arquivo, que errava e mandava incompatível pro
	// direct-play → errorCode 4 no Safari). Mesma lógica do classifyForBrowser
	// dos arquivos locais. Vazio até o ffprobe rodar.
	VideoCodec      string `json:"videoCodec"`
	Container       string `json:"container"`
	AudioCodec      string `json:"audioCodec"`
	NeedsTranscode  bool   `json:"needsTranscode"`
	TranscodeReason string `json:"transcodeReason,omitempty"`
}

// Conjuntos que o <video> dos browsers toca DIRETO (sem transcode). Fora deles →
// HLS. Espelha browserSafe* do internal/handlers/local_play.go.
var (
	browserSafeContainers  = map[string]bool{"mp4": true, "m4v": true, "mov": true, "webm": true, "isom": true, "mp42": true, "qt": true}
	browserSafeVideoCodecs = map[string]bool{"h264": true, "vp8": true, "vp9": true}
	browserSafeAudioCodecs = map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true}
)

// classifyTranscode decide se a fonte precisa de transcode→HLS (true) ou pode
// tocar direto, e o porquê. Navegador-agnóstico: os codecs/containers "unsafe"
// falham em todos os browsers.
func classifyTranscode(container, vcodec, acodec string) (bool, string) {
	if container != "" && !browserSafeContainers[container] {
		return true, "container=" + container
	}
	if vcodec != "" && !browserSafeVideoCodecs[vcodec] {
		return true, "vcodec=" + vcodec
	}
	if acodec != "" && !browserSafeAudioCodecs[acodec] {
		return true, "acodec=" + acodec
	}
	return false, ""
}

var (
	probeCacheMu sync.Mutex
	probeCache   = make(map[hashKey]ProbeResult)
)

// Probe runs ffprobe on the torrent file and lists audio + subtitle tracks.
// Reads at most 16 MB from the start of the file (enough for MKV/MP4 headers).
// Results are cached per (torrent, file).
func (s *Streamer) Probe(ctx context.Context, hash metainfo.Hash, fileIdx int) (ProbeResult, error) {
	key := hashKey{hash, fileIdx}
	probeCacheMu.Lock()
	if r, ok := probeCache[key]; ok {
		probeCacheMu.Unlock()
		return r, nil
	}
	probeCacheMu.Unlock()

	pi, ierr := s.resolveProbeInput(hash, fileIdx, 2*1024*1024)
	if ierr != nil {
		return ProbeResult{}, ierr
	}

	// Probe uses "pipe:" (not pipe0/pipe:0) and a limited reader
	input := pi.input
	stdin := pi.stdin
	if pi.input == pipe0 {
		input = "pipe:"
		stdin = io.LimitReader(pi.stdin, 16*1024*1024)
	}

	if pi.closeFn != nil {
		defer pi.closeFn()
	}

	out, err := runFFprobe(ctx, input, stdin)
	if err != nil {
		return ProbeResult{}, err
	}

	result, perr := parseProbeOutput(out)
	if perr != nil {
		return ProbeResult{}, fmt.Errorf("decode ffprobe: %w", perr)
	}

	probeCacheMu.Lock()
	if len(probeCache) >= 2000 {
		probeCache = make(map[hashKey]ProbeResult)
	}
	probeCache[key] = *result
	probeCacheMu.Unlock()
	return *result, nil
}

// ProbeLocal runs ffprobe on a local file path and returns the parsed tracks,
// chapters, duration and codec/container facts. It reuses parseProbeOutput — the
// SAME decoder the torrent path (Probe) uses — so /api/local/* handlers share
// ONE ffprobe invocation + parser with the torrent side instead of duplicating
// it. The caller owns the ctx/timeout.
func ProbeLocal(ctx context.Context, path string) (ProbeResult, error) {
	out, err := runFFprobe(ctx, path, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	result, perr := parseProbeOutput(out)
	if perr != nil {
		return ProbeResult{}, fmt.Errorf("decode ffprobe: %w", perr)
	}
	return *result, nil
}

func runFFprobe(ctx context.Context, input string, stdin io.Reader) ([]byte, error) {
	// #nosec G204 -- binario fixo/de config; valores de usuario sao operandos de -i ou inteiros; exec sem shell
	cmd := exec.CommandContext(ctx, "ffprobe",
		ffHideBanner, ffLogLevel, "error",
		"-of", "json",
		"-show_streams",
		"-show_format",
		"-show_chapters",
		"-i", input,
	)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, fmt.Errorf("ffprobe: %w", err)
		}
	}
	return out, nil
}

type ffprobeStream struct {
	Index       int               `json:"index"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	Channels    int               `json:"channels"`
	Tags        map[string]string `json:"tags"`
	Disposition struct {
		Default int `json:"default"`
		Forced  int `json:"forced"`
	} `json:"disposition"`
}

type ffprobeChapter struct {
	ID        int               `json:"id"`
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

type ffprobeOutput struct {
	Streams  []ffprobeStream  `json:"streams"`
	Chapters []ffprobeChapter `json:"chapters"`
	Format   struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// parseChapters maps ffprobe's chapter list to Chapters. start_time/end_time are
// seconds as strings (same shape as Format.Duration); a missing/garbage value
// just leaves the field at 0. Always returns a non-nil slice.
func parseChapters(chs []ffprobeChapter) []Chapter {
	out := []Chapter{}
	for _, ch := range chs {
		c := Chapter{Index: ch.ID}
		if s, err := strconv.ParseFloat(ch.StartTime, 64); err == nil {
			c.StartSec = s
		}
		if e, err := strconv.ParseFloat(ch.EndTime, 64); err == nil {
			c.EndSec = e
		}
		if ch.Tags != nil {
			c.Title = ch.Tags["title"]
		}
		out = append(out, c)
	}
	return out
}

func streamToTrack(st ffprobeStream) Track {
	t := Track{
		Index:    st.Index,
		Codec:    st.CodecName,
		Channels: st.Channels,
		Default:  st.Disposition.Default == 1,
		Forced:   st.Disposition.Forced == 1,
	}
	if st.Tags != nil {
		t.Language = st.Tags["language"]
		t.Title = st.Tags["title"]
	}
	return t
}

// classifyStreams separa as faixas por tipo e captura o codec do primeiro
// stream de vídeo (ignora capa/thumbnail anexada depois).
func classifyStreams(streams []ffprobeStream) (audio, subs []Track, videoCodec string) {
	audio, subs = []Track{}, []Track{}
	for _, st := range streams {
		t := streamToTrack(st)
		switch st.CodecType {
		case "audio":
			t.Type = "audio"
			audio = append(audio, t)
		case "subtitle":
			t.Type = "subtitle"
			t.Image = isImageSubtitle(st.CodecName)
			subs = append(subs, t)
		case "video":
			if videoCodec == "" {
				videoCodec = strings.ToLower(st.CodecName)
			}
		}
	}
	return audio, subs, videoCodec
}

// defaultAudioCodec devolve o codec da faixa de áudio default (ou a primeira).
func defaultAudioCodec(audio []Track) string {
	codec := ""
	for _, a := range audio {
		codec = strings.ToLower(a.Codec)
		if a.Default {
			break
		}
	}
	return codec
}

func parseProbeOutput(out []byte) (*ProbeResult, error) {
	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}

	audio, subs, videoCodec := classifyStreams(parsed.Streams)
	result := &ProbeResult{
		Audio:      audio,
		Subtitles:  subs,
		Chapters:   parseChapters(parsed.Chapters),
		VideoCodec: videoCodec,
	}
	if parsed.Format.Duration != "" {
		if d, perr := strconv.ParseFloat(parsed.Format.Duration, 64); perr == nil {
			result.DurationSec = d
		}
	}
	// Container = primeiro nome do format_name (ex: "matroska,webm" → "matroska").
	if fn := parsed.Format.FormatName; fn != "" {
		result.Container = strings.ToLower(strings.SplitN(fn, ",", 2)[0])
	}
	result.AudioCodec = defaultAudioCodec(result.Audio)
	result.NeedsTranscode, result.TranscodeReason = classifyTranscode(result.Container, result.VideoCodec, result.AudioCodec)
	return result, nil
}

func isImageSubtitle(codec string) bool {
	switch codec {
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvdsub", "pgssub", "xsub", "bd_pcm_dvb_subtitle":
		return true
	}
	return false
}

type probeInput struct {
	input   string
	stdin   io.Reader
	closeFn func() error
}

func (s *Streamer) resolveProbeInput(hash metainfo.Hash, fileIdx int, readahead int64) (probeInput, error) {
	if s.filePathResolver != nil {
		if path, ok := s.filePathResolver(hash, fileIdx); ok {
			return probeInput{input: path}, nil
		}
	}
	s.mu.Lock()
	e, ok := s.active[hash]
	if !ok {
		s.mu.Unlock()
		return probeInput{}, ErrTorrentNotActive
	}
	files := e.t.Files()
	s.mu.Unlock()
	if fileIdx < 0 || fileIdx >= len(files) {
		return probeInput{}, errors.New(ErrFileIndexOutOfRange)
	}
	f := files[fileIdx]
	r := f.NewReader()
	r.SetReadahead(readahead)
	r.SetResponsive()
	return probeInput{input: pipe0, stdin: r, closeFn: r.Close}, nil
}

// ExtractSubtitle pulls one embedded text-subtitle track out of the file as WebVTT.
// Image-based subs (PGS, DVD) are rejected — those need burn-in via transcoding.
// trackIdx must be the absolute stream index from Probe()'s Subtitles[i].Index.
// ExtractThumbnail seeks `atSeconds` into the given file and grabs a single
// frame as JPEG. Cached on disk under .thumbs/{hash}/{file}/{bucket}.jpg where
// bucket = round(seconds / 10), so consecutive hover positions reuse the same
// thumb and the disk doesn't explode. Resolution capped at 240 wide to keep
// payloads tiny (~20 KB each).
//
// Returns (jpeg, fromCache, error). Empty bytes + nil error means we couldn't
// decode at that timestamp (rare — likely seeking past the end). The handler
// translates that into HTTP 204.
func (s *Streamer) ExtractThumbnail(ctx context.Context, hash metainfo.Hash, fileIdx int, atSeconds int) ([]byte, bool, error) {
	if atSeconds < 0 {
		atSeconds = 0
	}
	bucket := atSeconds / 10 // quantize to 10s — keeps hover responsive without spamming ffmpeg
	cacheDir := filepath.Join(s.cfg.DataDir, ".thumbs", hash.HexString(), fmt.Sprintf("%d", fileIdx))
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("%d.jpg", bucket))
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, true, nil
	}

	pi, ierr := s.resolveProbeInput(hash, fileIdx, 8*1024*1024)
	if ierr != nil {
		return nil, false, ierr
	}
	if pi.closeFn != nil {
		defer pi.closeFn()
	}

	// -ss before -i is "fast seek" via container index; less accurate but much faster.
	// We're only producing a preview tooltip image — pixel-accuracy is overkill.
	// #nosec G204 -- binario fixo/de config; valores de usuario sao operandos de -i ou inteiros; exec sem shell
	cmd := exec.CommandContext(ctx, ffBinary,
		ffHideBanner, ffLogLevel, "error",
		"-ss", fmt.Sprintf("%d", bucket*10),
		"-i", pi.input,
		"-frames:v", "1",
		"-vf", "scale=240:-2", // 240 wide preserving aspect — height auto-computed
		"-q:v", "5", // 1-31, lower=better; 5 is sweet spot for previews
		"-f", "mjpeg",
		"-y",
		pipe1,
	)
	if pi.stdin != nil {
		cmd.Stdin = pi.stdin
	}
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil, false, nil
	}
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
		// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
		_ = os.WriteFile(cachePath, out, 0o644)
	}
	return out, false, nil
}

// ExtractArtwork pulls the embedded cover-art picture stream out of an audio
// file (MP3 APIC frame, FLAC PICTURE block, M4A covr atom, etc.) via ffmpeg and
// caches the JPEG on disk. Subsequent requests hit the disk cache for free.
//
// Returns (jpegBytes, fromCache, error). Empty bytes + nil error means the file
// has no embedded artwork — caller should serve a fallback placeholder.
func (s *Streamer) ExtractArtwork(ctx context.Context, hash metainfo.Hash, fileIdx int) ([]byte, bool, error) {
	cacheDir := filepath.Join(s.cfg.DataDir, ".artwork")
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("%s-%d.jpg", hash.HexString(), fileIdx))
	// Empty marker: same path + ".empty" suffix to negative-cache "no artwork" without re-running ffmpeg.
	emptyMarker := cachePath + ".empty"
	if _, err := os.Stat(emptyMarker); err == nil {
		return nil, true, nil
	}
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, true, nil
	}

	// Cover art typically sits in the header for MP3/FLAC, so a smaller readahead
	// is enough — we don't need to wait for the whole audio file.
	pi, ierr := s.resolveProbeInput(hash, fileIdx, 2*1024*1024)
	if ierr != nil {
		return nil, false, ierr
	}
	if pi.closeFn != nil {
		defer pi.closeFn()
	}

	// `-map 0:v -map -0:V` selects attached pictures only, excluding regular
	// video streams (e.g. a music-video stream baked into the same file).
	// #nosec G204 -- binario fixo/de config; valores de usuario sao operandos de -i ou inteiros; exec sem shell
	cmd := exec.CommandContext(ctx, ffBinary,
		ffHideBanner, ffLogLevel, "error",
		"-i", pi.input,
		"-map", "0:v",
		"-map", "-0:V",
		"-c", "copy",
		"-f", "mjpeg",
		"-frames:v", "1",
		"-y",
		pipe1,
	)
	if pi.stdin != nil {
		cmd.Stdin = pi.stdin
	}
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		// Negative-cache so we don't burn ffmpeg again next time.
		// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
		_ = os.MkdirAll(cacheDir, 0o755)
		// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
		_ = os.WriteFile(emptyMarker, []byte{}, 0o644)
		return nil, false, nil
	}
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
		// #nosec G306 -- arquivo de midia/cache; 0644 intencional p/ leitura
		_ = os.WriteFile(cachePath, out, 0o644)
	}
	return out, false, nil
}

func (s *Streamer) ExtractSubtitle(ctx context.Context, hash metainfo.Hash, fileIdx, trackIdx int) ([]byte, error) {
	var input string
	var stdin io.Reader
	var closeFn func() error

	if s.filePathResolver != nil {
		if path, ok := s.filePathResolver(hash, fileIdx); ok {
			input = path
		}
	}

	if input == "" {
		s.mu.Lock()
		e, ok := s.active[hash]
		if !ok {
			s.mu.Unlock()
			return nil, ErrTorrentNotActive
		}
		files := e.t.Files()
		s.mu.Unlock()
		if fileIdx < 0 || fileIdx >= len(files) {
			return nil, errors.New(ErrFileIndexOutOfRange)
		}
		f := files[fileIdx]
		r := f.NewReader()
		r.SetReadahead(4 * 1024 * 1024)
		r.SetResponsive()
		closeFn = r.Close

		input = pipe0
		stdin = r
	}

	if closeFn != nil {
		defer closeFn()
	}

	// #nosec G204 -- binario fixo/de config; valores de usuario sao operandos de -i ou inteiros; exec sem shell
	cmd := exec.CommandContext(ctx, ffBinary,
		ffHideBanner, ffLogLevel, "error",
		"-i", input,
		"-map", fmt.Sprintf("0:%d", trackIdx),
		"-c:s", "webvtt",
		"-f", "webvtt",
		"-y",
		pipe1,
	)
	if stdin != nil {
		cmd.Stdin = stdin
	}

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg extract: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("ffmpeg returned empty subtitle (track may be image-based or unsupported codec)")
	}
	return out, nil
}
