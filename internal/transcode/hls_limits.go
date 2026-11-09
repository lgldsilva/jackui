package transcode

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrTooManySessions is returned by GetOrStart when the global HLS session cap
// is reached (or the owner's cap is reached and none of its sessions can be
// evicted). Handlers map it to HTTP 429.
var ErrTooManySessions = errors.New("hls: too many concurrent transcode sessions")

// Every distinct ?playback= id (client-chosen) used to spawn its own ffmpeg
// with no upper bound, so one client could exhaust CPU/disk. Defaults leave
// room for a multi-variant master (ladder rungs + audio renditions) per user
// and a household of concurrent viewers.
const (
	defaultMaxHLSSessions        = 24
	defaultMaxHLSSessionsPerUser = 8
)

func hlsLimitFromEnv(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n // 0 = unlimited
}

// SetSessionLimits overrides the HLS session caps (tests; live config).
// Values <= 0 mean unlimited.
func (m *HLSSessionManager) SetSessionLimits(total, perOwner int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxSessions, m.maxPerOwner = total, perOwner
}

// admitLocked decides whether a new session for owner may be built. When the
// owner is at its cap it evicts the owner's least-recently-accessed session
// (removed from the map here; the caller stops it outside m.mu) — a user
// switching between playbacks keeps working, a client spraying ?playback= ids
// stays bounded. Caller holds m.mu.
func (m *HLSSessionManager) admitLocked(owner string) (*HLSSession, error) {
	var victim *HLSSession
	if owner != "" && m.maxPerOwner > 0 && m.ownerCountLocked(owner) >= m.maxPerOwner {
		victim = m.ownerLRULocked(owner)
		if victim == nil {
			return nil, ErrTooManySessions // all of the owner's slots are still starting
		}
		delete(m.sess, victim.Key)
	}
	if m.maxSessions > 0 && len(m.sess)+len(m.starting) >= m.maxSessions {
		if victim != nil {
			m.sess[victim.Key] = victim // undo: refusing anyway, keep it alive
		}
		return nil, ErrTooManySessions
	}
	return victim, nil
}

func (m *HLSSessionManager) ownerCountLocked(owner string) int {
	n := 0
	for _, s := range m.sess {
		if s.owner == owner {
			n++
		}
	}
	for _, o := range m.startingOwner {
		if o == owner {
			n++
		}
	}
	return n
}

func (m *HLSSessionManager) ownerLRULocked(owner string) *HLSSession {
	var lru *HLSSession
	var lruAt time.Time
	for _, s := range m.sess {
		if s.owner != owner {
			continue
		}
		s.mu.Lock()
		la := s.LastAccess
		s.mu.Unlock()
		if lru == nil || la.Before(lruAt) {
			lru, lruAt = s, la
		}
	}
	return lru
}
