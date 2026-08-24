package tmdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON_ExceedsSizeLimit(t *testing.T) {
	var out multiSearchResp
	err := decodeJSON(strings.NewReader(strings.Repeat(" ", maxResponseBytes+1)), &out)
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("expected explicit size-limit error, got: %v", err)
	}
}

func TestDecodeJSON_NormalPayload(t *testing.T) {
	var out multiSearchResp
	if err := decodeJSON(strings.NewReader(`{"results":[{"id":1,"media_type":"movie","title":"X"}]}`), &out); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].Title != "X" {
		t.Fatalf("unexpected payload: %+v", out)
	}
}

// TestRecommendations_ResponseTooLarge proves the cap through the full HTTP
// path: an upstream returning a huge body fails with an explicit error instead
// of an unbounded buffer or a truncated-JSON decode error.
func TestRecommendations_ResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes+100))
	}))
	t.Cleanup(srv.Close)

	_, err := testClient(t, srv).Recommendations(context.Background(), "movie", 550)
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("expected explicit size-limit error, got: %v", err)
	}
}
