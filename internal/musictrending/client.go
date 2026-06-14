// Package musictrending proxies Apple's keyless "Marketing Tools" RSS feed (top
// albums per country) from the backend, mirroring how tmdb/lyrics call external
// services server-side. The browser can't fetch it directly — Apple sends no
// Access-Control-Allow-Origin header — so a server-side proxy is required.
// Results are cached in memory (trending moves slowly; a re-fetch after a
// restart costs one request), keyed by country.
package musictrending

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	userAgent = "JackUI (https://github.com/lgldsilva/jackui)"
	cacheTTL  = 6 * time.Hour
	maxBody   = 1 << 20 // 1 MiB — the feed is small JSON
	feedSize  = 50      // how many we ask Apple for (then cache + slice per request)
	defLimit  = 30
	maxLimit  = 100
)

const defaultBaseURL = "https://rss.marketingtools.apple.com"

// Album is one trending album returned to the frontend. Artist + Name feed a
// clean search seed ("Artist Name"); Artwork is the cover (upscaled to 512px).
type Album struct {
	Artist      string `json:"artist"`
	Name        string `json:"name"`
	Artwork     string `json:"artwork"`
	AppleURL    string `json:"appleUrl"`
	ReleaseDate string `json:"releaseDate"`
}

// appleResult/appleResp model the parts of the Apple feed JSON we read.
type appleResult struct {
	ArtistName  string `json:"artistName"`
	Name        string `json:"name"`
	ArtworkURL  string `json:"artworkUrl100"`
	URL         string `json:"url"`
	ReleaseDate string `json:"releaseDate"`
}

type appleResp struct {
	Feed struct {
		Results []appleResult `json:"results"`
	} `json:"feed"`
}

type cacheEntry struct {
	albums []Album
	at     time.Time
}

// Client is a cached Apple-RSS trending proxy. The zero value is not usable —
// call New.
type Client struct {
	http    *http.Client
	baseURL string
	mu      sync.Mutex
	cache   map[string]cacheEntry
}

func New() *Client {
	return &Client{
		http:    &http.Client{Timeout: 8 * time.Second},
		baseURL: defaultBaseURL,
		cache:   map[string]cacheEntry{},
	}
}

// NewForTest builds a client pointed at a custom base URL (an httptest server).
// Test-only helper so handler tests in other packages don't hit the network.
func NewForTest(baseURL string) *Client {
	c := New()
	c.baseURL = baseURL
	return c
}

// normCountry sanitizes the country to a 2-letter lowercase ISO code, defaulting
// to "us" (Apple's feed path segment, e.g. us/br/gb).
func normCountry(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if len(c) != 2 {
		return "us"
	}
	for _, r := range c {
		if r < 'a' || r > 'z' {
			return "us"
		}
	}
	return c
}

func clampLimit(n int) int {
	if n <= 0 {
		return defLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// upscaleArt swaps Apple's 100x100 thumbnail for a 512px cover (the URL embeds
// the size). Returns the input unchanged if the marker isn't present.
func upscaleArt(u string) string {
	if u == "" {
		return ""
	}
	return strings.Replace(u, "100x100bb", "512x512bb", 1)
}

// Top returns the most-played albums for a country, cached for cacheTTL and
// sliced to `limit`.
func (c *Client) Top(ctx context.Context, country string, limit int) ([]Album, error) {
	country = normCountry(country)
	limit = clampLimit(limit)
	if v, ok := c.cached(country); ok {
		return capAlbums(v, limit), nil
	}
	albums, err := c.fetch(ctx, country)
	if err != nil {
		return nil, err
	}
	c.store(country, albums)
	return capAlbums(albums, limit), nil
}

func (c *Client) cached(country string) ([]Album, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[country]
	if !ok || time.Since(e.at) > cacheTTL {
		return nil, false
	}
	return e.albums, true
}

func (c *Client) store(country string, albums []Album) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[country] = cacheEntry{albums: albums, at: time.Now()}
}

func capAlbums(albums []Album, limit int) []Album {
	if limit < len(albums) {
		return albums[:limit]
	}
	return albums
}

func (c *Client) fetch(ctx context.Context, country string) ([]Album, error) {
	path := fmt.Sprintf("/api/v2/%s/music/most-played/%d/albums.json", country, feedSize)
	var r appleResp
	if err := c.getJSON(ctx, path, &r); err != nil {
		return nil, err
	}
	out := make([]Album, 0, len(r.Feed.Results))
	for _, a := range r.Feed.Results {
		if a.Name == "" {
			continue
		}
		out = append(out, Album{
			Artist:      a.ArtistName,
			Name:        a.Name,
			Artwork:     upscaleArt(a.ArtworkURL),
			AppleURL:    a.URL,
			ReleaseDate: a.ReleaseDate,
		})
	}
	return out, nil
}

// getJSON GETs path under baseURL with the identifying UA and decodes JSON into
// out. Returns an error on any non-200 / transport / decode failure.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("apple rss: status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
}
