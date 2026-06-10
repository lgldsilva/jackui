package downloads

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestMoveFileWithFallback(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "sub", "b.txt")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := moveFileWithFallback(src, dst); err != nil {
		t.Fatalf("move: %v", err)
	}
	if fileExists(src) || !fileExists(dst) {
		t.Error("file should have moved src→dst")
	}
	if err := moveFileWithFallback(filepath.Join(dir, "missing"), dst); err == nil {
		t.Error("expected error moving a non-existent source")
	}
}

// aiRenameCompleted re-organizes a finished download Plex-style. With a non-nil
// AI client that has no providers, the rename chain falls back to the regex
// parser (no network) — enough to verify the file gets moved and the row's path
// is updated.
func TestAIRenameCompleted(t *testing.T) {
	// A provider that always 500s: the AI extraction fails and the rename chain
	// falls back to the regex parser — which still yields a Plex-style path, so
	// aiRenameCompleted moves the file regardless of the AI's outcome.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	aiClient := ai.New(config.AIConfig{
		Enabled:   true,
		Providers: map[string]config.AIProvider{"test": {BaseURL: srv.URL}},
		Chain:     []config.AIChainSlot{{Provider: "test", Model: "m"}},
	})
	if aiClient == nil {
		t.Fatal("ai.New should return a non-nil client with a resolvable provider")
	}
	store := newTestStore(t)
	downloadDir := t.TempDir()
	w := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    streamer.NewForTesting(),
		DataDir:     t.TempDir(),
		DownloadDir: downloadDir,
		AIClient:    aiClient,
	})
	d, err := store.Create(Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m", Name: "Inception"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate the state right after moveCompletedFile: file in a per-torrent dir.
	cur := filepath.Join(downloadDir, "Inception", "Inception.2010.1080p.BluRay.x264.mkv")
	if err := os.MkdirAll(filepath.Dir(cur), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cur, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFilePath(1, d.ID, cur); err != nil {
		t.Fatal(err)
	}

	w.aiRenameCompleted(*d, cur)

	if fileExists(cur) {
		t.Error("source should have been moved out of the per-torrent folder")
	}
	got, _ := store.Get(1, d.ID)
	if got.FilePath == cur || got.FilePath == "" {
		t.Errorf("FilePath should point to the new organized path, got %q", got.FilePath)
	}
	if !fileExists(got.FilePath) {
		t.Errorf("renamed file should exist at %q", got.FilePath)
	}
}
