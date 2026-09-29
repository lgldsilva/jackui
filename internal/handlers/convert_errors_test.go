package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The SSRF dialer runs AFTER DNS resolution and must refuse loopback targets —
// httptest listens on 127.0.0.1, so the REAL client (not the permissive test
// swap) has to fail the dial with the "destination address not allowed" error.
func TestResolveTorrentToMagnet_LoopbackBlockedByDialer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res, cerr := resolveTorrentToMagnet(srv.URL + "/x.torrent")
	if cerr == nil {
		t.Fatal("loopback destination must be refused by the SSRF dialer")
	}
	if res != nil {
		t.Errorf("resolution should be nil on error, got %+v", res)
	}
	if cerr.Code != http.StatusBadGateway || !strings.Contains(cerr.Message, "destination address not allowed") {
		t.Errorf("cerr = %+v, want 502 with the dialer's refusal", cerr)
	}
}

func TestResolveTorrentToMagnet_RedirectWithoutLocation(t *testing.T) {
	withLoopbackClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusFound) // 302 with no Location header
	}))
	defer srv.Close()

	_, cerr := resolveTorrentToMagnet(srv.URL + "/dl")
	if cerr == nil || cerr.Code != http.StatusBadGateway || !strings.Contains(cerr.Message, "redirect without Location") {
		t.Fatalf("cerr = %+v, want 502 redirect-without-Location", cerr)
	}
}

// net/http already rejects an unparsable Location before CheckRedirect runs, so
// followRedirect's own guard is only reachable with a hand-built response.
func TestFollowRedirect_InvalidLocation(t *testing.T) {
	reqURL, _ := url.Parse("http://indexer.example/dl")
	resp := &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{"http://[::1"}},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    &http.Request{URL: reqURL},
	}

	res, next, cerr := followRedirect(resp)
	if cerr == nil || cerr.Code != http.StatusBadGateway || !strings.Contains(cerr.Message, "invalid redirect Location") {
		t.Fatalf("cerr = %+v, want 502 invalid-redirect-Location", cerr)
	}
	if res != nil || next != "" {
		t.Errorf("res=%+v next=%q, want nil/empty on error", res, next)
	}
}

func TestResolveTorrentToMagnet_InvalidIndexerMagnet(t *testing.T) {
	withLoopbackClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "magnet:?xt=urn:btih:not-a-valid-hash")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	_, cerr := resolveTorrentToMagnet(srv.URL + "/dl/magnetdownload")
	if cerr == nil || cerr.Code != http.StatusBadGateway || !strings.Contains(cerr.Message, "invalid indexer magnet") {
		t.Fatalf("cerr = %+v, want 502 invalid-indexer-magnet", cerr)
	}
}

func TestResolveTorrentToMagnet_ServerError(t *testing.T) {
	withLoopbackClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, cerr := resolveTorrentToMagnet(srv.URL + "/x.torrent")
	if cerr == nil || cerr.Code != http.StatusBadGateway || !strings.Contains(cerr.Message, "server returned error 500") {
		t.Fatalf("cerr = %+v, want 502 server-returned-error", cerr)
	}
}

// A body shorter than its declared Content-Length makes the client's read fail
// with an unexpected EOF — the converter must surface that as a 500 instead of
// trying to parse a truncated .torrent.
func TestResolveTorrentToMagnet_TruncatedBody(t *testing.T) {
	withLoopbackClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("d4:info"))
	}))
	defer srv.Close()

	_, cerr := resolveTorrentToMagnet(srv.URL + "/x.torrent")
	if cerr == nil || cerr.Code != http.StatusInternalServerError || !strings.Contains(cerr.Message, "failed to read torrent bytes") {
		t.Fatalf("cerr = %+v, want 500 failed-to-read-bytes", cerr)
	}
}

func TestResolveTorrentToMagnet_InvalidMetainfo(t *testing.T) {
	withLoopbackClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not a torrent</html>"))
	}))
	defer srv.Close()

	_, cerr := resolveTorrentToMagnet(srv.URL + "/x.torrent")
	if cerr == nil || cerr.Code != http.StatusBadRequest || !strings.Contains(cerr.Message, "failed to read torrent metainfo") {
		t.Fatalf("cerr = %+v, want 400 failed-to-read-metainfo", cerr)
	}
}
