package streamer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── F4: Stats()/enforceCacheLimit must skip dot-prefixed bookkeeping ────────

// TestEnforceCacheLimit_SkipsDotPrefixedBookkeeping: the DataDir walk counted
// and listed dot-prefixed dirs (.metainfo, .piece-completion-dl), so LRU
// eviction could delete live bookkeeping (the open Bolt piece DB, the
// serialized .torrent cache). They must be invisible to Stats() and eviction,
// exactly like ClearAll already treats them.
func TestEnforceCacheLimit_SkipsDotPrefixedBookkeeping(t *testing.T) {
	dir := t.TempDir()
	meta := filepath.Join(dir, ".metainfo")
	pc := filepath.Join(dir, ".piece-completion-dl")
	for _, d := range []string{meta, pc} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	big := make([]byte, 64*1024)
	old := time.Now().Add(-time.Hour) // older than the evictable entry: old LRU picked these FIRST
	if err := os.WriteFile(filepath.Join(meta, "h.torrent"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(meta, "h.torrent"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pc, "bolt.db"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(pc, "bolt.db"), old, old); err != nil {
		t.Fatal(err)
	}
	// The one evictable user-facing entry, newer.
	if err := os.WriteFile(filepath.Join(dir, "movie-entry"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	s := leaseTestStreamer()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1 // anything on disk is over the cap

	st, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.TotalSize != int64(len(big)) {
		t.Errorf("Stats TotalSize = %d, want %d — internal bookkeeping must not count toward the cache total", st.TotalSize, len(big))
	}
	for _, e := range st.Entries {
		if strings.HasPrefix(e.Path, ".") {
			t.Errorf("Stats listed internal bookkeeping entry %q as an eviction-visible entry", e.Path)
		}
	}

	s.enforceCacheLimit()

	if _, err := os.Stat(meta); err != nil {
		t.Errorf(".metainfo must survive LRU eviction (live metainfo cache): %v", err)
	}
	if _, err := os.Stat(pc); err != nil {
		t.Errorf(".piece-completion-dl must survive LRU eviction (open Bolt DB): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "movie-entry")); !os.IsNotExist(err) {
		t.Errorf("oversized normal entry should have been evicted, stat err = %v", err)
	}
}
