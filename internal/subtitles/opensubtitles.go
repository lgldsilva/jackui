// Package subtitles searches and downloads subtitles via OpenSubtitles REST v1.
// Requires a free API key (https://www.opensubtitles.com/consumers).
package subtitles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiBase   = "https://api.opensubtitles.com/api/v1"
	userAgent = "JackUI v1.0"
)

// utf8BOM is the byte-order mark sometimes prepended to SRT files (U+FEFF).
var utf8BOM = "\xef\xbb\xbf"

type Client struct {
	apiKey   string
	username string
	password string
	cacheDir string // disk cache for downloaded VTTs; empty = disabled
	http     *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func New(apiKey, username, password, cacheDir string) *Client {
	if cacheDir != "" {
		_ = os.MkdirAll(cacheDir, 0o755)
	}
	return &Client{
		apiKey:   apiKey,
		username: username,
		password: password,
		cacheDir: cacheDir,
		http:     &http.Client{Timeout: 20 * time.Second},
	}
}

// CachePath returns the file path used to persist a downloaded subtitle.
// Empty string if caching is disabled.
func (c *Client) cachePath(fileID string) string {
	if c.cacheDir == "" || fileID == "" {
		return ""
	}
	// fileID is digits from OpenSubtitles, but sanitize defensively
	safe := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-' || r == '_' {
			return r
		}
		return -1
	}, fileID)
	if safe == "" {
		return ""
	}
	return filepath.Join(c.cacheDir, safe+".vtt")
}

func (c *Client) Enabled() bool {
	return c != nil && c.apiKey != ""
}

type Subtitle struct {
	ID           string `json:"id"`
	Language     string `json:"language"`
	Release      string `json:"release"`
	URL          string `json:"url"`
	UploaderName string `json:"uploaderName"`
	Downloads    int    `json:"downloads"`
	HearingImp   bool   `json:"hearingImpaired"`
	Trusted      bool   `json:"trusted"`
}

// SearchOpts is the union of all OpenSubtitles search parameters we support.
type SearchOpts struct {
	Query         string
	MovieHash     string // OS file hash; best signal for exact-match
	MovieBytesize int64  // required alongside MovieHash
	IMDB          string // e.g., "tt0468569" — strip "tt" prefix when sending
	Season        int
	Episode       int
	Languages     string // "pt-BR,pt"
}

// SearchAuto runs the most accurate query OpenSubtitles supports for the given metadata.
// When MovieHash is set, results are typically ranked first by hash-match (frame-exact).
func (c *Client) SearchAuto(opts SearchOpts) ([]Subtitle, error) {
	if !c.Enabled() {
		return nil, errors.New("OpenSubtitles desabilitado — configure a API key em Settings")
	}
	q := url.Values{}
	if opts.Query != "" {
		q.Set("query", opts.Query)
	}
	if opts.MovieHash != "" {
		q.Set("moviehash", opts.MovieHash)
		if opts.MovieBytesize > 0 {
			q.Set("moviebytesize", strconv.FormatInt(opts.MovieBytesize, 10))
		}
		// Documented OS flag to push hash-matches to top
		q.Set("moviehash_match", "include")
	}
	if opts.IMDB != "" {
		// API expects integer ID (no "tt" prefix)
		id := strings.TrimPrefix(opts.IMDB, "tt")
		q.Set("imdb_id", id)
	}
	if opts.Languages != "" {
		q.Set("languages", opts.Languages)
	}
	if opts.Season > 0 {
		q.Set("season_number", strconv.Itoa(opts.Season))
	}
	if opts.Episode > 0 {
		q.Set("episode_number", strconv.Itoa(opts.Episode))
	}
	q.Set("order_by", "download_count")

	return c.search(q)
}

// Search is the legacy shortcut for query-only searches.
func (c *Client) Search(query, langs string, season, episode int) ([]Subtitle, error) {
	return c.SearchAuto(SearchOpts{
		Query:     query,
		Languages: langs,
		Season:    season,
		Episode:   episode,
	})
}

