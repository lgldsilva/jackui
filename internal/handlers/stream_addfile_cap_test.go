package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// addFileMultipart POSTs a multipart add-file upload with the given file bytes.
func addFileMultipart(t *testing.T, r *gin.Engine, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "test.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/stream/add-file", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// streamAddFileCap mirrors the handler's upload bound (16 MiB, same as the
// Transmission-RPC torrent-add cap). Declared here (not imported from the
// handler) so the test pins the VALUE, not just a symbol.
const streamAddFileCap = 16 << 20

// POST /api/stream/add-file is exempt from the global 2MiB JSON cap (real
// .torrent files exceed it) but must still enforce its OWN bound — otherwise a
// guest-reachable upload fills disk/RAM with an unbounded body.
func TestStreamAddTorrentFile_OversizeRejected413(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/stream/add-file", StreamAddTorrentFile(streamer.NewForTesting()))

	// Inert padding ('x' is invalid bencode, fails parsing immediately — no
	// deep recursion) just past the 16MiB cap.
	big := bytes.Repeat([]byte("x"), streamAddFileCap+1)
	w := addFileMultipart(t, r, big)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized add-file status = %d, want 413; body %s", w.Code, w.Body.String())
	}
}

// A legitimate-size upload passes the size gate: an under-cap body must reach
// the handler's own parsing (here it fails as an invalid torrent — 400 — not
// as 413), proving the cap only rejects genuinely oversized requests.
func TestStreamAddTorrentFile_UnderCapPassesSizeGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/stream/add-file", StreamAddTorrentFile(streamer.NewForTesting()))

	w := addFileMultipart(t, r, []byte("not-a-torrent"))
	if w.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("under-cap upload must pass the size gate, got 413; body %s", w.Body.String())
	}
	if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte("invalid torrent file")) {
		t.Fatalf("under-cap upload should reach metainfo parsing (400 invalid torrent file), got %d; body %s", w.Code, w.Body.String())
	}
}
