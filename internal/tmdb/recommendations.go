package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Recommendations returns TMDB's "recommendations" list for a movie or tv id —
// the same set the site shows under a title's Recommendations tab. Single page
// (~20 items), best-effort. kind MUST be "movie" or "tv": TMDB ids are namespaced
// per type, so calling the wrong endpoint would return an unrelated title's recs.
func (c *Client) Recommendations(ctx context.Context, kind string, tmdbID int) ([]Match, error) {
	if c.apiKey == "" {
		return nil, ErrDisabled
	}
	if kind != "movie" && kind != "tv" {
		return nil, fmt.Errorf("tmdb recommendations: invalid kind %q", kind)
	}
	if tmdbID <= 0 {
		return nil, fmt.Errorf("tmdb recommendations: invalid id %d", tmdbID)
	}
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("language", "pt-BR")
	q.Set("page", "1")
	endpoint := apiBase + "/" + kind + "/" + strconv.Itoa(tmdbID) + "/recommendations?" + q.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb recommendations %s/%d returned %d", kind, tmdbID, resp.StatusCode)
	}
	var out multiSearchResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	// The endpoint returns items of the same kind without media_type → force it.
	return matchesFromResults(out, kind), nil
}
