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
		mi := e.t.Metainfo()
		s.mu.Unlock()
		return s.activeHealth(hash, st.ConnectedSeeders, st.TotalPeers, trackersFromMetainfo(&mi)), true
	}
	s.mu.Unlock()
	return s.cache.GetHealth(hash.HexString()), false
}

// activeHealth builds the health for an ACTIVE torrent from its live stats.
// ConnectedSeeders counts only the SEEDERS we're connected to right now — it can
// be 0 while playing (pulling from leechers) even though the tracker reports
// many. Show the real swarm size (last scrape) when higher, and kick a background
// scrape when there's no fresh one, so an active torrent never sits at a
// misleading 0. We do NOT persist the live count here (it would bury the scrape's
// CheckedAt); the cache is written only by scrapes. Split out of HealthSnapshot
// so it's testable without a live torrent.
func (s *Streamer) activeHealth(hash metainfo.Hash, liveSeeders, livePeers int, trackers []string) *CachedHealth {
	cached := s.cache.GetHealth(hash.HexString())
	seeders := liveSeeders
	if cached != nil && cached.Seeders > seeders {
		seeders = cached.Seeders
	}
	s.maybeScrapeActive(hash, trackers, cached)
	return &CachedHealth{
		Seeders:   seeders,
		Peers:     livePeers,
		Available: seeders > 0 || livePeers > 0,
		CheckedAt: time.Now(),
	}
}

// maybeScrapeActive kicks a one-off background tracker scrape for an ACTIVE
// torrent when there's no fresh scrape yet, so the badge shows the real swarm
// size instead of just the peers we've connected to. Deduped/throttled like
// ProbeHealthAsync; no-op without trackers or while a probe is already running.
func (s *Streamer) maybeScrapeActive(hash metainfo.Hash, trackers []string, cached *CachedHealth) {
	if len(trackers) == 0 {
		return
	}
	if cached != nil && time.Since(cached.CheckedAt) < HealthFreshFor {
		return // a fresh scrape already exists
	}
	if _, busy := healthInflight.LoadOrStore(hash, true); busy {
		return
	}
	go func() {
		defer healthInflight.Delete(hash)
		healthProbeSem <- struct{}{}
		defer func() { <-healthProbeSem }()
		ctx, cancel := context.WithTimeout(context.Background(), scrapeBudget)
		defer cancel()
		if seeders, leechers, ok := scrapeSwarm(ctx, hash, trackers); ok {
			_ = s.cache.SetHealth(hash.HexString(), seeders, leechers)
		}
	}()
}

// seedersNotBelowScrape clamps a live ConnectedSeeders count up to the last
// persisted tracker scrape, if larger — the scrape is the real swarm size, the
// live count is just who we've connected to.
func (s *Streamer) seedersNotBelowScrape(hash metainfo.Hash, live int) int {
	if cached := s.cache.GetHealth(hash.HexString()); cached != nil && cached.Seeders > live {
		return cached.Seeders
	}
	return live
}

// CanProbeHealth reports whether a swarm probe is possible for this hash: we need
// either a magnet (its tr= trackers) or a cached .torrent (its announce list,
// which carries a private tracker's passkey). Private results from amigos-share
// ship no magnet, so the cached .torrent is the only tracker source.
func (s *Streamer) CanProbeHealth(hash metainfo.Hash, magnet string) bool {
	return magnet != "" || s.loadCachedMetainfo(hash) != nil
}

// ProbeHealthAsync runs a background swarm probe for an INACTIVE torrent: scrape
// the trackers (magnet tr= + cached .torrent announce list) for the real swarm
// size, persist it, falling back to a brief swarm-connect count. Throttled +
// deduped. No-op when there's no tracker source or a probe is already running.
func (s *Streamer) ProbeHealthAsync(hash metainfo.Hash, magnet string) {
	if !s.CanProbeHealth(hash, magnet) {
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
	// Already streaming? Just snapshot live — never interfere with a play. Keep
	// the seeders count from regressing the last tracker scrape (see HealthSnapshot).
	if e := s.activeEntry(hash); e != nil {
		st := e.t.Stats()
		_ = s.cache.SetHealth(hash.HexString(), s.seedersNotBelowScrape(hash, st.ConnectedSeeders), st.TotalPeers)
		return
	}

	// Preferred: tracker scrape (BEP 48) — the real swarm size the tracker
	// publishes, without joining the swarm. Trackers come from the magnet's tr=
	// AND the cached .torrent's announce list (the latter carries a private
	// tracker's passkey, so amigos-share works even with no magnet). Falls through
	// to the live connect probe only when no tracker answered.
	trackers := trackersFromMagnet(magnet)
	trackers = append(trackers, trackersFromMetainfo(s.loadCachedMetainfo(hash))...)
	sctx, scancel := context.WithTimeout(context.Background(), scrapeBudget)
	seeders, leechers, ok := scrapeSwarm(sctx, hash, trackers)
	scancel()
	if ok {
		_ = s.cache.SetHealth(hash.HexString(), seeders, leechers)
		return
	}

	// No magnet to join the swarm with (private result whose trackers didn't
	// answer the scrape) — leave the previous snapshot rather than zeroing it.
	if magnet == "" {
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
