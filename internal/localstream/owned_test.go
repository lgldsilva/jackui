package localstream

import (
	"io"
	"testing"
	"time"
)

// An owned session is an io.Closer: the HLS transcoder closes its source via
// closeIfCloser, which was a silent no-op for a bare *Session (fd leak on
// every duplicate playlist request and on every session stop).
func TestOwnedSessionIsCloserAndReleases(t *testing.T) {
	r := newRegistry(0, nil, false)
	f := newSpyFile(makeData(1000))
	s := r.OpenOwned("k", f, 1000)

	var _ io.ReadSeekCloser = s
	if got, ok := r.Get("k"); !ok || got != s.Session {
		t.Fatal("owned session must be visible for status lookup")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if !f.closed {
		t.Fatal("Close must close the file")
	}
	if _, ok := r.Get("k"); ok {
		t.Fatal("Close must unmap the slot")
	}
	_ = s.Close() // idempotent
}

// A duplicate owned session (GetOrStart found an existing HLS session and
// closes the newcomer) must hand the status slot back to the live one.
func TestOwnedDuplicateCloseRestoresLiveSlot(t *testing.T) {
	r := newRegistry(0, nil, false)
	live := r.OpenOwned("k", newSpyFile(makeData(1000)), 1000)
	dupFile := newSpyFile(makeData(1000))
	dup := r.OpenOwned("k", dupFile, 1000)

	if got, _ := r.Get("k"); got != dup.Session {
		t.Fatal("newest owned session takes the slot")
	}
	_ = dup.Close()
	if !dupFile.closed {
		t.Fatal("duplicate's file must be closed")
	}
	if got, ok := r.Get("k"); !ok || got != live.Session {
		t.Fatal("slot must fall back to the still-open owned session")
	}
}

// The idle reaper must not close a source its owner (a running ffmpeg) still
// holds: a VOD encode that reached EOF stops reading, and a later seek-restart
// would read a closed file.
func TestOwnedSessionNotReaped(t *testing.T) {
	clk := newClock()
	r := newRegistry(0, clk.now, false)
	f := newSpyFile(makeData(1 << 20))
	s := r.OpenOwned("k", f, 1<<20)
	if _, err := s.Read(make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	clk.advance(idleReapAfter + time.Minute)
	r.reap()
	if f.closed {
		t.Fatal("owned session must not be reaped")
	}
	if _, ok := r.Get("k"); !ok {
		t.Fatal("owned session must stay mapped")
	}
}

func TestRegistryCloseClosesOwned(t *testing.T) {
	r := newRegistry(0, nil, false)
	f1, f2 := newSpyFile(makeData(10)), newSpyFile(makeData(10))
	r.OpenOwned("k", f1, 10)
	r.OpenOwned("k", f2, 10) // shadowed in the slot — must still be closed
	r.Close()
	if !f1.closed || !f2.closed {
		t.Fatalf("Close must close every owned session (f1=%v f2=%v)", f1.closed, f2.closed)
	}
}
