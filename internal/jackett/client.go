package jackett

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/luizg/jackui/internal/dbutil"
)

const (
	errFailedToCreateRequest = "failed to create request: %w"
	torznabAPIEndpoint       = "/api/v2.0/indexers/all/results/torznab/api"
)

type Client struct {
	URL    string
	APIKey string
	http   *http.Client
}

// stripAPIKey removes the apikey query param from a Jackett download link before
// it's handed to the browser — the key must not travel client-side. The streamer
// re-injects it server-side when fetching (see streamer.addFromTorrentURL).
func stripAPIKey(link string) string {
	if link == "" {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	q := u.Query()
	if q.Get("apikey") == "" {
		return link
	}
	q.Del("apikey")
	u.RawQuery = q.Encode()
	return u.String()
}

type Result struct {
	Title       string `json:"title"`
	Tracker     string `json:"tracker"`
	TrackerID   string `json:"trackerId"`
	CategoryID  int    `json:"categoryId"`
	Category    string `json:"category"`
	Size        int64  `json:"size"`
	Seeders     int    `json:"seeders"`
	Leechers    int    `json:"leechers"`
	Age         string `json:"age"`
	MagnetURI   string `json:"magnetUri"`
	Link        string `json:"link"`
	InfoHash    string `json:"infoHash"`
	PublishDate string `json:"publishDate"`
}

type Indexer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Type        string `json:"type"`
	Configured  bool   `json:"configured"`
}

// jackettResponse is the raw API response
type jackettResponse struct {
	Results []jackettResult `json:"Results"`
}

type jackettResult struct {
	Title                string  `json:"Title"`
	Tracker              string  `json:"Tracker"`
	TrackerId            string  `json:"TrackerId"`
	CategoryDesc         string  `json:"CategoryDesc"`
	Category             []int   `json:"Category"`
	Size                 int64   `json:"Size"`
	Seeders              int     `json:"Seeders"`
	Peers                int     `json:"Peers"`
	MagnetUri            string  `json:"MagnetUri"`
	Link                 string  `json:"Link"`
	InfoHash             string  `json:"InfoHash"`
	PublishDate          string  `json:"PublishDate"`
	Gain                 float64 `json:"Gain"`
}

type jackettIndexer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Type        string `json:"type"`
	Configured  bool   `json:"configured"`
}

func New(apiURL, apiKey string) *Client {
	return &Client{
		URL:    strings.TrimRight(apiURL, "/"),
		APIKey: apiKey,
		http:   &http.Client{Timeout: 150 * time.Second},
	}
}

func (c *Client) Search(query, category string, indexers []string) ([]Result, error) {
	indexer := "all"
	if len(indexers) > 0 && indexers[0] != "" && indexers[0] != "all" {
		indexer = strings.Join(indexers, ",")
	}

	endpoint := fmt.Sprintf("%s/api/v2.0/indexers/%s/results", c.URL, url.PathEscape(indexer))

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf(errFailedToCreateRequest, err)
	}

	q := req.URL.Query()
	q.Set("apikey", c.APIKey)
	q.Set("Query", query)
	if category != "" && category != "all" {
		q.Set("Category[]", category)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Jackett API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jackett API returned %d: %s", resp.StatusCode, string(body))
	}

	var jackResp jackettResponse
	if err := json.NewDecoder(resp.Body).Decode(&jackResp); err != nil {
		return nil, fmt.Errorf("failed to decode Jackett response: %w", err)
	}

	results := make([]Result, 0, len(jackResp.Results))
	for _, r := range jackResp.Results {
		categoryID := 0
		if len(r.Category) > 0 {
			categoryID = r.Category[0]
		}

		age := formatAge(r.PublishDate)

		results = append(results, Result{
			Title:       r.Title,
			Tracker:     r.Tracker,
			TrackerID:   r.TrackerId,
			CategoryID:  categoryID,
			Category:    r.CategoryDesc,
			Size:        r.Size,
			Seeders:     r.Seeders,
			Leechers:    r.Peers - r.Seeders,
			Age:         age,
			MagnetURI:   r.MagnetUri,
			Link:        stripAPIKey(r.Link),
			InfoHash:    r.InfoHash,
			PublishDate: r.PublishDate,
		})
	}

	return results, nil
}

