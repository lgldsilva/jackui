package downloader

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luizg/jackui/internal/config"
)

func newTestTransmission(serverURL string) *Transmission {
	return NewTransmission(config.DownloadClient{
		Name:     "Test Transmission",
		URL:      serverURL,
		Username: "",
		Password: "",
	})
}

func successResponse(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(transmissionResponse{Result: "success"})
}

func TestTransmission_Name_Type(t *testing.T) {
	tr := newTestTransmission("http://localhost:9091")
	if tr.Name() != "Test Transmission" {
		t.Errorf("Name() = %q, want 'Test Transmission'", tr.Name())
	}
	if tr.Type() != "transmission" {
		t.Errorf("Type() = %q, want 'transmission'", tr.Type())
	}
}

func TestTransmission_SessionID_409Retry(t *testing.T) {
	// Transmission returns 409 on first request without session ID,
	// then the client must retry with the session ID from the response header.
	requestCount := 0
	const sessionID = "test-session-abc123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		if requestCount == 1 {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}

		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			http.Error(w, "missing or wrong session ID", http.StatusForbidden)
			return
		}
		successResponse(w)
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	err := tr.AddMagnet("magnet:?xt=urn:btih:abc", "")
	if err != nil {
		t.Fatalf("expected no error after 409 retry, got: %v", err)
	}
	if requestCount != 2 {
		t.Errorf("expected 2 requests (409 + retry), got %d", requestCount)
	}
}

func TestTransmission_SessionID_ReuseOnSubsequentCalls(t *testing.T) {
	// After the first 409, the session ID is stored and reused.
	conflictCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			conflictCount++
			w.Header().Set("X-Transmission-Session-Id", "session-xyz")
			w.WriteHeader(http.StatusConflict)
			return
		}
		successResponse(w)
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	_ = tr.AddMagnet("magnet:?xt=urn:btih:1", "")
	_ = tr.AddMagnet("magnet:?xt=urn:btih:2", "")

	if conflictCount != 1 {
		t.Errorf("409 conflict happened %d times, want 1 (session must be reused)", conflictCount)
	}
}

func TestTransmission_AddMagnet_SendsCorrectPayload(t *testing.T) {
	var capturedReq transmissionRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedReq)
		successResponse(w)
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	err := tr.AddMagnet("magnet:?xt=urn:btih:def456", "/media/downloads")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.Method != "torrent-add" {
		t.Errorf("method = %q, want 'torrent-add'", capturedReq.Method)
	}
	if capturedReq.Arguments["filename"] != "magnet:?xt=urn:btih:def456" {
		t.Errorf("filename = %v, want magnet URI", capturedReq.Arguments["filename"])
	}
	if capturedReq.Arguments["download-dir"] != "/media/downloads" {
		t.Errorf("download-dir = %v, want '/media/downloads'", capturedReq.Arguments["download-dir"])
	}
}

func TestTransmission_AddMagnet_OmitsDownloadDirWhenEmpty(t *testing.T) {
	var capturedReq transmissionRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedReq)
		successResponse(w)
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	_ = tr.AddMagnet("magnet:?xt=urn:btih:abc", "")

	if _, ok := capturedReq.Arguments["download-dir"]; ok {
		t.Error("download-dir should not be sent when save path is empty")
	}
}

func TestTransmission_AddTorrentURL(t *testing.T) {
	var capturedReq transmissionRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedReq)
		successResponse(w)
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	err := tr.AddTorrentURL("http://tracker.example.com/file.torrent", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.Method != "torrent-add" {
		t.Errorf("method = %q, want 'torrent-add'", capturedReq.Method)
	}
	if capturedReq.Arguments["filename"] != "http://tracker.example.com/file.torrent" {
		t.Errorf("filename = %v", capturedReq.Arguments["filename"])
	}
}

func TestTransmission_ErrorResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(transmissionResponse{Result: "duplicate torrent"})
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	err := tr.AddMagnet("magnet:?xt=urn:btih:abc", "")
	if err == nil {
		t.Error("expected error when result != 'success', got nil")
	}
}

func TestTransmission_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("service down"))
	}))
	defer srv.Close()

	tr := newTestTransmission(srv.URL)
	err := tr.AddMagnet("magnet:?xt=urn:btih:abc", "")
	if err == nil {
		t.Error("expected error on 503, got nil")
	}
}

func TestTransmission_BasicAuth_Sent(t *testing.T) {
	var capturedUser, capturedPass string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUser, capturedPass, _ = r.BasicAuth()
		successResponse(w)
	}))
	defer srv.Close()

	tr := NewTransmission(config.DownloadClient{
		Name:     "auth-test",
		URL:      srv.URL,
		Username: "myuser",
		Password: "mypass",
	})
	_ = tr.AddMagnet("magnet:?xt=urn:btih:abc", "")

	if capturedUser != "myuser" || capturedPass != "mypass" {
		t.Errorf("basic auth: got user=%q pass=%q, want user=myuser pass=mypass", capturedUser, capturedPass)
	}
}
