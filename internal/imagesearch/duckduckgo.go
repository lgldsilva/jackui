package imagesearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
)

// DuckDuckGo searches DDG's image endpoint, which needs a one-time "vqd" token
// scraped from the HTML page before the JSON i.js call is allowed. Safe-search
// is disabled (p=-1) so it also returns results for adult titles. Keyless.
type DuckDuckGo struct {
	http    *http.Client
	htmlURL string // token page; overridable in tests
	apiURL  string // i.js JSON endpoint; overridable in tests
}

func NewDuckDuckGo(hc *http.Client) *DuckDuckGo {
	return &DuckDuckGo{http: hc, htmlURL: "https://duckduckgo.com/", apiURL: "https://duckduckgo.com/i.js"}
}

func (d *DuckDuckGo) Name() string { return "duckduckgo" }

var vqdRe = regexp.MustCompile(`vqd=["']?([0-9-]+)["']?`)

type ddgResponse struct {
	Results []struct {
		Image  string `json:"image"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"results"`
}

func (d *DuckDuckGo) Find(ctx context.Context, query string) ([]byte, string, error) {
	vqd, err := d.token(ctx, query)
	if err != nil || vqd == "" {
		return nil, "", err
	}

	u, _ := url.Parse(d.apiURL)
	q := u.Query()
	q.Set("l", "us-en")
	q.Set("o", "json")
	q.Set("q", query)
	q.Set("vqd", vqd)
	q.Set("f", ",,,")
	q.Set("p", "-1") // safe search off
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", d.htmlURL)
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("ddg i.js %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var dr ddgResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return nil, "", fmt.Errorf("ddg parse: %w", err)
	}
	// Try results in order; the first that downloads as a real image wins.
	for _, r := range dr.Results {
		if r.Image == "" {
			continue
		}
		if data, ct, derr := downloadImage(ctx, d.http, r.Image); derr == nil {
			return data, ct, nil
		}
	}
	return nil, "", nil
}

// token scrapes the vqd value DDG requires before serving image JSON.
func (d *DuckDuckGo) token(ctx context.Context, query string) (string, error) {
	u, _ := url.Parse(d.htmlURL)
	q := u.Query()
	q.Set("q", query)
	q.Set("iax", "images")
	q.Set("ia", "images")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", browserUA)
	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	m := vqdRe.FindSubmatch(body)
	if m == nil {
		return "", nil
	}
	return string(m[1]), nil
}
