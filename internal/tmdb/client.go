// Package tmdb is a minimal The Movie Database client + on-disk cache.
//
// We do NOT pre-enrich search results (that would add 50-200ms to every search
// and is wasteful for queries the user won't click). Instead the frontend
// asks for a match lazily per visible card. The cache makes repeated lookups
// for the same title near-free.
package tmdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luizg/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

const (
	apiBase   = "https://api.themoviedb.org/3"
	imageBase = "https://image.tmdb.org/t/p/w300" // thumbnail size — enough for card UI
	cacheTTL  = 30 * 24 * time.Hour
)

// Match is the simplified view we expose to the frontend.
type Match struct {
	TmdbID      int     `json:"tmdbId"`
	ImdbID      string  `json:"imdbId,omitempty"` // resolved via external_ids; persisted so we never reprocess
	Title       string  `json:"title"`
	Year        int     `json:"year"`
	PosterURL   string  `json:"posterUrl"`
	Overview    string  `json:"overview"`
	VoteAverage float64 `json:"voteAverage"`          // TMDB community score (0-10)
	ImdbRating  float64 `json:"imdbRating,omitempty"` // real IMDb rating via OMDb (0-10), when available
	Kind        string  `json:"kind"`                 // "movie" | "tv"
}

type Client struct {
	apiKey  string
	omdbKey string
	http    *http.Client
	cache   *sql.DB

	// Trending is refreshed at most every trendingTTL (it changes slowly and is
	// shared by all users), cached in memory rather than the on-disk match cache.
	trendingMu    sync.Mutex
	trendingCache []Match
	trendingAt    time.Time

	// ratingTried dedupes OMDb backfill attempts (by IMDb id) within a process so
	// a title with no IMDb rating isn't re-queried on every card render.
	ratingTried sync.Map
}

const trendingTTL = 6 * time.Hour

// New returns a TMDB client. If apiKey is empty the client returns ErrDisabled
// from every call — handlers should surface that as "no enrichment available"
// (404) without exploding. omdbKey is optional: when set, matches are enriched
// with the real IMDb rating (via OMDb) on top of TMDB's own vote average.
func New(apiKey, omdbKey, cachePath string) (*Client, error) {
	db, err := sql.Open(dbutil.DriverName, cachePath+dbutil.PragmaWAL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tmdb_match (
			cache_key  TEXT PRIMARY KEY,
			payload    TEXT NOT NULL,
			cached_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Client{
		apiKey:  apiKey,
		omdbKey: omdbKey,
		http:    &http.Client{Timeout: 8 * time.Second},
		cache:   db,
	}, nil
}

func (c *Client) Close() error { return c.cache.Close() }

// ErrDisabled means no API key is configured — handlers should fall through
// gracefully instead of returning a 500.
var ErrDisabled = errors.New("tmdb: api key not configured")

// Release-name parsing for the TMDB search. Strategy: TRUNCATE at the first
// release marker (year / resolution / source / codec / SxxExx / bracket) rather
// than removing markers in place. Removing-in-place left the trailing scene
// group leaking into the query ("Inception.2010...x264-SPARKS" → "Inception
// SPARKS"), which hurt matches. Everything meaningful sits before the first
// marker, so cutting there yields a clean title.
var (
	yearRe = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	// markerRe finds the first release token preceded by a separator/bracket (or
	// the start) — the cut point. Order tokens longest-first inside alternation.
	markerRe    = regexp.MustCompile(`(?i)([._\s\-([]|^)((19|20)\d{2}|2160p|1080p|720p|480p|bluray|brrip|bdrip|webrip|web-dl|web|hdtv|hdrip|dvdrip|x264|x265|h\.?264|h\.?265|hevc|av1|aac|ac3|dts|atmos|truehd|ddp?5\.1|amzn|nf|hmax|repack|proper|extended|imax|hdr|sdr|10bit|remux|multi|dual|dublado|legendado|nacional|complete|season|s\d{1,2}e\d{1,3}|s\d{1,2}|e\d{1,3})([._\s\-)\]]|$)`)
	separatorRe = regexp.MustCompile(`[\._\-]+`)
	spacesRe    = regexp.MustCompile(`\s+`)
)

func cleanQuery(raw string) (title string, year int) {
	if m := yearRe.FindString(raw); m != "" {
		year, _ = strconv.Atoi(m)
	}
	t := raw
	if loc := markerRe.FindStringIndex(raw); loc != nil {
		t = raw[:loc[0]] // keep only the title before the first marker
	}
	t = separatorRe.ReplaceAllString(t, " ")
	t = spacesRe.ReplaceAllString(t, " ")
	return strings.TrimSpace(t), year
}

// Match looks up the best TMDB result for a raw torrent title, with caching.
// Returns (nil, nil) if no match was found — that's a normal "this isn't a
// known movie/show" case, not an error.
func (c *Client) Match(ctx context.Context, rawTitle string) (*Match, error) {
	if c.apiKey == "" {
		return nil, ErrDisabled
	}
	title, year := cleanQuery(rawTitle)
	if title == "" {
		return nil, nil
	}
	key := strings.ToLower(title)
	if year > 0 {
		key += fmt.Sprintf("|%d", year)
	}

	// Cache lookup
	if m, ok := c.getCached(key); ok {
		return m, nil
	}

	// Try multi-search (covers both movie and TV); fall back to movie-only.
	m, err := c.searchMulti(ctx, title, year)
	if err != nil {
		return nil, err
	}
	c.setCached(key, m)
	return m, nil
}

func (c *Client) getCached(key string) (*Match, bool) {
	var payload, cachedAt string
	err := c.cache.QueryRow(`SELECT payload, cached_at FROM tmdb_match WHERE cache_key=?`, key).Scan(&payload, &cachedAt)
	if err != nil {
		return nil, false
	}
	// Use the shared parser: modernc.org/sqlite sometimes returns DATETIME as
	// RFC3339, which the bare "2006-01-02 15:04:05" layout dropped to zero time
	// → every entry looked expired → every lookup hit the TMDB API, defeating
	// the 30-day cache. (Same class of bug already fixed in the other stores.)
	ts := dbutil.ParseTime(cachedAt)
	if time.Since(ts) > cacheTTL {
		return nil, false
	}
	if payload == "" || payload == "null" {
		// Negative cache — still valid to skip the API call for TTL.
		return nil, true
	}
	var m Match
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return nil, false
	}
	// Lazy IMDb-rating backfill: entries cached before OMDb was configured (or
	// before this feature) have ImdbID but no rating. Fetch it in the background
	// and re-cache, so the rating appears on the next view without waiting for the
	// 30-day TTL to expire. Deduped per IMDb id to avoid re-querying rating-less
	// titles on every render.
	if c.omdbKey != "" && m.ImdbID != "" && m.ImdbRating == 0 {
		if _, tried := c.ratingTried.LoadOrStore(m.ImdbID, true); !tried {
			go c.backfillRating(key, m)
		}
	}
	return &m, true
}

// backfillRating fetches the IMDb rating for an already-cached match and, if
// found, rewrites the cache entry in place (preserving everything else).
func (c *Client) backfillRating(key string, m Match) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rating := c.fetchImdbRating(ctx, m.ImdbID)
	if rating == 0 {
		return // nothing to persist; the dedupe guard stops further attempts this run
	}
	m.ImdbRating = rating
	b, _ := json.Marshal(&m)
	// Preserve cached_at so the backfill doesn't extend the TTL of a stale match.
	_, _ = c.cache.Exec(`UPDATE tmdb_match SET payload=? WHERE cache_key=?`, string(b), key)
}

