package streamer

import (
	"context"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

const (
	// HealthFreshFor — re-probe only when the persisted snapshot is older.
	HealthFreshFor = 30 * time.Minute
	// healthPeerWait — how long to let peers connect after Add before counting.
	healthPeerWait = 6 * time.Second
)

var (
	// healthProbeSem caps concurrent swarm probes so scrolling a long favorites
	// list doesn't open dozens of swarm connections at once (all via the VPN).
	healthProbeSem = make(chan struct{}, 3)
	// healthInflight dedupes probes per info_hash.
	healthInflight sync.Map
)

// activeEntry returns the active entry for a hash (or nil), bumping lastAccess.
func (s *Streamer) activeEntry(hash metainfo.Hash) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[hash]
}

// HealthSnapshot returns the cheapest available swarm health: live stats when the
// torrent is active (also refreshing the persisted copy), else the last probe
// (nil if never probed). Never touches the swarm.
func (s *Streamer) HealthSnapshot(hash metainfo.Hash) (health *CachedHealth, active bool) {
	// Read Stats() while STILL holding s.mu. Releasing the lock first (the old
	// code did) opened a TOCTOU: gcLoop/Drop could t.Drop() the torrent between
	// the unlock and e.t.Stats(), racing/​panicking on a torn-down torrent.
	// buildInfo already calls t.Stats() under s.mu, so this is consistent.
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		st := e.t.Stats()
		s.mu.Unlock()
		h := &CachedHealth{
			Seeders:   st.ConnectedSeeders,
			Peers:     st.TotalPeers,
			Available: st.ConnectedSeeders > 0 || st.TotalPeers > 0,
			CheckedAt: time.Now(),
		}
		_ = s.cache.SetHealth(hash.HexString(), h.Seeders, h.Peers)
		return h, true
	}
	s.mu.Unlock()
	return s.cache.GetHealth(hash.HexString()), false
}

// ProbeHealthAsync runs a background swarm probe for an INACTIVE torrent: add the
// magnet, let peers connect briefly, snapshot seeders/peers, persist, then drop
// it (unless a real playback attached meanwhile). Throttled + deduped. No-op when
// the magnet is empty or a probe for this hash is already running.
func (s *Streamer) ProbeHealthAsync(hash metainfo.Hash, magnet string) {
	if magnet == "" {
		return
	}
	if _, busy := healthInflight.LoadOrStore(hash, true); busy {
		return
	}
	go func() {
		defer healthInflight.Delete(hash)
		healthProbeSem <- struct{}{}
		defer func() { <-healthProbeSem }()
		s.probeHealth(hash, magnet)
	}()
}

func (s *Streamer) probeHealth(hash metainfo.Hash, magnet string) {
	// Already streaming? Just snapshot live — never interfere with a play.
	if e := s.activeEntry(hash); e != nil {
		st := e.t.Stats()
		_ = s.cache.SetHealth(hash.HexString(), st.ConnectedSeeders, st.TotalPeers)
		return
	}

	// Preferred: tracker scrape (BEP 48) — the real swarm size the tracker
	// publishes, without joining the swarm. Falls through to the live connect
	// probe below only when no tracker answered (DHT-only magnets, dead trackers).
	sctx, scancel := context.WithTimeout(context.Background(), scrapeBudget)
	seeders, leechers, ok := scrapeSwarm(sctx, hash, trackersFromMagnet(magnet))
	scancel()
	if ok {
		_ = s.cache.SetHealth(hash.HexString(), seeders, leechers)
		return
	}

	// Fallback (no tracker answered — DHT-only magnet / dead trackers): add the
	// magnet, let peers connect briefly, count connected seeders/peers, then drop.
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.MetadataWait+healthPeerWait+2*time.Second)
	defer cancel()
	if _, err := s.Add(ctx, magnet); err != nil {
		_ = s.cache.SetHealth(hash.HexString(), 0, 0)
		return
	}

	// Snapshot the probe's own entry + its lastAccess UNDER the lock —
	// trackingReader bumps lastAccess on every read, so a bare read raced.
	s.mu.Lock()
	probeEntry := s.active[hash]
	var la0 time.Time
	if probeEntry != nil {
		la0 = probeEntry.lastAccess
	}
	s.mu.Unlock()

	select {
	case <-time.After(healthPeerWait):
	case <-ctx.Done():
	}

	s.mu.Lock()
	e := s.active[hash]
	if e == nil {
		s.mu.Unlock()
		return
	}
	st := e.t.Stats()
	s.mu.Unlock()
	_ = s.cache.SetHealth(hash.HexString(), st.ConnectedSeeders, st.TotalPeers)
	s.dropProbeEntry(hash, probeEntry, la0)
}

// dropProbeEntry releases a torrent that was activated ONLY for a health
// probe. It deliberately bypasses Drop()'s activeReadGuard: the probe itself
// registered the torrent ~6s ago (always inside the 60s guard window), so
// Drop() was unconditionally a no-op and every probe left its torrent loaded,
// connected to the swarm, until the 30-min idle reaper — scrolling a favorites
// list piled them up. Safety still holds: under the lock we require the SAME
// entry with an UNTOUCHED lastAccess (any real read bumps it via
// trackingReader), zero viewer leases, and no download protection.
func (s *Streamer) dropProbeEntry(hash metainfo.Hash, probeEntry *entry, la0 time.Time) {
	if probeEntry == nil {
		return
	}
	s.mu.Lock()
	e, ok := s.active[hash]
	if !ok || e != probeEntry || !e.lastAccess.Equal(la0) || e.viewers > 0 {
		s.mu.Unlock()
		return
	}
	if _, protected := s.downloads[e.t.Name()]; protected {
		s.mu.Unlock()
		return
	}
	delete(s.active, hash)
	s.mu.Unlock()
	e.t.Drop()
	s.purgeVerifiedFiles(hash)
}
