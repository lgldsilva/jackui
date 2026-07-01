package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"
)

// discoverPages is how many 20-item pages to pull per kind in a filtered query.
const discoverPages = 2

// Genre is a TMDB genre (movie or tv) for the Discover filter dropdown.
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

var (
	genresMu    sync.Mutex
	genresCache []Genre
	genresAt    time.Time
)

const genresTTL = 30 * 24 * time.Hour

// Discover queries TMDB's /discover for movies AND tv with optional year/genre
// filters, merged and sorted by popularity (the Em Alta filters). Direction
// tags don't apply here (it's a filtered query, not the weekly ranking).
func (c *Client) Discover(ctx context.Context, year, genre int) ([]Match, error) {
	if c.apiKey == "" {
		return nil, ErrDisabled
	}
	movies, err := c.discoverKind(ctx, "movie", year, genre)
	if err != nil {
		return nil, err
	}
	shows, err := c.discoverKind(ctx, "tv", year, genre)
	if err != nil {
		shows = nil // best-effort: keep the movies we already have
	}
	all := append(movies, shows...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Popularity > all[j].Popularity })
	// Dedupe AFTER sorting so the surviving copy is the most-popular one. TMDB
	// paginates /discover by popularity.desc, whose ranking shifts between HTTP
	// calls, so the same title can land on more than one page.
	return dedupeMatches(all), nil
}

// dedupeMatches drops repeated titles (same Kind+TmdbID), keeping the first
// occurrence. TMDB's paginated endpoints can return the same item on more than
// one page (the popularity ranking shifts between requests); a duplicate would
// otherwise reach the UI as two cards sharing one React key ("kind-tmdbId"),
// which corrupts reconciliation and visibly duplicates cards. Returns a fresh
// slice (never mutates the input). Note: movie and tv share a numeric id space
// in TMDB, so the Kind is part of the key.
func dedupeMatches(in []Match) []Match {
	seen := make(map[string]struct{}, len(in))
	out := make([]Match, 0, len(in))
	for _, m := range in {
		k := m.Kind + ":" + strconv.Itoa(m.TmdbID)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, m)
	}
	return out
}

func (c *Client) discoverKind(ctx context.Context, kind string, year, genre int) ([]Match, error) {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("language", "pt-BR")
	q.Set("sort_by", "popularity.desc")
	q.Set("include_adult", "false")
	if year > 0 {
		if kind == "movie" {
			q.Set("primary_release_year", strconv.Itoa(year))
		} else {
			q.Set("first_air_date_year", strconv.Itoa(year))
		}
	}
	if genre > 0 {
		q.Set("with_genres", strconv.Itoa(genre))
	}
	var all []Match
	for page := 1; page <= discoverPages; page++ {
		q.Set("page", strconv.Itoa(page))
		items, err := c.fetchDiscoverPage(ctx, kind, q)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		if len(items) == 0 {
			break
		}
		all = append(all, items...)
	}
	return all, nil
}

func (c *Client) fetchDiscoverPage(ctx context.Context, kind string, q url.Values) ([]Match, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", apiBase+"/discover/"+kind+"?"+q.Encode(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb discover %s returned %d", kind, resp.StatusCode)
	}
	var out multiSearchResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return matchesFromResults(out, kind), nil // discover omits media_type → force it
}

// Genres returns the merged movie+tv genre list (deduped), cached for genresTTL.
func (c *Client) Genres(ctx context.Context) ([]Genre, error) {
	if c.apiKey == "" {
		return nil, ErrDisabled
	}
	genresMu.Lock()
	if genresCache != nil && time.Since(genresAt) < genresTTL {
		defer genresMu.Unlock()
		return genresCache, nil
	}
	genresMu.Unlock()

	seen := map[int]bool{}
	var merged []Genre
	for _, kind := range []string{"movie", "tv"} {
		gs, err := c.fetchGenres(ctx, kind)
		if err != nil {
			if len(merged) == 0 {
				return nil, err
			}
			break
		}
		for _, g := range gs {
			if !seen[g.ID] {
				seen[g.ID] = true
				merged = append(merged, g)
			}
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })

	genresMu.Lock()
	genresCache, genresAt = merged, time.Now()
	genresMu.Unlock()
	return merged, nil
}

func (c *Client) fetchGenres(ctx context.Context, kind string) ([]Genre, error) {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("language", "pt-BR")
	req, _ := http.NewRequestWithContext(ctx, "GET", apiBase+"/genre/"+kind+"/list?"+q.Encode(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb genres %s returned %d", kind, resp.StatusCode)
	}
	var out struct {
		Genres []Genre `json:"genres"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Genres, nil
}
