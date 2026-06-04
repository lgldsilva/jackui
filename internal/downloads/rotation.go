package downloads

import (
	"log"
	"regexp"
	"strings"
)

// sourceCooldownMin is how long a stalled source waits before it's eligible
// again in the round-robin.
const sourceCooldownMin = 30

// maxSourceTries caps how many times a single source is retried before it's
// marked failed (drops out of the rotation).
const maxSourceTries = 3

// maxDiscovered caps how many alternatives a single Jackett re-search persists.
const maxDiscovered = 5

// tryRotate attempts to switch a stalled download to an alternative source
// instead of demoting it. Returns true when it rotated (caller skips the demote).
//
// Flow: park the current source (→ cooldown/failed) → look for a ready
// alternative (round-robin, cooldown-gated) → if none, re-search Jackett and
// match by size + season/episode → activate the best candidate (drop the dead
// torrent, point active_magnet at it; the next tick re-inits). Returns false when
// no alternative is available, so the caller demotes to the back of the queue.
func (w *Worker) tryRotate(td *trackedDL, _ QueueSettings) bool {
	d, err := w.store.Get(td.userID, td.id)
	if err != nil || d == nil {
		return false
	}
	w.parkCurrentSource(*d)

	next := w.pickNextSource(d.ID)
	if next == nil {
		w.discoverSources(*d)
		next = w.pickNextSource(d.ID)
	}
	if next == nil {
		return false
	}
	return w.activateSource(td, *d, *next)
}

// parkCurrentSource registers the original source on first rotation, then marks
// the currently-active source tried (→ cooldown, or failed past maxSourceTries).
func (w *Worker) parkCurrentSource(d Download) {
	if has, _ := w.store.HasSources(d.ID); !has {
		_ = w.store.EnsureSource(Source{
			DownloadID: d.ID, Magnet: d.Magnet, InfoHash: d.InfoHash,
			Title: d.Name, Tracker: d.Tracker, Size: d.FileSize,
		}, SourceActive)
	}
	sources, _ := w.store.ListSources(d.ID)
	for _, s := range sources {
		if s.Status == SourceActive {
			_ = w.store.MarkSourceTried(s.ID, maxSourceTries)
		}
	}
}

func (w *Worker) pickNextSource(downloadID int) *Source {
	next, _ := w.store.NextSource(downloadID, sourceCooldownMin)
	return next
}

// discoverSources re-searches Jackett for the download's title and persists the
// matching alternatives (different info_hash, similar size, same S/E) as candidates.
func (w *Worker) discoverSources(d Download) {
	if w.jackett == nil {
		return
	}
	results, err := w.jackett.Search(cleanQuery(d.Name), "", nil)
	if err != nil {
		log.Printf("downloads: rotation re-search #%d failed: %v", d.ID, err)
		return
	}
	matches := matchAlternatives(d, results, maxDiscovered)
	for _, r := range matches {
		_ = w.store.EnsureSource(Source{
			DownloadID: d.ID, Magnet: r.MagnetURI, InfoHash: r.InfoHash,
			Title: r.Title, Tracker: r.Tracker, Seeders: r.Seeders, Size: r.Size,
		}, SourceCandidate)
	}
	if len(matches) > 0 {
		log.Printf("downloads: rotation #%d discovered %d alternative source(s)", d.ID, len(matches))
	}
}

// activateSource switches the download to `next`: persists active_magnet, drops
// the dead torrent and untracks it so the next tick re-inits with the new magnet.
func (w *Worker) activateSource(td *trackedDL, d Download, next Source) bool {
	if err := w.store.ActivateSource(d.ID, next.ID, next.Magnet); err != nil {
		return false
	}
	w.mu.Lock()
	delete(w.tracked, td.id)
	delete(w.retries, td.id)
	if cancel := w.pending[td.id]; cancel != nil {
		cancel()
		delete(w.pending, td.id)
	}
	w.unregisterLocked(td)
	w.mu.Unlock()
	w.streamer.Drop(td.hash)
	hashHint := next.InfoHash
	if len(hashHint) > 8 {
		hashHint = hashHint[:8]
	}
	log.Printf("downloads: #%d %q rotated → source %q (%s, %d seeds)", d.ID, d.Name, next.Tracker, hashHint, next.Seeders)
	return true
}

// cleanQuery turns a release name into a Jackett-friendly search query: drop the
// extension, normalize separators, and trim everything from the first quality/
// source tag onward so the search matches other releases of the same title.
var qualityTagRe = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|x264|x265|h264|h265|hevc|web-?dl|webrip|bluray|bdrip|hdtv|dvdrip|remux)\b`)

func cleanQuery(name string) string {
	q := name
	if i := strings.LastIndexByte(q, '.'); i > 0 && len(q)-i <= 5 {
		q = q[:i] // strip a file extension
	}
	q = strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(q)
	if loc := qualityTagRe.FindStringIndex(q); loc != nil {
		q = q[:loc[0]]
	}
	return strings.TrimSpace(strings.Join(strings.Fields(q), " "))
}
