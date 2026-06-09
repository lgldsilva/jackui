package localstream

import (
	"io"
	"testing"
	"time"
)

func TestRegistryOpenSoloGetRelease(t *testing.T) {
	r := newRegistry(0, nil, false)
	f := newSpyFile(makeData(1000))
	s := r.OpenSolo("k", f, 1000)

	got, ok := r.Get("k")
	if !ok || got != s {
		t.Fatal("Get did not return the solo session")
	}
	r.Release("k", s)
	if !f.closed {
		t.Fatal("Release did not close the underlying file")
	}
	if _, ok := r.Get("k"); ok {
		t.Fatal("Release did not unmap the session")
	}
}

func TestRegistryOpenSharedReusesAndClosesDup(t *testing.T) {
	r := newRegistry(0, nil, false)
	f1 := newSpyFile(makeData(1000))
	f2 := newSpyFile(makeData(1000))
	s1 := r.OpenShared("k", f1, 1000)
	s2 := r.OpenShared("k", f2, 1000)

	if s1 != s2 {
		t.Fatal("OpenShared returned a different session for the same key")
	}
	if !f2.closed {
		t.Fatal("duplicate file handle was not closed on reuse")
	}
	if f1.closed {
		t.Fatal("the live file handle must stay open")
	}
}

func TestRegistryReleaseDoesNotEvictNewerSession(t *testing.T) {
	r := newRegistry(0, nil, false)
	old := r.OpenSolo("k", newSpyFile(makeData(10)), 10)
	newer := r.OpenSolo("k", newSpyFile(makeData(10)), 10)

	r.Release("k", old) // releasing the replaced session must keep the newer one
	got, ok := r.Get("k")
	if !ok || got != newer {
		t.Fatal("Release evicted the newer session")
	}
}

func TestRegistryReapsIdleSessions(t *testing.T) {
	clk := newClock()
	r := newRegistry(0, clk.now, false)
	f := newSpyFile(makeData(1 << 20))
	s := r.OpenShared("k", f, 1<<20)

	// One read so the session has a lastReadAt; otherwise idleFor == 0.
	if _, err := s.Read(make([]byte, 4096)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	clk.advance(idleReapAfter + time.Second)
	r.reap()

	if _, ok := r.Get("k"); ok {
		t.Fatal("idle session was not reaped")
	}
	if !f.closed {
		t.Fatal("reaped session did not close its file")
	}
}

func TestRegistryNeverReadIsNotReaped(t *testing.T) {
	clk := newClock()
	r := newRegistry(0, clk.now, false)
	r.OpenShared("k", newSpyFile(makeData(10)), 10)
	clk.advance(idleReapAfter + time.Hour)
	r.reap()
	if _, ok := r.Get("k"); !ok {
		t.Fatal("a session that never read should get a grace period, not be reaped")
	}
}

func TestRegistryCloseClosesAll(t *testing.T) {
	r := newRegistry(0, nil, false)
	f := newSpyFile(makeData(10))
	r.OpenShared("k", f, 10)
	r.Close()
	if !f.closed {
		t.Fatal("Close did not close live sessions")
	}
	if _, ok := r.Get("k"); ok {
		t.Fatal("Close did not clear the map")
	}
	r.Close() // idempotent
}

func TestNewRegistryStartsReaperAndCloses(t *testing.T) {
	r := NewRegistry(4) // exercises the real constructor + gcLoop goroutine
	if r.readaheadBytes != 4<<20 {
		t.Fatalf("readahead = %d want %d", r.readaheadBytes, 4<<20)
	}
	f := newSpyFile(makeData(10))
	r.OpenShared("k", f, 10)
	r.Close() // signals gcLoop to return and closes live sessions
	if !f.closed {
		t.Fatal("Close did not close the live session")
	}
}

func TestRegistryDefaultReadahead(t *testing.T) {
	r := newRegistry(0, nil, false)
	if r.readaheadBytes != defaultReadaheadBytes {
		t.Fatalf("default readahead = %d want %d", r.readaheadBytes, defaultReadaheadBytes)
	}
	r2 := newRegistry(8, nil, false)
	if r2.readaheadBytes != 8<<20 {
		t.Fatalf("readahead = %d want %d", r2.readaheadBytes, 8<<20)
	}
}

func TestRegistrySoloSessionsReadIndependently(t *testing.T) {
	r := newRegistry(0, nil, false)
	data := makeData(4096)
	a := r.OpenSolo("k", newSpyFile(data), int64(len(data)))
	b := r.OpenSolo("k", newSpyFile(data), int64(len(data)))

	// Independent cursors: seeking one must not disturb the other.
	if _, err := a.Seek(2048, io.SeekStart); err != nil {
		t.Fatalf("seek a: %v", err)
	}
	buf := make([]byte, 16)
	if _, err := b.Read(buf); err != nil {
		t.Fatalf("read b: %v", err)
	}
	if !equalBytes(buf, data[:16]) {
		t.Fatal("solo session b read from the wrong offset")
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
