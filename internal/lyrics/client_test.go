package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToLyrics(t *testing.T) {
	if _, ok := toLyrics(lrclibResp{Instrumental: true, SyncedLyrics: "x"}); ok {
		t.Error("instrumental must be treated as no lyrics")
	}
	if _, ok := toLyrics(lrclibResp{}); ok {
		t.Error("empty must be no lyrics")
	}
	l, ok := toLyrics(lrclibResp{SyncedLyrics: "[00:01.00] hi", PlainLyrics: "hi"})
	if !ok || l.Synced == "" || l.Source != "lrclib" {
		t.Errorf("expected synced lyrics, got %+v ok=%v", l, ok)
	}
}

func TestCacheKeyNormalizes(t *testing.T) {
	if cacheKey(" A ", "B", "C") != cacheKey("a", "b", "c") {
		t.Error("cacheKey must lowercase + trim")
	}
}

func TestGetExactThenCache(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.HasPrefix(r.URL.Path, "/api/get") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if ua := r.Header.Get("User-Agent"); ua == "" {
			t.Error("expected identifying User-Agent")
		}
		_, _ = w.Write([]byte(`{"syncedLyrics":"[00:01.00] hi","plainLyrics":"hi"}`))
	}))
	defer srv.Close()
	old := baseURL
	baseURL = srv.URL
	defer func() { baseURL = old }()

	c := New()
	got, err := c.Get(context.Background(), "Artist", "Title", "Album", 200)
	if err != nil || got.Source != "lrclib" || !strings.Contains(got.Synced, "hi") {
		t.Fatalf("Get: %+v err=%v", got, err)
	}
	// Second call must hit the in-memory cache (no new HTTP request).
	if _, err := c.Get(context.Background(), "Artist", "Title", "Album", 200); err != nil {
		t.Fatalf("Get(cached): %v", err)
	}
	if hits != 1 {
		t.Errorf("expected 1 upstream hit (then cache), got %d", hits)
	}
}

func TestSearchFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/get") {
			w.WriteHeader(http.StatusNotFound) // exact miss → triggers search
			return
		}
		_, _ = w.Write([]byte(`[{"instrumental":true},{"syncedLyrics":"[00:02.00] yo","plainLyrics":"yo"}]`))
	}))
	defer srv.Close()
	old := baseURL
	baseURL = srv.URL
	defer func() { baseURL = old }()

	got, err := New().Get(context.Background(), "A", "T", "", 0)
	if err != nil || !strings.Contains(got.Synced, "yo") {
		t.Fatalf("search fallback: %+v err=%v", got, err)
	}
}

func TestGetMissReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	old := baseURL
	baseURL = srv.URL
	defer func() { baseURL = old }()

	got, err := New().Get(context.Background(), "A", "T", "", 0)
	if err != nil {
		t.Fatalf("miss must not error: %v", err)
	}
	if got.Source != "" {
		t.Errorf("miss must have empty source, got %q", got.Source)
	}
}

func TestGetRequiresTitle(t *testing.T) {
	if _, err := New().Get(context.Background(), "A", "  ", "", 0); err == nil {
		t.Error("expected error when title is blank")
	}
}
