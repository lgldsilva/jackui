package subtitles

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearch_ResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, strings.Repeat(" ", maxJSONBytes+100))
	}))
	t.Cleanup(srv.Close)

	_, err := testSubClient(t, srv, "testkey", "", "", "").SearchAuto(SearchOpts{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("expected explicit size-limit error, got: %v", err)
	}
}

func TestFetchSubtitle_TooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, strings.Repeat("a", maxSubtitleFileBytes+100))
	}))
	t.Cleanup(srv.Close)

	_, err := testSubClient(t, srv, "testkey", "", "", "").fetchSubtitle(srv.URL + "/file.srt")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected explicit size-limit error, got: %v", err)
	}
}

func TestFetchSubtitle_Normal(t *testing.T) {
	const srt = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, srt)
	}))
	t.Cleanup(srv.Close)

	raw, err := testSubClient(t, srv, "testkey", "", "", "").fetchSubtitle(srv.URL + "/file.srt")
	if err != nil {
		t.Fatalf("fetchSubtitle: %v", err)
	}
	if string(raw) != srt {
		t.Fatalf("unexpected body: %q", raw)
	}
}