func (c *Client) setCached(key string, m *Match) {
	payload := "null"
	if m != nil {
		b, _ := json.Marshal(m)
		payload = string(b)
	}
	_, _ = c.cache.Exec(`
		INSERT INTO tmdb_match(cache_key, payload, cached_at)
		VALUES(?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(cache_key) DO UPDATE SET payload=excluded.payload, cached_at=CURRENT_TIMESTAMP
	`, key, payload)
}

type multiSearchResp struct {
	Results []struct {
		ID           int     `json:"id"`
		MediaType    string  `json:"media_type"`
		Title        string  `json:"title"`
		Name         string  `json:"name"`
		Overview     string  `json:"overview"`
		PosterPath   string  `json:"poster_path"`
		ReleaseDate  string  `json:"release_date"`
		FirstAirDate string  `json:"first_air_date"`
		VoteAverage  float64 `json:"vote_average"`
		Popularity   float64 `json:"popularity"`
	} `json:"results"`
}

// fetchImdbID resolves the IMDb id for a TMDB movie/tv via the external_ids
// endpoint. Returns "" on any error — the caller treats it as "not available".
func (c *Client) fetchImdbID(ctx context.Context, kind string, tmdbID int) string {
	if kind != "movie" && kind != "tv" {
		return ""
	}
	u := fmt.Sprintf("%s/%s/%d/external_ids?api_key=%s", apiBase, kind, tmdbID, url.QueryEscape(c.apiKey))
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		ImdbID string `json:"imdb_id"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	return out.ImdbID
}

// fetchImdbRating resolves the real IMDb rating (0-10) for an IMDb id via the
// OMDb API. Returns 0 on any error / "N/A" / no key — the caller treats 0 as
// "no IMDb rating, fall back to the TMDB vote".
func (c *Client) fetchImdbRating(ctx context.Context, imdbID string) float64 {
	if c.omdbKey == "" || imdbID == "" {
		return 0
	}
	u := fmt.Sprintf("https://www.omdbapi.com/?i=%s&apikey=%s", url.QueryEscape(imdbID), url.QueryEscape(c.omdbKey))
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var out struct {
		ImdbRating string `json:"imdbRating"` // e.g. "8.8" or "N/A"
		Response   string `json:"Response"`   // "True" | "False"
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || out.Response != "True" {
		return 0
	}
	r, err := strconv.ParseFloat(out.ImdbRating, 64)
	if err != nil {
		return 0
	}
	return r
}

func (c *Client) Trending(ctx context.Context) ([]Match, error) {
	if c.apiKey == "" {
		return nil, ErrDisabled
	}
	if items, ok := c.trendingCached(); ok {
		return items, nil
	}
	items, err := c.fetchTrending(ctx)
	if err != nil {
		return nil, err
	}
	c.setTrendingCache(items)
	return items, nil
}

func (c *Client) trendingCached() ([]Match, bool) {
	c.trendingMu.Lock()
	defer c.trendingMu.Unlock()
	if c.trendingCache != nil && time.Since(c.trendingAt) < trendingTTL {
		return c.trendingCache, true
	}
	return nil, false
}

func (c *Client) fetchTrending(ctx context.Context) ([]Match, error) {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("language", "pt-BR")
	req, _ := http.NewRequestWithContext(ctx, "GET", apiBase+"/trending/all/week?"+q.Encode(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb trending returned %d", resp.StatusCode)
	}
	var out multiSearchResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return buildTrendingItems(out), nil
}

func buildTrendingItems(out multiSearchResp) []Match {
	items := make([]Match, 0, len(out.Results))
	for _, r := range out.Results {
		if r.MediaType != "movie" && r.MediaType != "tv" {
			continue
		}
		if r.PosterPath == "" {
			continue
		}
		m := buildMatchFromResult(r)
		m.PosterURL = imageBase + r.PosterPath
		items = append(items, *m)
	}
	return items
}

func (c *Client) setTrendingCache(items []Match) {
	c.trendingMu.Lock()
	c.trendingCache = items
	c.trendingAt = time.Now()
	c.trendingMu.Unlock()
}

func (c *Client) searchMulti(ctx context.Context, title string, year int) (*Match, error) {
	out, err := c.doSearchMulti(ctx, title, year)
	if err != nil {
		return nil, err
	}
	if len(out.Results) == 0 {
		return nil, nil
	}
	return c.pickBestMatch(ctx, out), nil
}

func (c *Client) doSearchMulti(ctx context.Context, title string, year int) (*multiSearchResp, error) {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("query", title)
	q.Set("language", "pt-BR")
	q.Set("include_adult", "false")
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", apiBase+"/search/multi?"+q.Encode(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb returned %d", resp.StatusCode)
	}
	var out multiSearchResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) pickBestMatch(ctx context.Context, out *multiSearchResp) *Match {
	for _, r := range out.Results {
		if r.MediaType != "movie" && r.MediaType != "tv" {
			continue
		}
		m := buildMatchFromResult(r)
		if r.PosterPath != "" {
			m.PosterURL = imageBase + r.PosterPath
		}
		if imdb := c.fetchImdbID(ctx, r.MediaType, r.ID); imdb != "" {
			m.ImdbID = imdb
			m.ImdbRating = c.fetchImdbRating(ctx, imdb)
		}
		return m
	}
	return nil
}

func buildMatchFromResult(r struct {
	ID           int     `json:"id"`
	MediaType    string  `json:"media_type"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
	Popularity   float64 `json:"popularity"`
}) *Match {
	m := &Match{
		TmdbID:      r.ID,
		Kind:        r.MediaType,
		Overview:    r.Overview,
		VoteAverage: r.VoteAverage,
	}
	if r.MediaType == "movie" {
		m.Title = r.Title
		if y, _ := strconv.Atoi(safePrefix(r.ReleaseDate, 4)); y > 0 {
			m.Year = y
		}
	} else {
		m.Title = r.Name
		if y, _ := strconv.Atoi(safePrefix(r.FirstAirDate, 4)); y > 0 {
			m.Year = y
		}
	}
	return m
}

func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

// FetchEpisodeName returns the localized episode title for a given TV show's TMDB ID, season, and episode.
// Falls back to empty string if not found or on error.
func (c *Client) FetchEpisodeName(ctx context.Context, seriesID int, season, episode int) string {
	if c.apiKey == "" || seriesID <= 0 || season <= 0 || episode <= 0 {
		return ""
	}
	u := fmt.Sprintf("%s/tv/%d/season/%d/episode/%d?api_key=%s&language=pt-BR", apiBase, seriesID, season, episode, url.QueryEscape(c.apiKey))
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	return strings.TrimSpace(out.Name)
}
