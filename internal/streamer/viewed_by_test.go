package streamer

import (
	"testing"
	"time"
)

func TestViewedBy(t *testing.T) {
	s := NewForTesting()
	h, cleanup := s.SeedActiveForTesting("viewed-by", time.Now())
	defer cleanup()

	if s.ViewedBy(h, "u1") {
		t.Fatal("no lease yet → false")
	}
	s.AcquireViewerFor(h, "u1:tab-a")
	if !s.ViewedBy(h, "u1") {
		t.Fatal("u1 holds a lease → true")
	}
	// Prefix must stop at the ':' separator: u1 must not match u12.
	if s.ViewedBy(h, "u12") || s.ViewedBy(h, "u2") || s.ViewedBy(h, "") {
		t.Fatal("other users must not match")
	}
	s.ReleaseViewerFor(h, "u1:tab-a")
	if s.ViewedBy(h, "u1") {
		t.Fatal("released lease → false")
	}

	// A stale lease (past the TTL) no longer authorizes.
	s.AcquireViewerFor(h, "u1:tab-b")
	s.cfg.ViewerLeaseTTL = time.Nanosecond
	time.Sleep(time.Millisecond)
	if s.ViewedBy(h, "u1") {
		t.Fatal("stale lease → false")
	}
	var nilS *Streamer
	if nilS.ViewedBy(h, "u1") {
		t.Fatal("nil streamer → false")
	}
}
