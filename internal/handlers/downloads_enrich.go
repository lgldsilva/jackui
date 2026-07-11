package handlers

import (
	"net/url"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// parseMagnetTrackers extracts &tr= tracker URLs from a magnet URI.
// The anacrolix runtime uses these but they are NOT in the torrent's stored
// metainfo, so UpvertedAnnounceList() misses them entirely.
func parseMagnetTrackers(magnet string) []string {
	if magnet == "" {
		return nil
	}
	// Magnet URIs aren't valid URLs, but we can parse the query string.
	rest := magnet
	if idx := strings.Index(magnet, "?"); idx >= 0 {
		rest = magnet[idx+1:]
	}
	vals, err := url.ParseQuery(rest)
	if err != nil {
		return nil
	}
	return vals["tr"]
}

// userCache is a simple in-memory cache for username lookups during a single
// request. Avoids N+1 queries to the auth store for each download row.
type userCache map[int]string

func (uc userCache) get(store *auth.Store, userID int) string {
	if s, ok := uc[userID]; ok {
		return s
	}
	if store == nil {
		return ""
	}
	u, err := store.GetUserByID(userID)
	if err != nil {
		return ""
	}
	uc[userID] = u.Username
	return u.Username
}

// liveStats holds a torrent's live (non-persisted) metrics. They're per-torrent,
// so every selected file of one torrent shares them.
type liveStats struct {
	down, up int64
	uploaded int64 // cumulative bytes served this session (anacrolix BytesWrittenData)
	seeders  int
}

// enrichETA populates the live metrics + ETA for a download by looking up the
// active torrent info from the streamer. No-op when streamer is nil or the
// torrent isn't active — the row's existing values are preserved.
func enrichETA(d *downloads.Download, s *streamer.Streamer) {
	if s == nil || d.InfoHash == "" || d.FileSize <= 0 {
		return
	}
	if st, ok := liveStatsOf(s, d.InfoHash); ok {
		applyLive(d, st)
	}
}

// applyLive sets DownRate/UpRate/Seeders + ETA on a row from its torrent's
// shared stats. The UI sorts by down/up rate and seeders client-side (they're
// live, not stored, so they can't be ORDER BY'd in SQL).
func applyLive(d *downloads.Download, st liveStats) {
	d.DownRate, d.UpRate, d.Seeders = st.down, st.up, st.seeders
	d.BytesUploaded = st.uploaded
	if st.down <= 0 {
		return
	}
	if remaining := d.FileSize - d.BytesDownloaded; remaining > 0 {
		d.ETA = int(remaining / st.down)
	}
}

// enrichETAList fills the live metrics + ETA for the slice, looking each torrent
// up in the streamer ONCE per unique info_hash. Many rows are selected files of
// the SAME torrent (a multi-file pack is hundreds of rows), and s.Get→buildInfo
// is O(files); doing it per row was O(rows×files) and locked the torrent client
// thousands of times — which made GET /api/downloads take MINUTES on a big pack.
// Deduping by hash makes it O(unique active torrents).
//
// NOTE: This relies on the implicit invariant that all files of the same torrent
// (sharing the same InfoHash) report the same aggregated torrent downRate/upRate
// and seeders. Caching stats by hash (byHash) ensures that even if list sorting
// is unstable, the rate applied to any sibling row is identical and stable.
func enrichETAList(list []downloads.Download, s *streamer.Streamer) {
	if s == nil {
		return
	}
	byHash := make(map[string]liveStats)
	for i := range list {
		d := &list[i]
		if d.InfoHash == "" || d.FileSize <= 0 {
			continue
		}
		st, seen := byHash[d.InfoHash]
		if !seen {
			st, _ = liveStatsOf(s, d.InfoHash) // zero value when inactive — rows default to 0 anyway
			byHash[d.InfoHash] = st
		}
		applyLive(d, st)
	}
}

// liveStatsOf returns a torrent's current down/up rate + seeders. ok is false
// when the torrent isn't active (or the hash is malformed) — callers preserve
// the row's existing values in that case. One streamer lookup; callers cache it
// by hash.
func liveStatsOf(s *streamer.Streamer, infoHash string) (liveStats, bool) {
	var h metainfo.Hash
	if err := h.FromHexString(infoHash); err != nil {
		return liveStats{}, false
	}
	// LiveStats (not Get) — Get→buildInfo is O(files) and a multi-file pack
	// (Morgpie: 778 files) made enriching the list take many seconds under load.
	// LiveStats is O(1) per torrent: just the cached rate sample + Stats().
	down, up, uploaded, seeders, ok := s.LiveStats(h)
	if !ok {
		return liveStats{}, false
	}
	return liveStats{down: down, up: up, uploaded: uploaded, seeders: seeders}, true
}

// markPromoted sets Promoted=true for completed downloads whose FilePath is
// outside the download dir (i.e. the file was moved to a library/GDrive).
func markPromoted(list []downloads.Download, downloadDir string) {
	if downloadDir == "" {
		return
	}
	for i := range list {
		d := &list[i]
		if d.Status == downloads.StatusCompleted && d.FilePath != "" &&
			!strings.HasPrefix(d.FilePath, downloadDir) {
			d.Promoted = true
		}
	}
}