func (c *Client) GetIndexers() ([]Indexer, error) {
	endpoint := c.URL + torznabAPIEndpoint

	// Jackett's /api/v2.0/indexers only works for admin-cookie sessions; with
	// apikey-only auth it returns 302 → /UI/Login. We try once, but if we hit
	// the login redirect we return an empty list (not an error) so the frontend
	// degrades gracefully into "search-all-indexers" mode without breaking.
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v2.0/indexers", c.URL), nil)
	if err != nil {
		return nil, fmt.Errorf(errFailedToCreateRequest, err)
	}

	q := req.URL.Query()
	q.Set("apikey", c.APIKey)
	q.Set("configured", "true")
	req.URL.RawQuery = q.Encode()

	_ = endpoint

	noFollow := &http.Client{
		Timeout: c.http.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noFollow.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Jackett API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		// Jackett requires admin login for this endpoint — return empty list as
		// a deliberate "feature degraded" signal. The UI shows a hint.
		return []Indexer{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jackett API returned %d: %s", resp.StatusCode, string(body))
	}

	var rawIndexers []jackettIndexer
	if err := json.NewDecoder(resp.Body).Decode(&rawIndexers); err != nil {
		return nil, fmt.Errorf("failed to decode indexers response: %w", err)
	}

	indexers := make([]Indexer, 0, len(rawIndexers))
	for _, ri := range rawIndexers {
		if ri.Configured {
			indexers = append(indexers, Indexer(ri))
		}
	}

	return indexers, nil
}

// TestConnection probes Jackett to confirm URL + apikey are valid.
//
// We deliberately hit the torznab caps endpoint (`/api/v2.0/indexers/all/results/torznab/api?t=caps`)
// instead of `/api/v2.0/indexers`. Recent Jackett versions only allow the latter
// after an admin login (the apikey alone gets redirected to /UI/Login), but the
// torznab path always honors apikey — so it's the right "is Jackett reachable +
// authorized" probe. A 200 means everything works; 302 means we hit the login
// redirect (key invalid or Jackett misconfigured); other codes are real errors.
func (c *Client) TestConnection() error {
	endpoint := c.URL + torznabAPIEndpoint
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return fmt.Errorf(errFailedToCreateRequest, err)
	}
	q := req.URL.Query()
	q.Set("apikey", c.APIKey)
	q.Set("t", "caps")
	req.URL.RawQuery = q.Encode()

	// Don't follow the auth-redirect — if Jackett wants login, we want to know.
	httpClient := &http.Client{
		Timeout: c.http.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusFound, http.StatusMovedPermanently:
		return fmt.Errorf("API key inválida (Jackett redirecionou para login)")
	case http.StatusForbidden, http.StatusUnauthorized:
		return fmt.Errorf("invalid API key")
	default:
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
}

// ─── Per-indexer streaming search ────────────────────────────────────────────
//
// Background: /api/v2.0/indexers/all/results blocks until EVERY configured indexer
// responds (or times out internally). For 100+ indexers this is ~100s before
// any client sees results. We can do better by querying each indexer concurrently
// and emitting their hits via a callback as soon as each finishes.

// listIndexersResponse mirrors the XML returned by /torznab/api?t=indexers
type listIndexersResponse struct {
	XMLName  xml.Name `xml:"indexers"`
	Indexers []struct {
		ID         string `xml:"id,attr"`
		Configured string `xml:"configured,attr"`
		Title      string `xml:"title"`
		Language   string `xml:"language"`
		Type       string `xml:"type"`
	} `xml:"indexer"`
}

// ListIndexers returns the configured indexers via Jackett's torznab `t=indexers` endpoint.
// This works with API key (no admin cookie needed), unlike /api/v2.0/indexers.
func (c *Client) ListIndexers() ([]Indexer, error) {
	endpoint := c.URL + torznabAPIEndpoint
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	q := req.URL.Query()
	q.Set("apikey", c.APIKey)
	q.Set("t", "indexers")
	q.Set("configured", "true")
	req.URL.RawQuery = q.Encode()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list indexers: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list indexers returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed listIndexersResponse
	if err := xml.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode indexers xml: %w", err)
	}

	out := make([]Indexer, 0, len(parsed.Indexers))
	for _, idx := range parsed.Indexers {
		if idx.Configured != "true" {
			continue
		}
		out = append(out, Indexer{
			ID:         idx.ID,
			Name:       idx.Title,
			Language:   idx.Language,
			Type:       idx.Type,
			Configured: true,
		})
	}
	return out, nil
}

