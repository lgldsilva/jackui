package streamer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/types"
)

// Controle: pause/resume/priority/active-list — extraído de streamer.go.
// Pause soft-pauses a torrent by zeroing its max established connections.
// anacrolix lacks a native Pause; this is the closest equivalent — existing
// peers drop off as TCP keepalives expire, and no new peers are accepted.
// On-disk pieces stay, so Resume picks up where we left off.
func (s *Streamer) Pause(hash metainfo.Hash) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return ErrTorrentNotActive
	}
	if e.paused {
		return nil // idempotent
	}
	e.t.SetMaxEstablishedConns(0)
	e.paused = true
	return nil
}

// Resume re-enables peer connections previously zeroed by Pause. Idempotent.
func (s *Streamer) Resume(hash metainfo.Hash) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.active[hash]
	if !ok {
		return ErrTorrentNotActive
	}
	if !e.paused {
		return nil
	}
	e.t.SetMaxEstablishedConns(defaultMaxEstablishedConns)
	e.paused = false
	return nil
}

// SetPriority changes the requested piece priority for every file in the
// torrent. anacrolix uses this to bias the request scheduler — "high" pieces
// will be fetched before "normal", which precede "low". Accepted labels:
// "low" | "normal" | "high".
func (s *Streamer) SetPriority(hash metainfo.Hash, label string) error {
	prio, ok := priorityFromLabel(label)
	if !ok {
		return fmt.Errorf("invalid priority %q (want low|normal|high)", label)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.active[hash]
	if !found {
		return ErrTorrentNotActive
	}
	for _, f := range e.t.Files() {
		f.SetPriority(prio)
	}
	e.priority = strings.ToLower(label)
	return nil
}

// ActiveList returns a snapshot of every torrent currently loaded by the
// streamer, formatted for the Transmission-style downloads UI. Each entry has
// rate samples taken under the streamer lock so the numbers are consistent
// across the slice.
func (s *Streamer) ActiveList() []*TorrentInfo {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.active))
	for _, e := range s.active {
		entries = append(entries, e)
	}
	s.mu.Unlock()

	out := make([]*TorrentInfo, 0, len(entries))
	for _, e := range entries {
		info := s.buildInfo(e, false)
		s.mu.Lock()
		info.Status = statusForLocked(e)
		info.Priority = e.priority
		s.mu.Unlock()
		out = append(out, info)
	}
	// Deterministic order — by name — avoids the UI rows shuffling on each poll.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PauseAll soft-pauses every active torrent. Returns the count of newly paused
// torrents (already-paused ones are not double-counted).
func (s *Streamer) PauseAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.active {
		if e.paused {
			continue
		}
		e.t.SetMaxEstablishedConns(0)
		e.paused = true
		n++
	}
	return n
}

// ResumeAll re-enables every soft-paused torrent.
func (s *Streamer) ResumeAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.active {
		if !e.paused {
			continue
		}
		e.t.SetMaxEstablishedConns(defaultMaxEstablishedConns)
		e.paused = false
		n++
	}
	return n
}

func (s *Streamer) ListenPort() int {
	return s.cfg.ListenPort
}

// statusForLocked returns the Transmission-style status label. Caller holds s.mu.
func statusForLocked(e *entry) string {
	if e.paused {
		return "paused"
	}
	t := e.t
	if t.Info() == nil {
		return "fetching-metadata"
	}
	if t.BytesCompleted() >= t.Length() && t.Length() > 0 {
		if t.Seeding() {
			return "seeding"
		}
		return "complete"
	}
	return "downloading"
}

// priorityFromLabel parses the user-facing string into an anacrolix priority.
func priorityFromLabel(label string) (types.PiecePriority, bool) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "none":
		return types.PiecePriorityNone, true
	case "low":
		return types.PiecePriorityNormal, true
	case "", "normal":
		return types.PiecePriorityHigh, true
	case "high":
		return types.PiecePriorityNow, true
	}
	return 0, false
}

func labelFromPriority(prio types.PiecePriority) string {
	switch prio {
	case types.PiecePriorityNone:
		return "none"
	case types.PiecePriorityNormal:
		return "low"
	case types.PiecePriorityHigh:
		return "normal"
	case types.PiecePriorityNow:
		return "high"
	default:
		return "normal"
	}
}

// SetFilePriority changes the priority of a single file in the active torrent.
func (s *Streamer) SetFilePriority(hash metainfo.Hash, fileIdx int, label string) error {
	prio, ok := priorityFromLabel(label)
	if !ok {
		return fmt.Errorf("invalid priority %q (want none|low|normal|high)", label)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.active[hash]
	if !found {
		return ErrTorrentNotActive
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	files[fileIdx].SetPriority(prio)
	return nil
}
