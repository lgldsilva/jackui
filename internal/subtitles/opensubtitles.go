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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lgldsilva/jackui/internal/httpretry"
)

const (
	apiBase         = "https://api.opensubtitles.com/api/v1"
	userAgent       = "JackUI v1.0"
	contentTypeJSON = "application/json"

	// Response size caps — OpenSubtitles is an external service; a misbehaving
	// upstream must not make us buffer unbounded bodies in memory.
	maxJSONBytes         = 2 << 20 // JSON payloads (search results, download meta, login)
	maxSubtitleFileBytes = 8 << 20 // a downloaded subtitle file (SRTs are KB-scale)
	maxErrorBodyBytes    = 8 << 10 // error bodies echoed into error strings
)

// decodeCapped reads a JSON body with a hard cap and unmarshals it. A body over
// maxJSONBytes fails with an explicit error instead of surfacing as a confusing
// truncated-JSON decode error.
func decodeCapped(body io.Reader, v any) error {
	data, err := io.ReadAll(io.LimitReader(body, maxJSONBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxJSONBytes {
		return errors.New("opensubtitles: response exceeds size limit")
	}
	return json.Unmarshal(data, v)
}

// ErrQuota reports an OpenSubtitles quota / rate-limit failure (HTTP 429 for
// rate limiting, HTTP 406 once the daily download quota is exhausted). The
// original status and body text stay in the wrapped message; callers must
// classify with errors.Is, never by string matching.
var ErrQuota = errors.New("opensubtitles quota exceeded")

// ErrDisabled reports a client without a configured API key. Same contract as
// ErrQuota: match with errors.Is.
var ErrDisabled = errors.New("OpenSubtitles disabled — configure the API key in Settings")

// isQuotaStatus reports whether an OpenSubtitles HTTP status means the account
// quota or rate limit blocked the call: 429 is the documented rate limit, and
// the download endpoint answers 406 once the daily quota is exhausted.
func isQuotaStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusNotAcceptable
}

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
		// #nosec G301 -- media/cache dir; 0755 intentional so the media server can read
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

// fileIDPattern restricts cache keys to OpenSubtitles numeric file IDs.
// Defensive validation prevents user-provided values from reaching the path
// expression, satisfying path-traversal analyzers (CodeQL go/path-injection).
var fileIDPattern = regexp.MustCompile(`^[0-9]+$`)

// CachePath returns the file path used to persist a downloaded subtitle.
// Empty string if caching is disabled or the fileID is not a numeric ID.
func (c *Client) cachePath(fileID string) string {
	if c.cacheDir == "" || fileID == "" {
		return ""
	}
	if !fileIDPattern.MatchString(fileID) {
		return ""
	}
	return filepath.Join(c.cacheDir, fileID+".vtt")
}

// CachePath is the exported form of cachePath: the persisted VTT path for an
// OpenSubtitles file ID ("" when caching is disabled or the ID is not a
// numeric OS file ID). The AutoFetcher records it in subtitle_links.vtt_path.
func (c *Client) CachePath(fileID string) string {
	if c == nil {
		return ""
	}
	return c.cachePath(fileID)
}

// CacheDir returns the disk cache root where downloaded VTTs are persisted
// ("" = caching disabled or nil client). The transcode external-subtitle
// burn-in path uses it to resolve cached files by OpenSubtitles file ID.
func (c *Client) CacheDir() string {
	if c == nil {
		return ""
	}
	return c.cacheDir
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
	Forced       bool   `json:"forced,omitempty"` // OS foreign_parts_only ("forced") flag
	Trusted      bool   `json:"trusted"`
	// Formats lists the deduplicated, lowercased file extensions offered by
	// this result (e.g. [".srt",".vtt"]), taken from the files array's
	// file_name fields. Empty means the API did not carry extensions, i.e. the
	// format is UNKNOWN — consumers that must skip ASS/SSA (srtToVTT yields an
	// invalid track for them) treat unknown as safe, matching the manual flow.
	Formats []string `json:"formats,omitempty"`
	// PrimaryFormat is the lowercased extension of the result's PRIMARY file —
	// the one this Subtitle actually downloads, since ID is always
	// Files[0].FileID (e.g. ".ass"). Empty = unknown (the API carried no
	// extension for it). Unlike Formats — a union over ALL files — it tells
	// auto-apply gating what the download delivers: a files[0]=.ass plus
	// files[1]=.srt result must not be auto-applied even though .srt appears
	// in Formats. Additive; the manual flow ignores it.
	PrimaryFormat string `json:"primaryFormat,omitempty"`
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
		return nil, ErrDisabled
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

	resp, err := httpretry.Do(req.Context(), c.http, req, httpretry.Policy{})
	if err != nil {
		return nil, fmt.Errorf("opensubtitles search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		if isQuotaStatus(resp.StatusCode) {
			return nil, fmt.Errorf("%w: opensubtitles search returned %d: %s", ErrQuota, resp.StatusCode, string(body))
		}
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
				ForeignParts    bool   `json:"foreign_parts_only"` // forced subtitles
				FromTrusted     bool   `json:"from_trusted"`
				Uploader        struct {
					Name string `json:"name"`
				} `json:"uploader"`
				Files []struct {
					FileID   int    `json:"file_id"`
					FileName string `json:"file_name"` // carries the container extension when present
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := decodeCapped(resp.Body, &parsed); err != nil {
		return nil, fmt.Errorf("decode opensubtitles: %w", err)
	}

	out := make([]Subtitle, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		names := make([]string, 0, len(d.Attributes.Files))
		for _, f := range d.Attributes.Files {
			names = append(names, f.FileName)
		}
		out = append(out, Subtitle{
			ID:            strconv.Itoa(d.Attributes.Files[0].FileID),
			Language:      d.Attributes.Language,
			Release:       d.Attributes.Release,
			URL:           d.Attributes.URL,
			UploaderName:  d.Attributes.Uploader.Name,
			Downloads:     d.Attributes.DownloadCount,
			HearingImp:    d.Attributes.HearingImpaired,
			Forced:        d.Attributes.ForeignParts,
			Trusted:       d.Attributes.FromTrusted,
			Formats:       subtitleFormats(names),
			PrimaryFormat: primaryFileFormat(d.Attributes.Files[0].FileName),
		})
	}
	return out, nil
}

// subtitleFormats extracts the deduplicated, lowercased file extensions from
// OpenSubtitles file_name values ("Show.2024.1080p.eng.srt" → ".srt"). Blank
// names and extensionless entries are skipped; an empty result means the
// format is unknown, not unsafe.
func subtitleFormats(fileNames []string) []string {
	seen := make(map[string]bool, len(fileNames))
	out := make([]string, 0, len(fileNames))
	for _, name := range fileNames {
		ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
		if ext == "" || ext == "." || seen[ext] {
			continue
		}
		seen[ext] = true
		out = append(out, ext)
	}
	return out
}

// primaryFileFormat is the lowercased extension of ONE OpenSubtitles file_name
// ("" = blank/extensionless = unknown). Applied to Files[0] it describes the
// file the result's download actually delivers, unlike subtitleFormats which
// unions every file of the result.
func primaryFileFormat(fileName string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	if ext == "." {
		return ""
	}
	return ext
}

func (c *Client) Download(fileID string) ([]byte, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}

	// Disk cache — subtitle content for a given file_id is immutable
	if path := c.cachePath(fileID); path != "" {
		// #nosec G304 -- path validated by Browser.ResolvePath (guards traversal/symlink) or derived from internal hash/config
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data, nil
		}
	}

	token, err := c.ensureToken()
	if err != nil {
		return nil, err
	}

	link, err := c.downloadLink(fileID, token)
	if err != nil {
		return nil, err
	}
	raw, err := c.fetchSubtitle(link)
	if err != nil {
		return nil, err
	}
	vtt := srtToVTT(raw)

	// Persist to disk cache so we don't burn quota again
	if path := c.cachePath(fileID); path != "" {
		// media/cache file; 0644 intentional for reading
		_ = os.WriteFile(path, vtt, 0o644)
	}
	return vtt, nil
}

func (c *Client) downloadLink(fileID, token string) (string, error) {
	body, _ := json.Marshal(map[string]string{"file_id": fileID})
	req, _ := http.NewRequest("POST", apiBase+"/download", bytes.NewReader(body))
	c.applyHeaders(req)
	req.Header.Set("Content-Type", contentTypeJSON)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("opensubtitles download request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		if isQuotaStatus(resp.StatusCode) {
			return "", fmt.Errorf("%w: opensubtitles download returned %d: %s", ErrQuota, resp.StatusCode, string(b))
		}
		return "", fmt.Errorf("opensubtitles download returned %d: %s", resp.StatusCode, string(b))
	}

	var meta struct {
		Link      string `json:"link"`
		Remaining int    `json:"remaining"`
		ResetTime string `json:"reset_time"`
	}
	if err := decodeCapped(resp.Body, &meta); err != nil {
		return "", fmt.Errorf("decode download meta: %w", err)
	}
	if meta.Link == "" {
		return "", errors.New("opensubtitles: empty download link")
	}
	return meta.Link, nil
}

func (c *Client) fetchSubtitle(link string) ([]byte, error) {
	dlReq, _ := http.NewRequest("GET", link, nil)
	dlResp, err := httpretry.Do(dlReq.Context(), c.http, dlReq, httpretry.Policy{})
	if err != nil {
		return nil, fmt.Errorf("fetch subtitle file: %w", err)
	}
	defer func() { _ = dlResp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(dlResp.Body, maxSubtitleFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read subtitle: %w", err)
	}
	if int64(len(raw)) > maxSubtitleFileBytes {
		return nil, fmt.Errorf("opensubtitles: subtitle file exceeds %d bytes", maxSubtitleFileBytes)
	}
	return raw, nil
}

func (c *Client) applyHeaders(req *http.Request) {
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", contentTypeJSON)
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
	req.Header.Set("Content-Type", contentTypeJSON)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("opensubtitles login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return "", fmt.Errorf("opensubtitles login returned %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := decodeCapped(resp.Body, &out); err != nil {
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
