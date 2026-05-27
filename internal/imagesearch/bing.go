package imagesearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Bing scrapes the image search results page for the original-image URL ("murl"
// field embedded in each result's HTML-escaped JSON attribute). safeSearch=Off
// covers adult titles. Keyless but scrape-based, so it's the fallback after DDG.
type Bing struct {
	http    *http.Client
	baseURL string // overridable in tests
}

func NewBing(hc *http.Client) *Bing {
	return &Bing{http: hc, baseURL: "https://www.bing.com/images/search"}
}

func (b *Bing) Name() string { return "bing" }

// murl lives inside an HTML-escaped JSON blob: ...&quot;murl&quot;:&quot;<url>&quot;...
var bingMurlRe = regexp.MustCompile(`murl&quot;:&quot;(.*?)&quot;`)

func (b *Bing) Find(ctx context.Context, query string) ([]byte, string, error) {
	u, _ := url.Parse(b.baseURL)
	q := u.Query()
	q.Set("q", query)
	q.Set("safeSearch", "Off")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", browserUA)
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("bing %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	for _, m := range bingMurlRe.FindAllSubmatch(body, 10) {
		raw := htmlUnescape(string(m[1]))
		if raw == "" {
			continue
		}
		if data, ct, derr := downloadImage(ctx, b.http, raw); derr == nil {
			return data, ct, nil
		}
	}
	return nil, "", nil
}

// htmlUnescape reverses the small set of entities Bing uses in the murl blob.
func htmlUnescape(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">")
	return r.Replace(s)
}
