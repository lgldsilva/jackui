// Package lyrics proxies the public LrcLib API (https://lrclib.net) from the
// backend, mirroring how tmdb/subtitles/imagesearch call external services
// server-side. Doing it here (not from the browser) lets us:
//   - send a proper identifying User-Agent (LrcLib etiquette) WITHOUT tripping a
//     CORS preflight the browser can't satisfy;
//   - cache results so repeated views don't re-hit the public API;
//   - shield each client's IP from LrcLib's per-IP rate limiting.
package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	userAgent = "JackUI (https://github.com/lgldsilva/jackui)"
	cacheTTL  = 24 * time.Hour
)

// baseURL is a var (not const) so tests can point it at an httptest server —
// the only reason it isn't const. Production never reassigns it.
var baseURL = "https://lrclib.net"

// Lyrics is what the handler returns to the player. Synced is LRC with
// [mm:ss.xx] timestamps (preferred); Plain is the fallback unsynced text.
type Lyrics struct {
	Synced string `json:"synced"`
	Plain  string `json:"plain"`
	Source string `json:"source"` // "lrclib" | "" (none)
}

type lrclibResp struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics  string `json:"plainLyrics"`
	Instrumental bool   `json:"instrumental"`
}

type cacheEntry struct {
	lyr Lyrics
	at  time.Time
}

// Client is a cached LrcLib proxy. The zero value is not usable — call New.
type Client struct {
	http  *http.Client
	mu    sync.Mutex
	cache map[string]cacheEntry
}

func New() *Client {
	return &Client{
		http:  &http.Client{Timeout: 8 * time.Second},
		cache: map[string]cacheEntry{},
	}
}

func cacheKey(artist, title, album string) string {
	return strings.ToLower(strings.TrimSpace(artist) + "|" + strings.TrimSpace(title) + "|" + strings.TrimSpace(album))
}

// Get resolves lyrics for a track. It tries the exact /api/get first, then
// falls back to /api/search. Results (including "not found") are cached for
// cacheTTL. A nil error with an empty Source means "no lyrics found".
func (c *Client) Get(ctx context.Context, artist, title, album string, durationSec int) (Lyrics, error) {
	if strings.TrimSpace(title) == "" {
		return Lyrics{}, fmt.Errorf("title is required")
	}
	key := cacheKey(artist, title, album)
	if v, ok := c.cached(key); ok {
		return v, nil
	}
	lyr := c.fetch(ctx, artist, title, album, durationSec)
	c.store(key, lyr)
	return lyr, nil
}

func (c *Client) cached(key string) (Lyrics, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Since(e.at) > cacheTTL {
		return Lyrics{}, false
	}
	return e.lyr, true
}

func (c *Client) store(key string, lyr Lyrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = cacheEntry{lyr: lyr, at: time.Now()}
}

// fetch tries the exact-match endpoint then search. Always returns a Lyrics
// (empty Source on miss/error) — lyrics are best-effort, never fatal.
func (c *Client) fetch(ctx context.Context, artist, title, album string, durationSec int) Lyrics {
	if r, ok := c.getExact(ctx, artist, title, album, durationSec); ok {
		return r
	}
	if r, ok := c.search(ctx, artist, title); ok {
		return r
	}
	return Lyrics{}
}

func (c *Client) getExact(ctx context.Context, artist, title, album string, durationSec int) (Lyrics, bool) {
	q := url.Values{}
	q.Set("track_name", title)
	q.Set("artist_name", artist)
	if album != "" {
		q.Set("album_name", album)
	}
	if durationSec > 0 {
		q.Set("duration", strconv.Itoa(durationSec))
	}
	var r lrclibResp
	if !c.getJSON(ctx, "/api/get?"+q.Encode(), &r) {
		return Lyrics{}, false
	}
	return toLyrics(r)
}

func (c *Client) search(ctx context.Context, artist, title string) (Lyrics, bool) {
	q := url.Values{}
	q.Set("track_name", title)
	if artist != "" {
		q.Set("artist_name", artist)
	}
	var results []lrclibResp
	if !c.getJSON(ctx, "/api/search?"+q.Encode(), &results) {
		return Lyrics{}, false
	}
	for _, r := range results {
		if lyr, ok := toLyrics(r); ok {
			return lyr, true
		}
	}
	return Lyrics{}, false
}

func toLyrics(r lrclibResp) (Lyrics, bool) {
	if r.Instrumental || (r.SyncedLyrics == "" && r.PlainLyrics == "") {
		return Lyrics{}, false
	}
	return Lyrics{Synced: r.SyncedLyrics, Plain: r.PlainLyrics, Source: "lrclib"}, true
}

// getJSON GETs path under baseURL with the identifying UA and decodes JSON into
// out. Returns false on any non-200 / transport / decode error (caller treats
// as miss).
func (c *Client) getJSON(ctx context.Context, path string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false
	}
	return json.Unmarshal(body, out) == nil
}
