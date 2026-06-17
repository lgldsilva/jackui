package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// countTree totals files + bytes for a file and for a directory tree.
func TestCountTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.bin"), []byte("678"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, b := countTree(filepath.Join(dir, "a.bin")); f != 1 || b != 5 {
		t.Fatalf("file countTree = %d/%d, want 1/5", f, b)
	}
	if f, b := countTree(dir); f != 2 || b != 8 {
		t.Fatalf("dir countTree = %d/%d, want 2/8", f, b)
	}
}

// LocalMoveEntry with a real tracker: the async move lands the file and the
// Transfers dock shows the job finishing at 100% (1/1 files).
func TestLocalMoveEntry_PopulatesTracker(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), bytes.Repeat([]byte("x"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Src", Path: srcDir},
		{Name: "Dst", Path: dstDir},
	})
	tr := transfer.New()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move",
		bytes.NewReader([]byte(`{"srcMount":"Src","srcPath":"file.txt","dstMount":"Dst","dstPath":""}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	setAuth(c, 1, true)

	LocalMoveEntry(b, nil, nil, tr)(c)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}
	waitForLocalFile(t, filepath.Join(dstDir, "file.txt"), 2*time.Second)

	// The job must be present and reach done with progress 1.0.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list := tr.List()
		if len(list) == 1 && list[0].Status == transfer.StatusDone {
			if list[0].Kind != "local-move" || list[0].Progress != 1.0 {
				t.Fatalf("job = %+v, want local-move at 100%%", list[0])
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("tracker job did not reach done; list=%+v", tr.List())
}
