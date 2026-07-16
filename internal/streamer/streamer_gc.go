package streamer

import (
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

// ─── internal helpers ────────────────────────────────────────────────────────

func (s *Streamer) buildInfo(e *entry, withFiles bool) *TorrentInfo {
	t := e.t
	// Rate sample requires mutating entry counters — must hold s.mu so concurrent
	// GlobalStats / Get callers see a consistent snapshot.
	s.mu.Lock()
	dn, up := sampleRateLocked(e, time.Now())
	s.mu.Unlock()
	st := t.Stats()
	info := &TorrentInfo{
		InfoHash:        t.InfoHash().HexString(),
		Name:            t.Name(),
		TotalSize:       t.Length(),
		Peers:           st.TotalPeers,
		Seeders:         st.ConnectedSeeders,
		DownRate:        dn,
		UpRate:          up,
		BytesDownloaded: t.BytesCompleted(),
		BytesUploaded:   st.BytesWrittenData.Int64(),
	}

	if t.Length() > 0 {
		info.Progress = float64(t.BytesCompleted()) / float64(t.Length())
	}

	// Populate announce trackers list
	mi := t.Metainfo()
	for _, tier := range mi.UpvertedAnnounceList() {
		info.Trackers = append(info.Trackers, tier...)
	}

	if withFiles {
		files := t.Files()
		info.Files = make([]FileInfo, 0, len(files))
		for i, f := range files {
			ext := strings.ToLower(filepath.Ext(f.Path()))
			isVideo := videoExtensions[ext]
			fi := FileInfo{
				Index:      i,
				Path:       f.Path(),
				Size:       f.Length(),
				IsVideo:    isVideo,
				Downloaded: f.BytesCompleted(),
				Priority:   labelFromPriority(f.Priority()),
			}
			if f.Length() > 0 {
				fi.Progress = float64(f.BytesCompleted()) / float64(f.Length())
			}
			info.Files = append(info.Files, fi)
		}
		info.PrimaryFile = pickPrimaryFile(info.Files)
	}
	return info
}

// gcLoop runs every minute and drops torrents idle longer than IdleTimeout.
func (s *Streamer) gcLoop() {
	tick := time.NewTicker(1 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-tick.C:
			dropped := s.dropIdleTorrents(now)
			// Purge hash-check dedup keys outside s.mu (purgeVerifiedFiles takes
			// verifiedMu — avoid nesting the two locks).
			for _, h := range dropped {
				s.purgeVerifiedFiles(h)
			}
			// Then enforce cache size cap (LRU over inactive entries)
			s.enforceCacheLimit()
		}
	}
}

// dropIdleTorrents drops torrents idle longer than IdleTimeout (skipping active
// downloads and seed-tracker torrents) and returns the dropped hashes so the
// caller can purge their dedup keys outside s.mu.
func (s *Streamer) dropIdleTorrents(now time.Time) []metainfo.Hash {
	var dropped []metainfo.Hash
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, e := range s.active {
		if now.Sub(e.lastAccess) <= s.cfg.IdleTimeout {
			continue
		}
		// Active downloads stay alive even when idle — the user is waiting for
		// the file to finish in background.
		if _, protected := s.downloads[e.t.Name()]; protected {
			continue
		}
		// Seed-tracker torrents keep seeding regardless of idleness.
		if s.shouldKeepSeeding(e.t) {
			continue
		}
		log.Printf("streamer: dropping idle torrent %s (%s)", e.t.Name(), h.HexString()[:8])
		delete(s.active, h)
		e.t.Drop()
		dropped = append(dropped, h)
	}
	return dropped
}

// firstChars returns up to n characters from s — for error messages without leaking huge URLs.
func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ─── Active torrent controls (Transmission-style) ───────────────────────────

// NewForTesting returns a Streamer with only the fields the
// non-torrent-client-touching handlers exercise (active map, downloads
// protection set, rate limiters). Opening a real anacrolix client requires
// binding UDP :42069, which collides between parallel test packages and a
// running dev server. Use this in handler/unit tests that don't need the
// torrent transport.
func NewForTesting() *Streamer {
	return &Streamer{
		active:        make(map[metainfo.Hash]*entry),
		downloads:     make(map[string]struct{}),
		dlLimiter:     rate.NewLimiter(rate.Inf, 1<<16),
		upLimiter:     rate.NewLimiter(rate.Inf, 1<<16),
		verifiedFiles: make(map[string]bool),
		verifyLim:     newVerifyLimiter(1),
	}
}

// SetVerifyConcurrency sets how many piece-hash jobs may run in parallel
// (disk-bound). Independent of downloads max_active. n < 1 clamps to 1.
// Live — no restart. Safe to call with a nil Streamer.
func (s *Streamer) SetVerifyConcurrency(n int) {
	if s == nil || s.verifyLim == nil {
		return
	}
	s.verifyLim.SetLimit(n)
}

// VerifyConcurrency returns the current piece-hash concurrency cap (0 if unset).
func (s *Streamer) VerifyConcurrency() int {
	if s == nil || s.verifyLim == nil {
		return 0
	}
	return s.verifyLim.Limit()
}
