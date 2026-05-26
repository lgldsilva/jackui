package jackett

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	URL    string
	APIKey string
	http   *http.Client
}

type Result struct {
	Title       string `json:"title"`
	Tracker     string `json:"tracker"`
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
		http:   &http.Client{Timeout: 30 * time.Second},
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
		return nil, fmt.Errorf("failed to create request: %w", err)
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
		return nil, fmt.Errorf("Jackett API returned %d: %s", resp.StatusCode, string(body))
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
			CategoryID:  categoryID,
			Category:    r.CategoryDesc,
			Size:        r.Size,
			Seeders:     r.Seeders,
			Leechers:    r.Peers - r.Seeders,
			Age:         age,
			MagnetURI:   r.MagnetUri,
			Link:        r.Link,
			InfoHash:    r.InfoHash,
			PublishDate: r.PublishDate,
		})
	}

	return results, nil
}

func (c *Client) GetIndexers() ([]Indexer, error) {
	endpoint := fmt.Sprintf("%s/api/v2.0/indexers/all/results/torznab/api", c.URL)

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v2.0/indexers", c.URL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	q.Set("apikey", c.APIKey)
	req.URL.RawQuery = q.Encode()

	_ = endpoint

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Jackett API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Jackett API returned %d: %s", resp.StatusCode, string(body))
	}

	var rawIndexers []jackettIndexer
	if err := json.NewDecoder(resp.Body).Decode(&rawIndexers); err != nil {
		return nil, fmt.Errorf("failed to decode indexers response: %w", err)
	}

	indexers := make([]Indexer, 0, len(rawIndexers))
	for _, ri := range rawIndexers {
		if ri.Configured {
			indexers = append(indexers, Indexer{
				ID:          ri.ID,
				Name:        ri.Name,
				Description: ri.Description,
				Language:    ri.Language,
				Type:        ri.Type,
				Configured:  ri.Configured,
			})
		}
	}

	return indexers, nil
}

func (c *Client) TestConnection() error {
	endpoint := fmt.Sprintf("%s/api/v2.0/indexers", c.URL)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	q := req.URL.Query()
	q.Set("apikey", c.APIKey)
	req.URL.RawQuery = q.Encode()

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid API key")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
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
		"2006-01-02 15:04:05",
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
