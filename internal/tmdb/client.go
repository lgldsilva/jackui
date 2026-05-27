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
	VoteAverage float64 `json:"voteAverage"`
	Kind        string  `json:"kind"` // "movie" | "tv"
}

type Client struct {
	apiKey string
	http   *http.Client
	cache  *sql.DB
}

// New returns a TMDB client. If apiKey is empty the client returns ErrDisabled
// from every call — handlers should surface that as "no enrichment available"
// (404) without exploding.
func New(apiKey, cachePath string) (*Client, error) {
	db, err := sql.Open("sqlite", cachePath+"?_pragma=journal_mode(WAL)")
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
		db.Close()
		return nil, err
	}
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 8 * time.Second},
		cache:  db,
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
	return &m, true
}

func (c *Client) setCached(key string, m *Match) {
	payload := "null"
	if m != nil {
		b, _ := json.Marshal(m)
		payload = string(b)
	}
	c.cache.Exec(`
		INSERT INTO tmdb_match(cache_key, payload, cached_at)
		VALUES(?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(cache_key) DO UPDATE SET payload=excluded.payload, cached_at=CURRENT_TIMESTAMP
	`, key, payload)
}

type multiSearchResp struct {
	Results []struct {
		ID          int     `json:"id"`
		MediaType   string  `json:"media_type"`
		Title       string  `json:"title"`
		Name        string  `json:"name"`
		Overview    string  `json:"overview"`
		PosterPath  string  `json:"poster_path"`
		ReleaseDate string  `json:"release_date"`
		FirstAirDate string `json:"first_air_date"`
		VoteAverage float64 `json:"vote_average"`
		Popularity  float64 `json:"popularity"`
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
	defer resp.Body.Close()
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

func (c *Client) searchMulti(ctx context.Context, title string, year int) (*Match, error) {
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
	if len(out.Results) == 0 {
		return nil, nil
	}
	// Pick the most popular result that's a movie or TV show (skip people).
	for _, r := range out.Results {
		if r.MediaType != "movie" && r.MediaType != "tv" {
			continue
		}
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
		if r.PosterPath != "" {
			m.PosterURL = imageBase + r.PosterPath
		}
		// Resolve the IMDb id once (best-effort) so callers can persist it and
		// never reprocess. A failure here must not lose the otherwise-good match.
		if imdb := c.fetchImdbID(ctx, r.MediaType, r.ID); imdb != "" {
			m.ImdbID = imdb
		}
		return m, nil
	}
	return nil, nil
}

func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}
