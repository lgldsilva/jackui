package downloads

import (
	"path/filepath"
	"testing"
)

func TestUserStats(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "dl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	mk := func(hash string, status string, bytes int64) {
		d, cerr := s.Create(Download{UserID: 1, InfoHash: hash, Magnet: "magnet:" + hash})
		if cerr != nil {
			t.Fatal(cerr)
		}
		if status != StatusQueued {
			if uerr := s.SetStatus(1, d.ID, status); uerr != nil {
				t.Fatal(uerr)
			}
		}
		if bytes > 0 {
			if perr := s.UpdateProgress(1, d.ID, bytes); perr != nil {
				t.Fatal(perr)
			}
		}
	}
	mk("h1", StatusCompleted, 1000)
	mk("h2", StatusDownloading, 300)
	mk("h3", StatusQueued, 0)

	total, completed, bytes, err := s.UserStats(1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || completed != 1 || bytes != 1300 {
		t.Fatalf("UserStats = (%d, %d, %d), want (3, 1, 1300)", total, completed, bytes)
	}

	// A user with no rows gets zeroes, not an error.
	total, completed, bytes, err = s.UserStats(99)
	if err != nil || total != 0 || completed != 0 || bytes != 0 {
		t.Fatalf("empty UserStats = (%d, %d, %d, %v)", total, completed, bytes, err)
	}
}
