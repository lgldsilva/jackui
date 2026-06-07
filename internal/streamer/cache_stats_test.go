package streamer

import (
	"os"
	"path/filepath"
	"testing"
)

// Test_CacheStats_ReportsEvictionAndDisk pins the observability surface added for
// the cache health panel: Stats() reports filesystem disk usage and lifetime
// eviction counters, and an actual eviction bumps the counters + stamps a time.
func Test_CacheStats_ReportsEvictionAndDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "evictme"), make([]byte, 64*1024), 0o644); err != nil {
		t.Fatal(err)
	}

	s := leaseTestStreamer()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1 // anything on disk is over the limit

	st0, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st0.EvictedCount != 0 || st0.EvictedBytes != 0 || st0.LastEvictionAt != nil {
		t.Fatalf("fresh streamer should report no evictions, got count=%d bytes=%d last=%v",
			st0.EvictedCount, st0.EvictedBytes, st0.LastEvictionAt)
	}
	if st0.DiskTotal <= 0 || st0.DiskFree <= 0 {
		t.Errorf("expected statfs to report disk free/total > 0, got free=%d total=%d", st0.DiskFree, st0.DiskTotal)
	}

	s.enforceCacheLimit() // evicts "evictme"

	st1, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st1.EvictedCount != 1 {
		t.Errorf("EvictedCount = %d, want 1", st1.EvictedCount)
	}
	if st1.EvictedBytes <= 0 {
		t.Errorf("EvictedBytes = %d, want > 0", st1.EvictedBytes)
	}
	if st1.LastEvictionAt == nil {
		t.Error("LastEvictionAt should be set after an eviction")
	}
}