func (c *Client) search(q url.Values) ([]Subtitle, error) {

	req, _ := http.NewRequest("GET", apiBase+"/subtitles?"+q.Encode(), nil)
	c.applyHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opensubtitles search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("opensubtitles search returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Data []struct {
			Attributes struct {
				Language        string `json:"language"`
				Release         string `json:"release"`
				URL             string `json:"url"`
				DownloadCount   int    `json:"download_count"`
				HearingImpaired bool   `json:"hearing_impaired"`
				FromTrusted     bool   `json:"from_trusted"`
				Uploader        struct {
					Name string `json:"name"`
				} `json:"uploader"`
				Files []struct {
					FileID int `json:"file_id"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode opensubtitles: %w", err)
	}

	out := make([]Subtitle, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		out = append(out, Subtitle{
			ID:           strconv.Itoa(d.Attributes.Files[0].FileID),
			Language:     d.Attributes.Language,
			Release:      d.Attributes.Release,
			URL:          d.Attributes.URL,
			UploaderName: d.Attributes.Uploader.Name,
			Downloads:    d.Attributes.DownloadCount,
			HearingImp:   d.Attributes.HearingImpaired,
			Trusted:      d.Attributes.FromTrusted,
		})
	}
	return out, nil
}

func (c *Client) Download(fileID string) ([]byte, error) {
	if !c.Enabled() {
		return nil, errors.New("OpenSubtitles desabilitado")
	}

	// Disk cache — subtitle content for a given file_id is immutable
	if path := c.cachePath(fileID); path != "" {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data, nil
		}
	}

	token, err := c.ensureToken()
	if err != nil {
		return nil, err
	}

	body, _ := json.Marshal(map[string]string{"file_id": fileID})
	req, _ := http.NewRequest("POST", apiBase+"/download", bytes.NewReader(body))
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opensubtitles download request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("opensubtitles download returned %d: %s", resp.StatusCode, string(b))
	}

	var meta struct {
		Link      string `json:"link"`
		Remaining int    `json:"remaining"`
		ResetTime string `json:"reset_time"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("decode download meta: %w", err)
	}
	if meta.Link == "" {
		return nil, errors.New("opensubtitles: empty download link")
	}

	dlResp, err := c.http.Get(meta.Link)
	if err != nil {
		return nil, fmt.Errorf("fetch subtitle file: %w", err)
	}
	defer func() { _ = dlResp.Body.Close() }()
	raw, err := io.ReadAll(dlResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read subtitle: %w", err)
	}
	vtt := srtToVTT(raw)

	// Persist to disk cache so we don't burn quota again
	if path := c.cachePath(fileID); path != "" {
		_ = os.WriteFile(path, vtt, 0o644)
	}
	return vtt, nil
}

func (c *Client) applyHeaders(req *http.Request) {
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
}

func (c *Client) ensureToken() (string, error) {
	if c.username == "" || c.password == "" {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}

	body, _ := json.Marshal(map[string]string{"username": c.username, "password": c.password})
	req, _ := http.NewRequest("POST", apiBase+"/login", bytes.NewReader(body))
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("opensubtitles login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("opensubtitles login returned %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	c.token = out.Token
	c.tokenExp = time.Now().Add(20 * time.Hour)
	return c.token, nil
}

// SRTToVTT (exported) converts SubRip to WebVTT — usable by anyone needing the conversion.
// Strips BOM, normalizes line endings, converts comma timestamps to dot.
func SRTToVTT(srt []byte) []byte { return srtToVTT(srt) }

func srtToVTT(srt []byte) []byte {
	s := strings.ReplaceAll(string(srt), "\r\n", "\n")
	s = strings.TrimPrefix(s, utf8BOM)
	s = arrowRe.ReplaceAllStringFunc(s, func(line string) string {
		return strings.ReplaceAll(line, ",", ".")
	})
	return []byte("WEBVTT\n\n" + s)
}
