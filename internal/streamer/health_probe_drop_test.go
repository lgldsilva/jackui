package streamer

import (
	"testing"
	"time"
)

// dropProbeEntry must release a probe-only torrent (Drop() never could — the
// probe's own registration sits inside the 60s activeReadGuard, so every probe
// leaked its torrent until the idle reaper), while still refusing whenever the
// entry shows ANY sign of real use.
func TestDropProbeEntry(t *testing.T) {
	setup := func(t *testing.T) (*Streamer, *entry, [20]byte) {
		t.Helper()
		s, err := newTestStreamer(t, Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		tor, _, err := s.client.AddTorrentSpec(str3TorrentSpec(t))
		if err != nil {
			t.Fatalf("AddTorrentSpec: %v", err)
		}
		hash := tor.InfoHash()
		e := &entry{t: tor, lastAccess: time.Now()}
		s.mu.Lock()
		s.active[hash] = e
		s.mu.Unlock()
		return s, e, hash
	}

	isActive := func(s *Streamer, hash [20]byte) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, ok := s.active[hash]
		return ok
	}

	t.Run("untouched probe entry is dropped", func(t *testing.T) {
		s, e, hash := setup(t)
		s.dropProbeEntry(hash, e, e.lastAccess)
		if isActive(s, hash) {
			t.Error("probe entry should have been dropped (lastAccess untouched, no viewers)")
		}
	})

	t.Run("read since registration keeps it", func(t *testing.T) {
		s, e, hash := setup(t)
		la0 := e.lastAccess
		s.mu.Lock()
		e.lastAccess = e.lastAccess.Add(time.Second) // a viewer read mid-probe
		s.mu.Unlock()
		s.dropProbeEntry(hash, e, la0)
		if !isActive(s, hash) {
			t.Error("entry read after registration must NOT be dropped")
		}
	})

	t.Run("viewer lease keeps it", func(t *testing.T) {
		s, e, hash := setup(t)
		s.mu.Lock()
		e.viewers = 1
		s.mu.Unlock()
		s.dropProbeEntry(hash, e, e.lastAccess)
		if !isActive(s, hash) {
			t.Error("entry with a viewer lease must NOT be dropped")
		}
	})

	t.Run("download protection keeps it", func(t *testing.T) {
		s, e, hash := setup(t)
		s.mu.Lock()
		s.downloads[e.t.Name()] = struct{}{}
		s.mu.Unlock()
		s.dropProbeEntry(hash, e, e.lastAccess)
		if !isActive(s, hash) {
			t.Error("download-protected entry must NOT be dropped")
		}
	})

	t.Run("nil probe entry is a no-op", func(t *testing.T) {
		s, _, hash := setup(t)
		s.dropProbeEntry(hash, nil, time.Time{})
		if !isActive(s, hash) {
			t.Error("nil probeEntry must not drop anything")
		}
	})
}
