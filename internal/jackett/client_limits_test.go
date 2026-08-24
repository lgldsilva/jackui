package jackett

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// oversizedJackettServer answers every request with a body of `size` bytes of
// whitespace — big enough to trip the response caps without encoding anything.
func oversizedJackettServer(t *testing.T, size int64) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, strings.Repeat(" ", int(size)))
	}))
	t.Cleanup(srv.Close)
	return srv, New(srv.URL, "testkey")
}

func TestSearch_ResponseTooLarge(t *testing.T) {
	_, client := oversizedJackettServer(t, maxSearchResponseBytes+100)
	_, err := client.Search("ubuntu", "", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected explicit size-limit error, got: %v", err)
	}
}

func TestSearchOnIndexer_ResponseTooLarge(t *testing.T) {
	_, client := oversizedJackettServer(t, maxSearchResponseBytes+100)
	_, err := client.SearchOnIndexer(context.Background(), "1337x", "q", "")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected explicit size-limit error, got: %v", err)
	}
}

func TestGetIndexers_ResponseTooLarge(t *testing.T) {
	_, client := oversizedJackettServer(t, maxIndexersResponseBytes+100)
	_, err := client.GetIndexers()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected explicit size-limit error, got: %v", err)
	}
}

func TestListIndexers_ResponseTooLarge(t *testing.T) {
	_, client := oversizedJackettServer(t, maxIndexersResponseBytes+100)
	_, err := client.ListIndexers()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected explicit size-limit error, got: %v", err)
	}
}

// TestSearch_ErrorBodyCapped: an error response body is echoed into the error
// string, so it must be bounded — a Jackett returning a huge error page must
// not balloon our error/log output.
func TestSearch_ErrorBodyCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, strings.Repeat("e", 64<<10))
	}))
	t.Cleanup(srv.Close)

	_, err := New(srv.URL, "testkey").Search("x", "", nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if len(err.Error()) > maxErrorBodyBytes+128 { // + status/message boilerplate
		t.Fatalf("error body not capped: message is %d bytes", len(err.Error()))
	}
}
