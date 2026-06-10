package streamer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// enforceCacheLimit calls Stats(), which snapshots the active set under the lock
// and then RELEASES it to walk the filesystem. A play that starts during that
// walk loads its entry into s.active *after* the snapshot — so the stale snapshot
// would still mark it inactive and the eviction loop would delete its file out
// from under an in-flight HLS transcode ("torrent closed" → segment 404).
//
// The fix re-checks evictionBlocked under the lock right before RemoveAll. These
// tests pin that guard: it must report an active torrent (and its ".part"
// variant) as blocked, which is what makes the concurrent gap safe.

func Test_evictionBlocked_ActiveTorrentByName(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	spec := str3TorrentSpec(t) // on-disk name: "str3-sample.bin"
	tor, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	s.mu.Lock()
	s.active[tor.InfoHash()] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()

	if !s.evictionBlocked("str3-sample.bin") {
		t.Error("active torrent's on-disk entry must be blocked from eviction")
	}
	// anacrolix writes single-file torrents as "<name>.part" while in flight; the
	// .part entry on disk must still map back to the active torrent's name.
	if !s.evictionBlocked("str3-sample.bin.part") {
		t.Error(".part variant of an active torrent must be blocked")
	}
	if s.evictionBlocked("unrelated.bin") {
		t.Error("unrelated entry must not be blocked by an active torrent")
	}
}

func Test_evictionBlocked_DownloadProtected(t *testing.T) {
	s := leaseTestStreamer()
	s.RegisterDownload("movie.mkv")

	if !s.evictionBlocked("movie.mkv") {
		t.Error("registered download must be blocked")
	}
	if !s.evictionBlocked("movie.mkv.part") {
		t.Error(".part of a registered download must be blocked")
	}
	if s.evictionBlocked("other.mkv") {
		t.Error("unregistered name must not be blocked")
	}
}

// Test_evictCandidates_SkipsBlocked deterministically covers the TOCTOU re-check
// branch: a candidate that looked evictable at snapshot time but is blocked at
// deletion time (here: a download registered after the snapshot) must be skipped,
// its file left intact, while an unblocked one over the limit is removed.
func Test_evictCandidates_SkipsBlocked(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, 64*1024), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep-me") // becomes blocked below
	write("evict-me")

	s := leaseTestStreamer()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1 // anything on disk is over the limit
	s.RegisterDownload("keep-me")

	candidates := []CacheEntry{
		{Path: "keep-me", Size: 64 * 1024},
		{Path: "evict-me", Size: 64 * 1024},
	}
	s.evictCandidates(candidates, 128*1024)

	if _, err := os.Stat(filepath.Join(dir, "keep-me")); os.IsNotExist(err) {
		t.Error("blocked candidate was evicted; the re-check should have skipped it")
	}
	if _, err := os.Stat(filepath.Join(dir, "evict-me")); !os.IsNotExist(err) {
		t.Error("unblocked over-limit candidate should have been evicted")
	}
}

// Test_evictCandidates_StopsAtLimit covers the early break: once enough has been
// freed to drop to/below MaxCacheSize, remaining candidates are left untouched.
func Test_evictCandidates_StopsAtLimit(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, 64*1024), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("old")   // evicted first
	write("newer") // must survive once we're back under the limit

	s := leaseTestStreamer()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 70 * 1024 // one 64K eviction brings 128K under the limit

	s.evictCandidates([]CacheEntry{
		{Path: "old", Size: 64 * 1024},
		{Path: "newer", Size: 64 * 1024},
	}, 128*1024)

	if _, err := os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
		t.Error("oldest candidate should have been evicted")
	}
	if _, err := os.Stat(filepath.Join(dir, "newer")); os.IsNotExist(err) {
		t.Error("eviction should stop once under the limit; 'newer' must survive")
	}
}

// Test_EnforceCacheLimit_RaceWithViewerChurn runs enforceCacheLimit concurrently
// with viewer acquire/release churn on the same active torrent. It asserts no
// behaviour by itself — its value is under `-race`: it exercises the lock-shared
// paths (Stats/buildActiveMaps, evictionBlocked, AcquireViewer/ReleaseViewer)
// against each other to catch any data race on s.active / s.downloads.
func Test_EnforceCacheLimit_RaceWithViewerChurn(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	s.cfg.MaxCacheSize = 1 // force the eviction path to run every call

	spec := str3TorrentSpec(t)
	tor, _, err := s.client.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	h := tor.InfoHash()
	s.mu.Lock()
	s.active[h] = &entry{t: tor, lastAccess: time.Now()}
	s.mu.Unlock()

	const iters = 200
	done := make(chan struct{})
	go func() {
		for i := 0; i < iters; i++ {
			s.enforceCacheLimit()
		}
		close(done)
	}()
	for i := 0; i < iters; i++ {
		s.AcquireViewer(h)
		_ = s.evictionBlocked("str3-sample.bin")
		s.ReleaseViewer(h)
	}
	// Leave a held lease so the last ReleaseViewer's grace timer doesn't fire a
	// drop into a torrent we're about to Close().
	s.AcquireViewer(h)
	<-done
}
