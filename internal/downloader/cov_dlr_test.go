package downloader

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/luizg/jackui/internal/config"
)

// dlrUnreachableQBit builds a qBittorrent client pointed at an address that is
// guaranteed to refuse connections, so the very first login PostForm errors.
func dlrUnreachableQBit() *QBittorrent {
	return NewQBittorrent(config.DownloadClient{
		Name: "dlr-dead",
		// Reserved TEST-NET-1 port 1 — connection is refused/unroutable.
		URL:      "http://192.0.2.1:1",
		Username: "u",
		Password: "p",
	})
}

// dlrUnreachableTransmission mirrors the helper above for Transmission.
func dlrUnreachableTransmission() *Transmission {
	return NewTransmission(config.DownloadClient{
		Name:     "dlr-dead",
		URL:      "http://192.0.2.1:1",
		Username: "",
		Password: "",
	})
}

// --- qBittorrent: login network failure (covers login PostForm err branch) ---

func Test_dlrQBitLoginRequestFails(t *testing.T) {
	q := dlrUnreachableQBit()
	if err := q.AddMagnet("magnet:?xt=urn:btih:dlr", ""); err == nil {
		t.Fatal("expected error when login request cannot reach server")
	}
}

func Test_dlrQBitAddTorrentURLLoginFails(t *testing.T) {
	q := dlrUnreachableQBit()
	if err := q.AddTorrentURL("http://example.invalid/x.torrent", ""); err == nil {
		t.Fatal("expected error when login request cannot reach server")
	}
}

// --- qBittorrent: login HTTP non-200 (covers login status != 200 branch) ---

func Test_dlrQBitLoginNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	q := NewQBittorrent(config.DownloadClient{Name: "dlr", URL: srv.URL, Username: "u", Password: "p"})
	if err := q.AddMagnet("magnet:?xt=urn:btih:dlr", ""); err == nil {
		t.Fatal("expected error when login returns non-200 status")
	}
}

// --- qBittorrent: AddTorrentURL server error on add endpoint ---

func Test_dlrQBitAddTorrentURLServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("boom"))
		}
	}))
	defer srv.Close()

	q := NewQBittorrent(config.DownloadClient{Name: "dlr", URL: srv.URL, Username: "u", Password: "p"})
	if err := q.AddTorrentURL("http://example.invalid/x.torrent", "/dlr"); err == nil {
		t.Fatal("expected error on 500 from add endpoint")
	}
}

// --- qBittorrent: re-login retry path. Already logged in, but the add POST
// fails (server closed) — exercises the q.loggedIn=false + re-login branch,
// which then also fails because the server is gone. ---

func Test_dlrQBitAddMagnetReloginFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))

	q := NewQBittorrent(config.DownloadClient{Name: "dlr", URL: srv.URL, Username: "u", Password: "p"})
	// First add succeeds and marks the session logged in.
	if err := q.AddMagnet("magnet:?xt=urn:btih:dlr1", ""); err != nil {
		t.Fatalf("first add should succeed: %v", err)
	}
	// Kill the server: the next add POST errors, re-login is attempted and also
	// fails, so AddMagnet returns the wrapped re-login failure.
	srv.Close()
	if err := q.AddMagnet("magnet:?xt=urn:btih:dlr2", ""); err == nil {
		t.Fatal("expected error when add POST and re-login both fail")
	}
}

func Test_dlrQBitAddTorrentURLReloginFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))

	q := NewQBittorrent(config.DownloadClient{Name: "dlr", URL: srv.URL, Username: "u", Password: "p"})
	if err := q.AddTorrentURL("http://example.invalid/a.torrent", ""); err != nil {
		t.Fatalf("first add should succeed: %v", err)
	}
	srv.Close()
	if err := q.AddTorrentURL("http://example.invalid/b.torrent", ""); err == nil {
		t.Fatal("expected error when add POST and re-login both fail")
	}
}

// --- Transmission: request failure (covers do/doN client.Do err branch) ---

func Test_dlrTransmissionRequestFails(t *testing.T) {
	tr := dlrUnreachableTransmission()
	if err := tr.AddMagnet("magnet:?xt=urn:btih:dlr", ""); err == nil {
		t.Fatal("expected error when RPC request cannot reach server")
	}
}

func Test_dlrTransmissionAddTorrentURLRequestFails(t *testing.T) {
	tr := dlrUnreachableTransmission()
	if err := tr.AddTorrentURL("http://example.invalid/x.torrent", "/dlr"); err == nil {
		t.Fatal("expected error when RPC request cannot reach server")
	}
}

// --- Transmission: 409 with no session id header (covers the empty-session
// id guard inside doN). ---

func Test_dlrTransmission409NoSessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always 409, never supply the session id header.
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	if err := tr.AddMagnet("magnet:?xt=urn:btih:dlr", ""); err == nil {
		t.Fatal("expected error when 409 carries no session id")
	}
}

// --- Transmission: 409 storm that keeps rotating the session id, exhausting
// the bounded retry (covers retriesLeft <= 0 branch). ---

func Test_dlrTransmission409RetriesExhausted(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		// Every response rotates the session id and returns 409, so the retry
		// never converges.
		w.Header().Set("X-Transmission-Session-Id", "dlr-rotating-"+strconv.Itoa(n))
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	if err := tr.AddMagnet("magnet:?xt=urn:btih:dlr", ""); err == nil {
		t.Fatal("expected error when 409 retries are exhausted")
	}
}

// --- Transmission: malformed JSON body (covers decode-error branch). ---

func Test_dlrTransmissionDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{ not valid json"))
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	if err := tr.AddMagnet("magnet:?xt=urn:btih:dlr", ""); err == nil {
		t.Fatal("expected error when response body is not valid JSON")
	}
}

// --- Transmission: AddTorrentURL error result (covers AddTorrentURL err wrap). ---

func Test_dlrTransmissionAddTorrentURLErrorResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json := `{"result":"duplicate torrent"}`
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(json))
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	if err := tr.AddTorrentURL("http://example.invalid/x.torrent", ""); err == nil {
		t.Fatal("expected error when transmission result is not success")
	}
}
