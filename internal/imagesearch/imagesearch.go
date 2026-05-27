// Package imagesearch finds a poster/cover image for a title on the open web.
// It exists for content TMDB doesn't cover (adult, obscure, non-catalogued
// releases): the art resolver calls it only AFTER a TMDB lookup fails. Sources
// are pluggable and tried in order as a fallback chain — a keyless web image
// search today, with room for an LLM-tool-use source or a dedicated database
// behind the same interface.
package imagesearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// browserUA is sent on every request — image search front-ends gate or reshape
// their responses for non-browser clients.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// maxImageBytes caps a downloaded image so a mislabeled huge file can't blow up
// memory or the .art cache.
const maxImageBytes = 10 << 20 // 10 MiB

// Source is one image provider. Find returns image bytes + content type for the
// best match, or (nil, "", nil) when it simply found nothing (NOT an error, so
// the chain keeps trying the next source).
type Source interface {
	Name() string
	Find(ctx context.Context, query string) ([]byte, string, error)
}

// Chain tries each source in order and returns the first usable image. It also
// owns the shared HTTP client the sources use.
type Chain struct {
	sources []Source
	http    *http.Client
}

// NewChain builds the default keyless web-search chain (DuckDuckGo → Bing).
// Returns nil if you pass no sources, so callers can treat nil as "disabled".
func NewChain(sources ...Source) *Chain {
	if len(sources) == 0 {
		return nil
	}
	return &Chain{sources: sources}
}

// Default returns the standard keyless chain. Separate from NewChain so tests
// can inject mock sources.
func Default() *Chain {
	hc := &http.Client{Timeout: 12 * time.Second}
	return &Chain{
		http:    hc,
		sources: []Source{NewDuckDuckGo(hc), NewBing(hc)},
	}
}

// Find walks the source chain. Returns the image bytes, its content type, and
// the name of the source that produced it. A nil error with nil data means no
// source found anything.
func (c *Chain) Find(ctx context.Context, query string) (data []byte, contentType, source string, err error) {
	query = strings.TrimSpace(query)
	if c == nil || query == "" {
		return nil, "", "", nil
	}
	var lastErr error
	for _, s := range c.sources {
		d, ct, ferr := s.Find(ctx, query)
		if ferr != nil {
			lastErr = ferr
			continue
		}
		if len(d) > 0 {
			return d, ct, s.Name(), nil
		}
	}
	return nil, "", "", lastErr
}

// downloadImage fetches an image URL and returns its bytes + content type,
// rejecting non-image payloads and capping the size. Shared by all sources.
func downloadImage(ctx context.Context, hc *http.Client, imageURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", browserUA)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("imagesearch: download %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, "", err
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		// Some CDNs omit/mislabel the header — sniff the bytes before rejecting.
		sniff := http.DetectContentType(data)
		if !strings.HasPrefix(sniff, "image/") {
			return nil, "", fmt.Errorf("imagesearch: not an image (%s)", ct)
		}
		ct = sniff
	}
	if len(data) < 1024 {
		return nil, "", fmt.Errorf("imagesearch: image too small")
	}
	return data, ct, nil
}
