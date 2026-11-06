package streamer

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// countMetainfoLoads swaps the package loader for a counting wrapper for the
// duration of the test.
func countMetainfoLoads(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	orig := loadMetainfoFile
	loadMetainfoFile = func(p string) (*metainfo.MetaInfo, error) {
		n.Add(1)
		return orig(p)
	}
	t.Cleanup(func() { loadMetainfoFile = orig })
	return &n
}

func newMetainfoNamesStreamer(t *testing.T) (*Streamer, string) {
	t.Helper()
	s := NewForTesting()
	dir := filepath.Join(t.TempDir(), ".metainfo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s.metainfoDir = dir
	return s, dir
}

// Regression: metrics.StartWorker calls Stats() every 10s and Stats() used to
// re-parse (bencode + SHA-1) every .torrent in metainfoDir on each call. With
// ~450 files that pinned up to 1.7 cores every tick in production. Unchanged
// files must be parsed exactly once.
func TestAugmentNameToHash_ParsesUnchangedFilesOnce(t *testing.T) {
	s, dir := newMetainfoNamesStreamer(t)
	want := map[string]string{}
	for _, name := range []string{"A.Movie", "B.Show", "C.Doc"} {
		mi := strmBuildMetainfo(t, dir, name)
		want[name] = mi.HashInfoBytes().HexString()
	}
	loads := countMetainfoLoads(t)

	for i := 0; i < 3; i++ {
		got := map[string]string{}
		s.augmentNameToHashFromMetainfo(got)
		for name, h := range want {
			if got[name] != h {
				t.Fatalf("call %d: %s → %q, want %q", i, name, got[name], h)
			}
		}
	}
	if n := loads.Load(); n != 3 {
		t.Fatalf("metainfo loads = %d across 3 calls, want 3 (one per file)", n)
	}
}

func TestAugmentNameToHash_ReparsesChangedAndForgetsRemoved(t *testing.T) {
	s, dir := newMetainfoNamesStreamer(t)
	a := strmBuildMetainfo(t, dir, "A.Movie")
	b := strmBuildMetainfo(t, dir, "B.Show")
	loads := countMetainfoLoads(t)
	s.augmentNameToHashFromMetainfo(map[string]string{})

	aPath := filepath.Join(dir, a.HashInfoBytes().HexString()+".torrent")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(aPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, b.HashInfoBytes().HexString()+".torrent")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got := map[string]string{}
	s.augmentNameToHashFromMetainfo(got)
	if n := loads.Load(); n != 3 {
		t.Fatalf("loads = %d, want 3 (2 initial + 1 for the touched file)", n)
	}
	if _, ok := got["B.Show"]; ok {
		t.Fatal("removed .torrent still mapped")
	}
	if got["A.Movie"] != a.HashInfoBytes().HexString() {
		t.Fatalf("A.Movie → %q", got["A.Movie"])
	}
	if len(s.miNames.entries) != 1 {
		t.Fatalf("cache holds %d entries, want 1 after removal", len(s.miNames.entries))
	}
}

func TestAugmentNameToHash_InvalidFileCachedAndActiveWins(t *testing.T) {
	s, dir := newMetainfoNamesStreamer(t)
	if err := os.WriteFile(filepath.Join(dir, "broken.torrent"), []byte("not bencode"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Loads fine but the info dict is not a dict → UnmarshalInfo fails.
	if err := os.WriteFile(filepath.Join(dir, "badinfo.torrent"), []byte("d4:info3:abce"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub.torrent"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	strmBuildMetainfo(t, dir, "A.Movie")
	loads := countMetainfoLoads(t)

	for i := 0; i < 2; i++ {
		got := map[string]string{"A.Movie": "active-hash"}
		s.augmentNameToHashFromMetainfo(got)
		if got["A.Movie"] != "active-hash" {
			t.Fatalf("active mapping overwritten: %q", got["A.Movie"])
		}
		if len(got) != 1 {
			t.Fatalf("unexpected mappings: %v", got)
		}
	}
	if n := loads.Load(); n != 3 {
		t.Fatalf("loads = %d, want 3 (broken + badinfo + valid, each once)", n)
	}
}

func TestAugmentNameToHash_NoDirOrMissingDir(t *testing.T) {
	s := NewForTesting()
	got := map[string]string{}
	s.augmentNameToHashFromMetainfo(got) // metainfoDir == ""
	s.metainfoDir = filepath.Join(t.TempDir(), "missing")
	s.augmentNameToHashFromMetainfo(got)
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
