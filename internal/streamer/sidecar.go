package streamer

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// SidecarSubtitle describes a standalone subtitle file inside the torrent
// (not embedded in the container, just next to the video).
type SidecarSubtitle struct {
	Index    int    `json:"index"`    // file index in torrent (use for download)
	Path     string `json:"path"`     // full path within torrent
	Size     int64  `json:"size"`
	Language string `json:"language"` // ISO-639 best-effort from filename
	Format   string `json:"format"`   // "srt" | "vtt" | "ass" | "ssa" | "sub"
}

// subtitleExtensions lists the file extensions we recognize as sidecar subs.
var subtitleExtensions = map[string]string{
	".srt": "srt",
	".vtt": "vtt",
	".ass": "ass",
	".ssa": "ssa",
	".sub": "sub",
}

// languageHints maps regex matches in filenames/paths → ISO 639 code.
// Order matters — more specific patterns (with region) tried first.
var languagePatterns = []struct {
	re   *regexp.Regexp
	code string
}{
	{regexp.MustCompile(`(?i)\bpt[-_.]?br\b|portuguese[-_ ]?\(?(brazil|br)\)?`), "pt-BR"},
	{regexp.MustCompile(`(?i)\bpt[-_.]?pt\b|portuguese[-_ ]?\(?(portugal|pt)\)?`), "pt-PT"},
	{regexp.MustCompile(`(?i)\bport(uguese)?\b|\bpor\b|\bpt\b`), "pt"},
	{regexp.MustCompile(`(?i)\beng(lish)?\b|\ben[-_]?us\b`), "en"},
	{regexp.MustCompile(`(?i)\bspa(nish)?\b|\bes\b|\besp\b`), "es"},
	{regexp.MustCompile(`(?i)\bfre(nch)?\b|\bfra\b|\bfr\b`), "fr"},
	{regexp.MustCompile(`(?i)\bita(lian)?\b|\bit\b`), "it"},
	{regexp.MustCompile(`(?i)\bger(man)?\b|\bdeu\b|\bde\b`), "de"},
	{regexp.MustCompile(`(?i)\bjap(anese)?\b|\bjpn\b|\bja\b`), "ja"},
	{regexp.MustCompile(`(?i)\brus(sian)?\b|\bru\b`), "ru"},
	{regexp.MustCompile(`(?i)\bchi(nese)?\b|\bzh\b|\bchs\b|\bcht\b`), "zh"},
	{regexp.MustCompile(`(?i)\bara(bic)?\b|\bar\b`), "ar"},
}

// detectLanguage tries to guess an ISO 639 code from the filename or path.
func detectLanguage(path string) string {
	base := strings.ToLower(filepath.Base(path))
	dir := strings.ToLower(filepath.Dir(path))
	for _, p := range languagePatterns {
		if p.re.MatchString(base) || p.re.MatchString(dir) {
			return p.code
		}
	}
	return ""
}

// Sidecars returns subtitle files inside the torrent.
// If videoFileIdx >= 0, results are ranked by path proximity to that video file
// (same directory first, then sibling 'subs' dirs, then anywhere).
func (s *Streamer) Sidecars(hash metainfo.Hash, videoFileIdx int) ([]SidecarSubtitle, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return nil, errors.New("torrent não está ativo")
	}

	files := e.t.Files()
	videoDir := ""
	if videoFileIdx >= 0 && videoFileIdx < len(files) {
		videoDir = filepath.Dir(files[videoFileIdx].Path())
	}

	out := []SidecarSubtitle{}
	for i, f := range files {
		ext := strings.ToLower(filepath.Ext(f.Path()))
		format, isSub := subtitleExtensions[ext]
		if !isSub {
			continue
		}
		out = append(out, SidecarSubtitle{
			Index:    i,
			Path:     f.Path(),
			Size:     f.Length(),
			Language: detectLanguage(f.Path()),
			Format:   format,
		})
	}

	// Sort: same-dir as video first, then everything else, preserving original order otherwise
	if videoDir != "" {
		// Stable partition
		var sameDir, other []SidecarSubtitle
		for _, sub := range out {
			if filepath.Dir(sub.Path) == videoDir {
				sameDir = append(sameDir, sub)
			} else {
				other = append(other, sub)
			}
		}
		out = append(sameDir, other...)
	}
	return out, nil
}

// ReadSidecar fetches the full subtitle file by index.
// Returns the raw bytes — caller is responsible for format conversion (e.g., SRT → VTT).
func (s *Streamer) ReadSidecar(ctx context.Context, hash metainfo.Hash, fileIdx int) ([]byte, string, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return nil, "", errors.New("torrent não está ativo")
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return nil, "", errors.New("file index out of range")
	}
	f := files[fileIdx]
	ext := strings.ToLower(filepath.Ext(f.Path()))
	format := subtitleExtensions[ext]
	if format == "" {
		return nil, "", errors.New("file is not a recognized subtitle format")
	}

	r := f.NewReader()
	r.SetReadahead(f.Length())
	r.SetResponsive()
	defer r.Close()

	// Bound the read to file size — subtitles are tiny (<1MB typically)
	data, err := io.ReadAll(io.LimitReader(r, 5*1024*1024))
	if err != nil {
		return nil, format, err
	}
	return data, format, nil
}
