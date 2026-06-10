package localcache

import (
	"testing"
)

// Root must expose the directory the cache was created on — handlers build
// derived paths (e.g. the extracted-subtitle cache) from it.
func TestRoot(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if got := c.Root(); got != dir {
		t.Errorf("Root() = %q, want %q", got, dir)
	}
}
