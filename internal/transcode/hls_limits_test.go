package transcode

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func seedSessions(m *HLSSessionManager, owner string, n int, base time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("%s-%d", owner, i)
		m.sess[k] = &HLSSession{Key: k, owner: owner, LastAccess: base.Add(time.Duration(i) * time.Second)}
	}
}

// TestGetOrStart_GlobalCapRefuses: a new session past the global cap is
// refused with ErrTooManySessions (→ 429) and the handed-over source closed —
// before this, every distinct ?playback= id spawned another ffmpeg.
func TestGetOrStart_GlobalCapRefuses(t *testing.T) {
	m := lifecycleManager(t)
	m.SetSessionLimits(3, 0)
	seedSessions(m, "u1", 2, time.Now())
	seedSessions(m, "u2", 1, time.Now())

	src := &fakeSource{}
	_, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "new", Source: src, Owner: "u3"})
	if !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("err = %v, want ErrTooManySessions", err)
	}
	if !src.closed.Load() {
		t.Error("refused request must close its source")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sess) != 3 || len(m.starting) != 0 {
		t.Errorf("sess=%d starting=%d, want 3/0 (nothing evicted, nothing leaked)", len(m.sess), len(m.starting))
	}
}

// TestAdmit_OwnerCapEvictsOwnersLRU: at its per-owner cap, the owner's
// least-recently-used session makes room; other owners are untouched.
func TestAdmit_OwnerCapEvictsOwnersLRU(t *testing.T) {
	m := lifecycleManager(t)
	m.SetSessionLimits(0, 2)
	base := time.Now().Add(-time.Minute)
	seedSessions(m, "u1", 2, base) // u1-0 is the LRU
	seedSessions(m, "u2", 1, base.Add(-time.Hour))

	m.mu.Lock()
	victim, err := m.admitLocked("u1")
	m.mu.Unlock()
	if err != nil || victim == nil || victim.Key != "u1-0" {
		t.Fatalf("admit = (%v, %v), want evicted u1-0", victim, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sess["u1-0"]; ok {
		t.Error("victim must be removed from the map")
	}
	if _, ok := m.sess["u2-0"]; !ok {
		t.Error("another owner's (older) session must not be evicted")
	}
}

// TestAdmit_OwnerCapAllStartingRefuses: when every one of the owner's slots
// is still launching there is nothing to evict → refuse.
func TestAdmit_OwnerCapAllStartingRefuses(t *testing.T) {
	m := lifecycleManager(t)
	m.SetSessionLimits(0, 1)
	m.mu.Lock()
	m.starting["k"] = make(chan struct{})
	m.startingOwner["k"] = "u1"
	_, err := m.admitLocked("u1")
	_, errOther := m.admitLocked("u2")
	m.mu.Unlock()
	if !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("err = %v, want ErrTooManySessions", err)
	}
	if errOther != nil {
		t.Fatalf("other owner must be admitted: %v", errOther)
	}
}

// TestAdmit_OwnerEvictionFreesGlobalSlot: replacing the owner's own LRU
// session doesn't grow the total, so it is admitted at a full global cap.
func TestAdmit_OwnerEvictionFreesGlobalSlot(t *testing.T) {
	m := lifecycleManager(t)
	m.SetSessionLimits(2, 1)
	seedSessions(m, "u1", 1, time.Now())
	seedSessions(m, "u2", 1, time.Now())
	m.mu.Lock()
	victim, err := m.admitLocked("u1")
	m.mu.Unlock()
	if err != nil || victim == nil {
		t.Fatalf("admit = (%v, %v), want replacement of u1-0", victim, err)
	}
}

// TestAdmit_GlobalCapUndoesEviction: even after evicting the owner's LRU the
// total is still at the cap (caps lowered at runtime) → refuse, and the
// would-be victim stays alive.
func TestAdmit_GlobalCapUndoesEviction(t *testing.T) {
	m := lifecycleManager(t)
	m.SetSessionLimits(1, 1)
	seedSessions(m, "u1", 1, time.Now())
	seedSessions(m, "u2", 1, time.Now())
	m.mu.Lock()
	_, err := m.admitLocked("u1")
	_, still := m.sess["u1-0"]
	m.mu.Unlock()
	if !errors.Is(err, ErrTooManySessions) || !still {
		t.Fatalf("err=%v still=%v, want refusal with victim kept", err, still)
	}
}

func TestHLSLimitFromEnv(t *testing.T) {
	t.Setenv("X_LIMIT", "")
	if got := hlsLimitFromEnv("X_LIMIT", 7); got != 7 {
		t.Errorf("unset = %d", got)
	}
	t.Setenv("X_LIMIT", "0")
	if got := hlsLimitFromEnv("X_LIMIT", 7); got != 0 {
		t.Errorf("0 = %d", got)
	}
	t.Setenv("X_LIMIT", "-3")
	if got := hlsLimitFromEnv("X_LIMIT", 7); got != 7 {
		t.Errorf("negative = %d", got)
	}
	t.Setenv("X_LIMIT", "12")
	if got := hlsLimitFromEnv("X_LIMIT", 7); got != 12 {
		t.Errorf("12 = %d", got)
	}
}
