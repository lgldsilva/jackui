package local

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	lb "github.com/lgldsilva/jackui/internal/local"
)

func newRenameRouter(t *testing.T) (*gin.Engine, *lb.Browser, *downloads.Store, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	meus := t.TempDir()
	dls, err := downloads.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dls.Close() })
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: meus}})
	router := gin.New()
	router.POST("/api/local/rename", LocalRename(b, dls, nil))
	router.POST("/api/local/lock", LocalSetFolderLock(b, nil))
	router.GET("/api/local/list", LocalList(b, nil))
	return router, b, dls, meus
}

// TestLocalRenameFileRelinksTorrent renames a file and asserts the linked
// download row's file_path is rewritten to the new location.
func TestLocalRenameFileRelinksTorrent(t *testing.T) {
	router, _, dls, meus := newRenameRouter(t)
	old := filepath.Join(meus, "raw.name.mkv")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := dls.Create(downloads.Download{
		UserID: 1, InfoHash: "aabbccddeeff00112233445566778899aabbccdd",
		FileIndex: 0, FilePath: old, Name: "raw.name.mkv",
		Magnet: "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd",
	}); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, router, "/api/local/rename", gin.H{"mount": "Meus downloads", "path": "raw.name.mkv", "newName": "Filme Bonito.mkv"})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	newPath := filepath.Join(meus, "Filme Bonito.mkv")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old file still present")
	}
	rows, _ := dls.List(1)
	if len(rows) != 1 || rows[0].FilePath != newPath {
		t.Errorf("file_path not relinked: %+v", rows)
	}
}

func TestLocalRenameRejectsBadNames(t *testing.T) {
	router, _, _, meus := newRenameRouter(t)
	if err := os.WriteFile(filepath.Join(meus, "a.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape", "sub/dir.mkv", "..", "."} {
		w := postJSON(t, router, "/api/local/rename", gin.H{"mount": "Meus downloads", "path": "a.mkv", "newName": name})
		if w.Code != http.StatusBadRequest {
			t.Errorf("name %q: status=%d body=%s, want 400", name, w.Code, w.Body.String())
		}
	}
}

func TestLocalRenameRefusesCollision(t *testing.T) {
	router, _, _, meus := newRenameRouter(t)
	if err := os.WriteFile(filepath.Join(meus, "a.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meus, "b.mkv"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := postJSON(t, router, "/api/local/rename", gin.H{"mount": "Meus downloads", "path": "a.mkv", "newName": "b.mkv"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", w.Code)
	}
}

func TestLocalRenameMissingFields(t *testing.T) {
	router, _, _, _ := newRenameRouter(t)
	cases := []gin.H{
		{"mount": "", "path": "a.mkv", "newName": "b.mkv"},
		{"mount": "Meus downloads", "path": "", "newName": "b.mkv"},
		{"mount": "Meus downloads", "path": "a.mkv", "newName": ""},
	}
	for _, body := range cases {
		w := postJSON(t, router, "/api/local/rename", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body=%v status=%d, want 400", body, w.Code)
		}
	}
}

func TestLocalRenameBadJSON(t *testing.T) {
	router, _, _, _ := newRenameRouter(t)
	// NewName is a string; sending a number makes ShouldBindJSON fail.
	w := postJSON(t, router, "/api/local/rename", gin.H{"mount": "Meus downloads", "path": "a.mkv", "newName": 123})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestLocalRenameSourceNotFound(t *testing.T) {
	router, _, _, _ := newRenameRouter(t)
	w := postJSON(t, router, "/api/local/rename", gin.H{"mount": "Meus downloads", "path": "ghost.mkv", "newName": "x.mkv"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", w.Code, w.Body.String())
	}
}

func TestLocalRenameNoopSameName(t *testing.T) {
	router, _, _, meus := newRenameRouter(t)
	if err := os.WriteFile(filepath.Join(meus, "same.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := postJSON(t, router, "/api/local/rename", gin.H{"mount": "Meus downloads", "path": "same.mkv", "newName": "same.mkv"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestLocalSetFolderLockErrors(t *testing.T) {
	router, _, _, _ := newRenameRouter(t)

	// Malformed body: Locked is a bool, a string fails the bind.
	if w := postJSON(t, router, "/api/local/lock", gin.H{"mount": "Meus downloads", "path": "x", "locked": "nope"}); w.Code != http.StatusBadRequest {
		t.Errorf("bad json: status=%d, want 400", w.Code)
	}
	// Missing required fields.
	if w := postJSON(t, router, "/api/local/lock", gin.H{"mount": "", "path": "", "locked": true}); w.Code != http.StatusBadRequest {
		t.Errorf("missing fields: status=%d, want 400", w.Code)
	}
	// Nonexistent directory → SetFolderLock stat fails with IsNotExist → 404.
	if w := postJSON(t, router, "/api/local/lock", gin.H{"mount": "Meus downloads", "path": "ghost", "locked": true}); w.Code != http.StatusNotFound {
		t.Errorf("ghost dir: status=%d body=%s, want 404", w.Code, w.Body.String())
	}
	// Locking the mount root is refused (non-IsNotExist error → 400).
	if w := postJSON(t, router, "/api/local/lock", gin.H{"mount": "Meus downloads", "path": ".", "locked": true}); w.Code != http.StatusBadRequest {
		t.Errorf("root lock: status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

// TestLocalFolderLockRoundtrip locks a folder, sees Locked=true in the listing,
// confirms the empty-dir sweep keeps it, then unlocks.
func TestLocalFolderLockRoundtrip(t *testing.T) {
	router, b, _, meus := newRenameRouter(t)
	if err := os.Mkdir(filepath.Join(meus, "keepme"), 0o755); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, router, "/api/local/lock", gin.H{"mount": "Meus downloads", "path": "keepme", "locked": true})
	if w.Code != http.StatusOK {
		t.Fatalf("lock status=%d body=%s", w.Code, w.Body.String())
	}

	entries, err := b.List("Meus downloads", "")
	if err != nil {
		t.Fatal(err)
	}
	var found *lb.Entry
	for i := range entries {
		if entries[i].Name == "keepme" {
			found = &entries[i]
		}
		if entries[i].Name == ".keep" {
			t.Error(".keep marker leaked into the listing")
		}
	}
	if found == nil || !found.Locked {
		t.Fatalf("folder not reported as locked: %+v", entries)
	}

	// Empty-dir sweep must keep the locked folder.
	if _, err := b.RemoveEmptyDirs("Meus downloads", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(meus, "keepme")); err != nil {
		t.Errorf("locked folder was removed by clean-empty: %v", err)
	}

	// Unlock removes the marker and the folder becomes sweepable.
	w = postJSON(t, router, "/api/local/lock", gin.H{"mount": "Meus downloads", "path": "keepme", "locked": false})
	if w.Code != http.StatusOK {
		t.Fatalf("unlock status=%d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(meus, "keepme", ".keep")); !os.IsNotExist(err) {
		t.Error(".keep marker not removed on unlock")
	}
	if n, _ := b.RemoveEmptyDirs("Meus downloads", ""); n != 1 {
		t.Errorf("unlocked empty folder not cleaned (removed=%d)", n)
	}
}