// SearchOnIndexer queries one specific indexer (by id). Used for parallel fan-out.
func (c *Client) SearchOnIndexer(ctx context.Context, indexerID, query, category string) ([]Result, error) {
	endpoint := fmt.Sprintf("%s/api/v2.0/indexers/%s/results", c.URL, url.PathEscape(indexerID))
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	q := req.URL.Query()
	q.Set("apikey", c.APIKey)
	q.Set("Query", query)
	if category != "" && category != "all" {
		q.Set("Category[]", category)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("indexer %s returned %d", indexerID, resp.StatusCode)
	}

	var jackResp jackettResponse
	if err := json.NewDecoder(resp.Body).Decode(&jackResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]Result, 0, len(jackResp.Results))
	for _, r := range jackResp.Results {
		categoryID := 0
		if len(r.Category) > 0 {
			categoryID = r.Category[0]
		}
		results = append(results, Result{
			Title:       r.Title,
			Tracker:     r.Tracker,
			CategoryID:  categoryID,
			Category:    r.CategoryDesc,
			Size:        r.Size,
			Seeders:     r.Seeders,
			Leechers:    r.Peers - r.Seeders,
			Age:         formatAge(r.PublishDate),
			MagnetURI:   r.MagnetUri,
			Link:        stripAPIKey(r.Link),
			InfoHash:    r.InfoHash,
			PublishDate: r.PublishDate,
		})
	}
	return results, nil
}

// IndexerHit is what the streaming search callback receives per indexer.
// One IndexerHit means one indexer is finished (or failed) — emit its results.
type IndexerHit struct {
	IndexerID   string
	IndexerName string
	Results     []Result
	Err         error
	Duration    time.Duration
}

// StreamSearch fires concurrent queries to every configured indexer (or a filtered subset).
// Invokes `onHit` from a single goroutine as each indexer finishes. Blocks until all done or ctx cancels.
//
// `indexers` filters which to query; empty or ["all"] means every configured indexer.
// `perIndexerTimeout` caps each individual query (default 30s); slow indexers don't block the rest.
func (c *Client) StreamSearch(
	ctx context.Context,
	query, category string,
	indexers []string,
	perIndexerTimeout time.Duration,
	onHit func(IndexerHit),
) error {
	if perIndexerTimeout == 0 {
		perIndexerTimeout = 30 * time.Second
	}

	all, err := c.ListIndexers()
	if err != nil {
		return fmt.Errorf("list indexers: %w", err)
	}

	// Filter if user picked specific indexers
	want := make(map[string]bool)
	if len(indexers) > 0 && indexers[0] != "all" && indexers[0] != "" {
		for _, id := range indexers {
			want[id] = true
		}
	}
	targets := make([]Indexer, 0, len(all))
	for _, idx := range all {
		if len(want) == 0 || want[idx.ID] {
			targets = append(targets, idx)
		}
	}

	// Serializing onHit avoids races in the caller
	var mu sync.Mutex
	emit := func(h IndexerHit) {
		mu.Lock()
		defer mu.Unlock()
		onHit(h)
	}

	var wg sync.WaitGroup
	for _, idx := range targets {
		wg.Add(1)
		go func(idx Indexer) {
			defer wg.Done()
			t0 := time.Now()
			ictx, cancel := context.WithTimeout(ctx, perIndexerTimeout)
			defer cancel()
			results, err := c.SearchOnIndexer(ictx, idx.ID, query, category)
			emit(IndexerHit{
				IndexerID:   idx.ID,
				IndexerName: idx.Name,
				Results:     results,
				Err:         err,
				Duration:    time.Since(t0),
			})
		}(idx)
	}
	wg.Wait()
	return nil
}

func formatAge(publishDate string) string {
	if publishDate == "" {
		return "unknown"
	}

	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		dbutil.TimeFormat,
	}

	var t time.Time
	var parseErr error
	for _, format := range formats {
		t, parseErr = time.Parse(format, publishDate)
		if parseErr == nil {
			break
		}
	}

	if parseErr != nil {
		return publishDate
	}

	diff := time.Since(t)
	switch {
	case diff < time.Hour:
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	case diff < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
	case diff < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(diff.Hours()/(24*7)))
	case diff < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(diff.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(diff.Hours()/(24*365)))
	}
}
