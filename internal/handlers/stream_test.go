package handlers

import (
	"net/http"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestNormalizeKind_Stream(t *testing.T) {
	if got := normalizeKind("audio"); got != "audio" {
		t.Errorf("audio = %q", got)
	}
	if got := normalizeKind("video"); got != "video" {
		t.Errorf("video = %q", got)
	}
	if got := normalizeKind("bogus"); got != "" {
		t.Errorf("bogus = %q, want empty", got)
	}
	if got := normalizeKind(""); got != "" {
		t.Errorf("empty = %q, want empty", got)
	}
}

func TestParseHash(t *testing.T) {
	h, err := parseHash("0123456789012345678901234567890123456789")
	if err != nil {
		t.Fatalf("valid hash: %v", err)
	}
	if h == (metainfo.Hash{}) {
		t.Error("expected non-zero hash")
	}

	_, err = parseHash("not-a-hash")
	if err == nil {
		t.Fatal("invalid hash must error")
	}
}

func TestStreamAdd_InvalidMagnet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	w := invokeCoverageHandler(t, StreamAdd(s, nil), http.MethodPost, "/api/stream/add", `{"magnet":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestStreamAdd_AddFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	// NewForTesting has no torrent client, so Add must fail.
	w := invokeCoverageHandler(t, StreamAdd(s, nil), http.MethodPost, "/api/stream/add", `{"magnet":"magnet:?xt=urn:btih:deadbeef"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestStreamAddTorrentFile_MissingFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	w := invokeCoverageHandler(t, StreamAddTorrentFile(s), http.MethodPost, "/api/stream/add-file", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestStreamInfo_InvalidHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	params := gin.Params{{Key: "hash", Value: "not-a-hash"}}
	w := invokeCoverageHandlerWithParams(t, StreamInfo(s), http.MethodGet, "/api/stream/info/not-a-hash", "", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestStreamInfo_NotFound_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	params := gin.Params{{Key: "hash", Value: "0123456789012345678901234567890123456789"}}
	w := invokeCoverageHandlerWithParams(t, StreamInfo(s), http.MethodGet, "/api/stream/info/0123456789012345678901234567890123456789", "", params)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
