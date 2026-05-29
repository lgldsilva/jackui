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
	"sync"

	"github.com/anacrolix/torrent/metainfo"
)

const (
	pipe0        = "pipe:0"
	pipe1        = "pipe:1"
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
	Image    bool   `json:"image,omitempty"`    // true if subtitle is image-based (PGS, DVD) — needs burn-in
}

// ProbeResult lists all switchable tracks in a torrent file.
type ProbeResult struct {
	Audio     []Track `json:"audio"`
	Subtitles []Track `json:"subtitles"`
	// DurationSec is the total media duration in seconds, 0 when ffprobe
	// couldn't determine it (e.g. MP4 with moov-at-end whose tail isn't
	// downloaded yet). Callers must treat 0 as "unknown" and fall back.
	DurationSec float64 `json:"durationSec"`
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
			return ProbeResult{}, errors.New(ErrTorrentNotActive)
		}
		files := e.t.Files()
		s.mu.Unlock()
		if fileIdx < 0 || fileIdx >= len(files) {
			return ProbeResult{}, errors.New(ErrFileIndexOutOfRange)
		}
		f := files[fileIdx]

		r := f.NewReader()
		r.SetReadahead(2 * 1024 * 1024)
		r.SetResponsive()
		closeFn = r.Close

		input = "pipe:"
		stdin = io.LimitReader(r, 16*1024*1024)
	}

	if closeFn != nil {
		defer closeFn()
	}

	cmd := exec.CommandContext(ctx, "ffprobe",
		ffHideBanner, ffLogLevel, "error",
		"-of", "json",
		"-show_streams",
		"-show_format",
		"-i", input,
	)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.Output()
	if err != nil {
		// ffprobe might return early-EOF errors but still emit valid JSON; try parsing anyway
		if len(out) == 0 {
			return ProbeResult{}, fmt.Errorf("ffprobe: %w", err)
		}
	}

	var parsed struct {
		Streams []struct {
			Index       int               `json:"index"`
			CodecType   string            `json:"codec_type"`
			CodecName   string            `json:"codec_name"`
			Channels    int               `json:"channels"`
			Tags        map[string]string `json:"tags"`
			Disposition struct {
				Default int `json:"default"`
				Forced  int `json:"forced"`
			} `json:"disposition"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return ProbeResult{}, fmt.Errorf("decode ffprobe: %w", err)
	}

	// Initialize as empty slices so JSON serializes them as [] (not null) — crucial for frontend safety
	result := ProbeResult{
		Audio:     []Track{},
		Subtitles: []Track{},
	}
	for _, st := range parsed.Streams {
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
		switch st.CodecType {
		case "audio":
			t.Type = "audio"
			result.Audio = append(result.Audio, t)
		case "subtitle":
			t.Type = "subtitle"
			switch st.CodecName {
			case "hdmv_pgs_subtitle", "dvd_subtitle", "dvdsub", "pgssub", "xsub", "bd_pcm_dvb_subtitle":
				t.Image = true
			}
			result.Subtitles = append(result.Subtitles, t)
		}
	}

	// Total duration (0 when ffprobe couldn't read it — e.g. moov-at-end MP4
	// whose tail isn't on disk). strconv handles the "N/A" / "" cases as 0.
	if parsed.Format.Duration != "" {
		if d, perr := strconv.ParseFloat(parsed.Format.Duration, 64); perr == nil {
			result.DurationSec = d
		}
	}

	probeCacheMu.Lock()
	probeCache[key] = result
	probeCacheMu.Unlock()
	return result, nil
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
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, true, nil
	}

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
			return nil, false, errors.New(ErrTorrentNotActive)
		}
		files := e.t.Files()
		s.mu.Unlock()
		if fileIdx < 0 || fileIdx >= len(files) {
			return nil, false, errors.New(ErrFileIndexOutOfRange)
		}
		f := files[fileIdx]
		r := f.NewReader()
		r.SetReadahead(8 * 1024 * 1024)
		r.SetResponsive()
		closeFn = r.Close

		input = pipe0
		stdin = r
	}

	if closeFn != nil {
		defer closeFn()
	}

	// -ss before -i is "fast seek" via container index; less accurate but much faster.
	// We're only producing a preview tooltip image — pixel-accuracy is overkill.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		ffHideBanner, ffLogLevel, "error",
		"-ss", fmt.Sprintf("%d", bucket*10),
		"-i", input,
		"-frames:v", "1",
		"-vf", "scale=240:-2", // 240 wide preserving aspect — height auto-computed
		"-q:v", "5",            // 1-31, lower=better; 5 is sweet spot for previews
		"-f", "mjpeg",
		"-y",
		pipe1,
	)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil, false, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
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
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, true, nil
	}

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
			return nil, false, errors.New(ErrTorrentNotActive)
		}
		files := e.t.Files()
		s.mu.Unlock()
		if fileIdx < 0 || fileIdx >= len(files) {
			return nil, false, errors.New(ErrFileIndexOutOfRange)
		}
		f := files[fileIdx]
		r := f.NewReader()
		// Cover art typically sits in the header for MP3/FLAC, so a smaller readahead
		// is enough — we don't need to wait for the whole audio file.
		r.SetReadahead(2 * 1024 * 1024)
		r.SetResponsive()
		closeFn = r.Close

		input = pipe0
		stdin = r
	}

	if closeFn != nil {
		defer closeFn()
	}

	// `-map 0:v -map -0:V` selects attached pictures only, excluding regular
	// video streams (e.g. a music-video stream baked into the same file).
	cmd := exec.CommandContext(ctx, "ffmpeg",
		ffHideBanner, ffLogLevel, "error",
		"-i", input,
		"-map", "0:v",
		"-map", "-0:V",
		"-c", "copy",
		"-f", "mjpeg",
		"-frames:v", "1",
		"-y",
		pipe1,
	)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		// Negative-cache so we don't burn ffmpeg again next time.
		_ = os.MkdirAll(cacheDir, 0o755)
		_ = os.WriteFile(emptyMarker, []byte{}, 0o644)
		return nil, false, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
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
			return nil, errors.New(ErrTorrentNotActive)
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

	cmd := exec.CommandContext(ctx, "ffmpeg",
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
