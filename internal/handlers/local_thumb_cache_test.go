package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNegativeMarkerPath(t *testing.T) {
	if got := negativeMarkerPath("/x/abc.jpg"); got != "/x/abc.jpg.empty" {
		t.Fatalf("negativeMarkerPath = %q, want /x/abc.jpg.empty", got)
	}
}

func TestNegativeThumbFresh(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "deadbeef.jpg")

	// No marker yet → not fresh (should attempt a capture).
	if negativeThumbFresh(cachePath) {
		t.Fatal("expected no marker to read as not-fresh")
	}

	// A just-written marker → fresh (suppress retries).
	if err := os.WriteFile(negativeMarkerPath(cachePath), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !negativeThumbFresh(cachePath) {
		t.Fatal("expected a recent marker to read as fresh")
	}

	// An expired marker (mtime older than the TTL) → not fresh (retry allowed).
	old := time.Now().Add(-negativeThumbTTL - time.Hour)
	if err := os.Chtimes(negativeMarkerPath(cachePath), old, old); err != nil {
		t.Fatal(err)
	}
	if negativeThumbFresh(cachePath) {
		t.Fatal("expected an expired marker to read as not-fresh")
	}
}

func TestSetLocalThumbCacheDir(t *testing.T) {
	orig := localThumbCacheDir
	t.Cleanup(func() { localThumbCacheDir = orig })

	SetLocalThumbCacheDir("") // empty is a no-op
	if localThumbCacheDir != orig {
		t.Fatal("empty dir must not change the cache dir")
	}

	SetLocalThumbCacheDir("/data/streams/.thumbs/local")
	if localThumbCacheDir != "/data/streams/.thumbs/local" {
		t.Fatalf("cache dir = %q, want the value just set", localThumbCacheDir)
	}
}
