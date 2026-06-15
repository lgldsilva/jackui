package musictrending

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleFeed = `{"feed":{"results":[
{"artistName":"A","name":"Album One","artworkUrl100":"https://x/100x100bb.jpg","url":"https://music/1","releaseDate":"2026-01-01"},
{"artistName":"B","name":"Album Two","artworkUrl100":"https://x/100x100bb.jpg","url":"https://music/2","releaseDate":"2026-02-02"},
{"artistName":"C","name":""}
]}}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewForTest(srv.URL)
}

func TestTopParsesAndUpscales(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, sampleFeed) })
	albums, err := c.Top(context.Background(), "br", 10)
	if err != nil {
		t.Fatalf("Top: %v", err)
	}
	if len(albums) != 2 { // the empty-name entry is dropped
		t.Fatalf("want 2 albums, got %d", len(albums))
	}
	if albums[0].Artist != "A" || albums[0].Name != "Album One" {
		t.Fatalf("bad parse: %+v", albums[0])
	}
	if !strings.Contains(albums[0].Artwork, "512x512bb") {
		t.Fatalf("artwork not upscaled: %s", albums[0].Artwork)
	}
}

func TestTopLowercasesCountryInPath(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, sampleFeed)
	})
	_, _ = c.Top(context.Background(), "GB", 5)
	if !strings.Contains(gotPath, "/gb/") {
		t.Fatalf("country not lowercased in path: %s", gotPath)
	}
}

func TestTopInvalidCountryDefaultsUS(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, sampleFeed)
	})
	_, _ = c.Top(context.Background(), "zzz", 5)
	if !strings.Contains(gotPath, "/us/") {
		t.Fatalf("invalid country should default to us: %s", gotPath)
	}
}

func TestTopCaches(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, sampleFeed)
	})
	_, _ = c.Top(context.Background(), "us", 10)
	_, _ = c.Top(context.Background(), "us", 10)
	if calls != 1 {
		t.Fatalf("expected 1 upstream call (cached), got %d", calls)
	}
}

func TestTopLimitSlices(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, sampleFeed) })
	albums, _ := c.Top(context.Background(), "us", 1)
	if len(albums) != 1 {
		t.Fatalf("limit not applied: got %d", len(albums))
	}
}

func TestTopUpstreamError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	if _, err := c.Top(context.Background(), "us", 10); err == nil {
		t.Fatal("expected error on upstream 500")
	}
}
