package watchlist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A configured token must be sent as Authorization: Bearer so protected /
// self-hosted ntfy topics accept the push.
func TestNtfyPoster_SetsBearerWhenTokenSet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &NtfyPoster{BaseURL: srv.URL, Token: "tk_secret"}
	if err := p.Notify(context.Background(), "topic", "title", "body", ""); err != nil {
		t.Fatalf("Notify err: %v", err)
	}
	if gotAuth != "Bearer tk_secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tk_secret")
	}
}

// No token → no Authorization header (anonymous public-topic push, unchanged).
func TestNtfyPoster_NoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &NtfyPoster{BaseURL: srv.URL}
	if err := p.Notify(context.Background(), "topic", "title", "body", ""); err != nil {
		t.Fatalf("Notify err: %v", err)
	}
	if hadAuth {
		t.Error("Authorization header must be absent when no token configured")
	}
}
