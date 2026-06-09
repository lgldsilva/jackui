package downloader

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

func newTestQBit(t *testing.T, serverURL string) *QBittorrent {
	t.Helper()
	return NewQBittorrent(config.DownloadClient{
		Name:     "Test qBit",
		URL:      serverURL,
		Username: "admin",
		Password: "adminadmin",
	})
}

func TestQBittorrent_Name_Type(t *testing.T) {
	q := newTestQBit(t, "http://localhost:8080")
	if q.Name() != "Test qBit" {
		t.Errorf("Name() = %q, want 'Test qBit'", q.Name())
	}
	if q.Type() != "qbittorrent" {
		t.Errorf("Type() = %q, want 'qbittorrent'", q.Type())
	}
}

func TestQBittorrent_AddMagnet_Success(t *testing.T) {
	var capturedMagnet, capturedSavePath string
	loginCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			loginCalled = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			capturedMagnet = vals.Get("urls")
			capturedSavePath = vals.Get("savepath")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	q := newTestQBit(t, srv.URL)
	err := q.AddMagnet("magnet:?xt=urn:btih:abc", "/downloads")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if !loginCalled {
		t.Error("expected login to be called on first request")
	}
	if capturedMagnet != "magnet:?xt=urn:btih:abc" {
		t.Errorf("urls param = %q, want magnet URI", capturedMagnet)
	}
	if capturedSavePath != "/downloads" {
		t.Errorf("savepath param = %q, want '/downloads'", capturedSavePath)
	}
}

func TestQBittorrent_AddMagnet_OmitsSavePathWhenEmpty(t *testing.T) {
	savePathPresent := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			if _, ok := vals["savepath"]; ok {
				savePathPresent = true
			}
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	q := newTestQBit(t, srv.URL)
	_ = q.AddMagnet("magnet:?xt=urn:btih:abc", "")

	if savePathPresent {
		t.Error("savepath should not be sent when save path is empty")
	}
}

func TestQBittorrent_AddMagnet_InvalidCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Fails."))
		}
	}))
	defer srv.Close()

	q := newTestQBit(t, srv.URL)
	err := q.AddMagnet("magnet:?xt=urn:btih:abc", "")
	if err == nil {
		t.Error("expected error for failed login, got nil")
	}
}

func TestQBittorrent_NoReloginWhenAlreadyLoggedIn(t *testing.T) {
	loginCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			loginCount++
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	q := newTestQBit(t, srv.URL)
	_ = q.AddMagnet("magnet:?xt=urn:btih:1", "")
	_ = q.AddMagnet("magnet:?xt=urn:btih:2", "")

	if loginCount != 1 {
		t.Errorf("login called %d times, want exactly 1 (session reuse)", loginCount)
	}
}

func TestQBittorrent_AddTorrentURL(t *testing.T) {
	var capturedURL string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			capturedURL = vals.Get("urls")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	q := newTestQBit(t, srv.URL)
	err := q.AddTorrentURL("http://tracker.example.com/file.torrent", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedURL != "http://tracker.example.com/file.torrent" {
		t.Errorf("urls param = %q, want torrent URL", capturedURL)
	}
}

func TestQBittorrent_AddMagnet_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		}
	}))
	defer srv.Close()

	q := newTestQBit(t, srv.URL)
	err := q.AddMagnet("magnet:?xt=urn:btih:abc", "")
	if err == nil {
		t.Error("expected error on 500 from add endpoint, got nil")
	}
}
