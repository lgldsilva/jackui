package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecommendations_ReturnsMoviesWithForcedKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/movie/550/recommendations") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// No media_type in the payload — Recommendations must force "movie".
		_, _ = w.Write([]byte(`{"results":[
			{"id":11,"title":"Rec One","poster_path":"/r1.jpg","popularity":30},
			{"id":12,"title":"No Poster","popularity":20}
		]}`))
	}))
	defer srv.Close()
	c := testClient(t, srv)

	items, err := c.Recommendations(context.Background(), "movie", 550)
	if err != nil {
		t.Fatalf("Recommendations: %v", err)
	}
	// The poster-less item is dropped (matchesFromResults rule).
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].TmdbID != 11 || items[0].Kind != "movie" {
		t.Errorf("expected movie #11, got id=%d kind=%q", items[0].TmdbID, items[0].Kind)
	}
}

func TestRecommendations_RejectsInvalidKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // must never be hit
	}))
	defer srv.Close()
	c := testClient(t, srv)

	if _, err := c.Recommendations(context.Background(), "person", 550); err == nil {
		t.Error("expected error for invalid kind")
	}
	if _, err := c.Recommendations(context.Background(), "movie", 0); err == nil {
		t.Error("expected error for invalid id")
	}
}

func TestRecommendations_UpstreamErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := testClient(t, srv)

	if _, err := c.Recommendations(context.Background(), "tv", 1399); err == nil {
		t.Error("expected error on non-200 upstream")
	}
}

func TestRecommendations_InvalidJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := testClient(t, srv)

	if _, err := c.Recommendations(context.Background(), "movie", 1); err == nil {
		t.Error("expected decode error on malformed body")
	}
}

func TestRecommendations_DisabledWithoutKey(t *testing.T) {
	c := &Client{} // no apiKey
	if _, err := c.Recommendations(context.Background(), "movie", 1); err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
}
