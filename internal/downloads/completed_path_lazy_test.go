package downloads

import (
	"context"
	"testing"
)

// relCounter is a relPath resolver that counts how often it is consulted.
type relCounter struct {
	rel   string
	calls int
}

func (r *relCounter) resolve() string {
	r.calls++
	return r.rel
}

// The stream endpoint resolves the completed path on EVERY Range request. The
// relative path (a metadata SELECT + decoding the torrent's whole file list)
// must only be computed when a whole-torrent row actually needs it.
func TestGetCompletedPathRelFunc_ResolvesRelPathOnlyForWholeRows(t *testing.T) {
	s := newTestStore(t)
	dest := t.TempDir()
	moved := mustWrite(t, dest, "Sub", "a.mkv")
	seedWholeCompleted(t, s, "lazywhole", "Pack", dest)

	single := mustWrite(t, t.TempDir(), "single.mkv")
	d, err := s.Create(context.Background(), Download{UserID: 1, InfoHash: "lazyfile", FileIndex: 0, Magnet: "m", Name: "single"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMetadata(context.Background(), 1, d.ID, "single", single, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(context.Background(), 1, d.ID, StatusCompleted); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		hash      string
		idx       int
		rel       string
		wantPath  string
		wantCalls int
	}{
		{"per-file row", "lazyfile", 0, "single.mkv", single, 0},
		{"no completed download", "lazynone", 0, "x.mkv", "", 0},
		{"negative index", "lazywhole", FileIndexAuto, "Pack/Sub/a.mkv", "", 0},
		{"whole-torrent row", "lazywhole", 0, "Pack/Sub/a.mkv", moved, 1},
		{"whole-torrent row, unknown rel path", "lazywhole", 0, "", "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := &relCounter{rel: tc.rel}
			got, err := s.GetCompletedPathRelFunc(context.Background(), tc.hash, tc.idx, rc.resolve, 1)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.wantPath {
				t.Errorf("path = %q, want %q", got, tc.wantPath)
			}
			if rc.calls != tc.wantCalls {
				t.Errorf("relPath resolved %d time(s), want %d", rc.calls, tc.wantCalls)
			}
		})
	}

	// Trusted internal lookup (userID < 0) takes the same lazy route.
	rc := &relCounter{rel: "Pack/Sub/a.mkv"}
	if got, err := s.GetCompletedPathRelFunc(context.Background(), "lazywhole", 0, rc.resolve, -1); err != nil || got != moved || rc.calls != 1 {
		t.Errorf("any-owner lookup = %q, %v, calls=%d; want %q, nil, 1", got, err, rc.calls, moved)
	}
	// Another user's whole row is invisible, so nothing is resolved for them.
	rc = &relCounter{rel: "Pack/Sub/a.mkv"}
	if got, err := s.GetCompletedPathRelFunc(context.Background(), "lazywhole", 0, rc.resolve, 2); err != nil || got != "" || rc.calls != 0 {
		t.Errorf("other user = %q, %v, calls=%d; want empty, nil, 0", got, err, rc.calls)
	}
}

func TestGetCompletedPathRelFunc_NilStoreAndDBError(t *testing.T) {
	var none *Store
	rc := &relCounter{rel: "a"}
	if got, err := none.GetCompletedPathRelFunc(context.Background(), "h", 0, rc.resolve, 1); got != "" || err != nil || rc.calls != 0 {
		t.Fatalf("nil store = %q, %v, calls=%d", got, err, rc.calls)
	}

	s := newTestStore(t)
	_ = s.db.Close()
	if _, err := s.GetCompletedPathRelFunc(context.Background(), "h", 0, rc.resolve, 1); err == nil {
		t.Fatal("closed pool must surface the error")
	}
}
