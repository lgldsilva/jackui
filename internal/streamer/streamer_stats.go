package streamer

import (
	"errors"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Stats/peers/rate por torrent — extraído de streamer.go.
// Get returns the current TorrentInfo for an active torrent.
func (s *Streamer) Get(hash metainfo.Hash) (*TorrentInfo, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return nil, errors.New("torrent não encontrado (expirou ou nunca foi adicionado)")
	}
	return s.buildInfo(e, true), nil
}

// LiveStats returns a torrent's current down/up rate + connected seeders WITHOUT
// building the full file list. buildInfo (used by Get) iterates t.Files() — a
// 778-file pack walks every file under the client lock, so enriching the
// downloads list via Get made GET /api/downloads take many SECONDS (worse under
// active-download lock contention). The list only needs the per-torrent
// rate/seeders, so this skips the O(files) loop → O(1) per torrent. uploaded is
// the cumulative bytes served THIS session (anacrolix BytesWrittenData; resets on
// re-add). ok=false when the torrent isn't active.
func (s *Streamer) LiveStats(hash metainfo.Hash) (down, up, uploaded int64, seeders int, ok bool) {
	now := time.Now()
	s.mu.Lock()
	e, exists := s.active[hash]
	if !exists {
		s.mu.Unlock()
		return 0, 0, 0, 0, false
	}
	e.lastAccess = now
	down, up = sampleRateLocked(e, now)
	t := e.t
	s.mu.Unlock()
	st := t.Stats()
	return down, up, st.BytesWrittenData.Int64(), st.ConnectedSeeders, true
}

// Peers returns a snapshot of the currently-connected peers of an active
// torrent for the downloads inspector. Errors when the torrent isn't active
// (dropped or never opened). The peer set is read live from anacrolix.
func (s *Streamer) Peers(hash metainfo.Hash) ([]PeerInfo, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return nil, errors.New("torrent não encontrado (expirou ou nunca foi adicionado)")
	}
	t := e.t
	numPieces := t.NumPieces()
	conns := t.PeerConns()
	out := make([]PeerInfo, 0, len(conns))
	for _, pc := range conns {
		st := pc.Stats()
		pi := PeerInfo{
			Network:    pc.Network,
			DownRate:   int64(pc.DownloadRate()),
			UpRate:     int64(st.LastWriteUploadRate),
			Downloaded: st.BytesReadData.Int64(),
			Uploaded:   st.BytesWrittenData.Int64(),
			Encrypted:  pc.PeerPrefersEncryption,
		}
		if pc.RemoteAddr != nil {
			pi.Addr = pc.RemoteAddr.String()
		}
		if name, _ := pc.PeerClientName.Load().(string); name != "" {
			pi.Client = name
		}
		if numPieces > 0 {
			pi.Availability = float64(st.RemotePieceCount) / float64(numPieces)
			pi.IsSeeder = st.RemotePieceCount >= numPieces
		}
		pi.Receiving = pi.DownRate > 0
		pi.Sending = pi.UpRate > 0
		out = append(out, pi)
	}
	return out, nil
}

// GlobalStats returns aggregate download/upload rates across all active torrents.
// Snapshot taken under the streamer lock; safe to poll from a handler.
func (s *Streamer) GlobalStats() GlobalRate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := GlobalRate{ActiveTorrents: len(s.active)}
	now := time.Now()
	for _, e := range s.active {
		dn, up := sampleRateLocked(e, now)
		g.DownRate += dn
		g.UpRate += up
	}
	return g
}

// sampleRateLocked computes per-second rates for an entry by diffing against the
// previous sample and then updates the entry's sample state. Caller holds s.mu.
//
// Returns (0, 0) on the very first sample after Add or when the elapsed window is
// too small (< 250ms) to give a meaningful rate.
func sampleRateLocked(e *entry, now time.Time) (downRate, upRate int64) {
	st := e.t.Stats()
	br := st.BytesReadData.Int64()
	bw := st.BytesWrittenData.Int64()
	elapsed := now.Sub(e.lastSampleAt).Seconds()

	if e.lastSampleAt.IsZero() || elapsed < 0.25 {
		// First sample after Add or sampled too soon — record and emit zero.
		e.lastBytesRead = br
		e.lastBytesWritten = bw
		e.lastSampleAt = now
		return 0, 0
	}

	dr := br - e.lastBytesRead
	dw := bw - e.lastBytesWritten
	if dr < 0 {
		dr = 0 // counter reset (e.g., torrent dropped + re-added)
	}
	if dw < 0 {
		dw = 0
	}
	downRate = int64(float64(dr) / elapsed)
	upRate = int64(float64(dw) / elapsed)

	e.lastBytesRead = br
	e.lastBytesWritten = bw
	e.lastSampleAt = now
	return downRate, upRate
}
