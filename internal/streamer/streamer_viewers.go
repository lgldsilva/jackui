package streamer

import (
	"log"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// viewerGrace is how long a stream-only torrent lingers after its last viewer
// leaves before being dropped. Short enough to stop seeding promptly, long
// enough to absorb a quick reopen and React StrictMode's dev double-mount.
const viewerGrace = 8 * time.Second

// AcquireViewer registers an open player session ("lease") on a torrent and
// cancels any pending drop. Called when the player opens a stream. No-op if the
// torrent isn't active (e.g. a local file, which lives outside the streamer).
func (s *Streamer) AcquireViewer(hash metainfo.Hash) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return
	}
	e.viewers++
	e.lastAccess = time.Now()
	if e.dropTimer != nil {
		e.dropTimer.Stop()
		e.dropTimer = nil
	}
}

// ReleaseViewer drops a player session's lease. When the LAST viewer leaves a
// stream-only (non-download) torrent, it schedules a drop after viewerGrace
// instead of dropping eagerly — so other viewers keep streaming and a quick
// reopen cancels the teardown.
//
// Returns (scheduled, lastViewer). scheduled is true when a drop was scheduled.
// lastViewer is true whenever THIS call removed the final viewer — even when the
// torrent is kept alive (background download or seed-tracker): the HLS transcode
// exists only to feed the player, so the caller must stop it once nobody is
// watching, while the torrent keeps seeding/downloading on its own.
func (s *Streamer) ReleaseViewer(hash metainfo.Hash) (scheduled, lastViewer bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return false, false
	}
	if e.viewers > 0 {
		e.viewers--
	}
	if e.viewers > 0 {
		return false, false // another viewer still watching
	}
	// No viewers left — the caller stops the HLS transcode regardless of what
	// keeps the torrent alive below.
	// Deliberate background downloads stay alive regardless of viewers.
	if _, protected := s.downloads[e.t.Name()]; protected {
		return false, true
	}
	// Seed-tracker torrents keep uploading after the viewer leaves — never drop.
	if s.shouldKeepSeeding(e.t) {
		return false, true
	}
	if e.dropTimer != nil {
		e.dropTimer.Stop()
	}
	e.dropTimer = time.AfterFunc(viewerGrace, func() { s.dropIfStillIdle(hash, e) })
	return true, true
}

// dropIfStillIdle runs when a viewer-lease grace timer fires. It drops the
// torrent only if nothing changed in the meantime: same entry still active, no
// viewers re-acquired, and not a protected download.
func (s *Streamer) dropIfStillIdle(hash metainfo.Hash, e *entry) {
	s.mu.Lock()
	cur, ok := s.active[hash]
	if !ok || cur != e || e.viewers > 0 {
		s.mu.Unlock()
		return
	}
	if _, protected := s.downloads[e.t.Name()]; protected {
		s.mu.Unlock()
		return
	}
	if s.shouldKeepSeeding(e.t) {
		e.dropTimer = nil
		s.mu.Unlock()
		return
	}
	delete(s.active, hash)
	e.dropTimer = nil
	s.mu.Unlock()
	log.Printf("streamer: dropping stream-only torrent %s (%s) — no viewers", e.t.Name(), hash.HexString()[:8])
	e.t.Drop()
	s.purgeVerifiedFiles(hash)
}
