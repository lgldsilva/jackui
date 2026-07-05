package local

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
)

// batchRouter wires LocalPlayBatch onto a fresh gin engine backed by a temp mount
// containing the given files (each created empty — probeLocalFile fails gracefully
// on a non-media file, which the resolvers handle).
func batchRouter(t *testing.T, files ...string) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mountDir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(mountDir, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Test", Path: mountDir}})
	r := gin.New()
	r.POST("/api/local/play/batch", LocalPlayBatch(b))
	return r, mountDir
}

func doBatch(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/local/play/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLocalPlayBatch_BadRequest(t *testing.T) {
	r, _ := batchRouter(t)
	// Missing mount / empty paths → 400.
	if w := doBatch(r, `{"mount":"Test","paths":[]}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty paths: status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if w := doBatch(r, `{"paths":["a.mp3"]}`); w.Code != http.StatusBadRequest {
		t.Errorf("missing mount: status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPlayBatch_TooMany(t *testing.T) {
	r, _ := batchRouter(t)
	paths := make([]string, 501)
	for i := range paths {
		paths[i] = "x.mp3"
	}
	body, _ := json.Marshal(map[string]any{"mount": "Test", "paths": paths})
	if w := doBatch(r, string(body)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalPlayBatch_Resolves(t *testing.T) {
	r, _ := batchRouter(t, "song.mp3", "clip.mp4", "clip.avi")
	// Audio + two videos (their direct-vs-HLS split depends on whether ffprobe is
	// present in the env, so we only assert a URL is produced, not the kind) + one
	// missing path (reported per-file, batch still 200).
	w := doBatch(r, `{"mount":"Test","paths":["song.mp3","clip.mp4","clip.avi","gone.mp3"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []LocalPlayBatchItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(resp.Items))
	}
	byPath := map[string]LocalPlayBatchItem{}
	for _, it := range resp.Items {
		byPath[it.Path] = it
	}
	// song.mp3 → audio always direct-plays (deterministic, no matter the probe).
	if byPath["song.mp3"].Kind != "direct" || byPath["song.mp3"].URL == "" {
		t.Errorf("song.mp3 = %+v, want direct with URL", byPath["song.mp3"])
	}
	// Videos always resolve to SOME playable URL (direct or HLS).
	for _, p := range []string{"clip.mp4", "clip.avi"} {
		if byPath[p].URL == "" || byPath[p].Error != "" {
			t.Errorf("%s = %+v, want a URL and no error", p, byPath[p])
		}
	}
	// gone.mp3 → per-file error, batch still 200.
	if byPath["gone.mp3"].Error == "" {
		t.Errorf("gone.mp3 should carry an error: %+v", byPath["gone.mp3"])
	}
}

func TestLocalPlayBatch_ForceHLSVideo(t *testing.T) {
	r, _ := batchRouter(t, "movie.mkv")
	// forceHLS=true → video resolves straight to HLS (client_forced), no probe.
	w := doBatch(r, `{"mount":"Test","paths":["movie.mkv"],"forceHLS":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []LocalPlayBatchItem `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Items) != 1 || resp.Items[0].Kind != "hls" {
		t.Errorf("forceHLS video: %+v, want kind=hls", resp.Items)
	}
}
