package downloads

import (
	"path/filepath"
	"testing"
)

func TestPromoteDir(t *testing.T) {
	cases := []struct {
		shared, category, want string
	}{
		{"/shared", "tv-sonarr", filepath.Join("/shared", "tv-sonarr")},
		{"/shared", "radarr", filepath.Join("/shared", "radarr")},
		{"/shared", "", "/shared"},                                  // no category → base only
		{"/shared", "a/b", filepath.Join("/shared", "a_b")},             // separators sanitized to one segment
		{"/shared", "../escape", filepath.Join("/shared", ".._escape")}, // traversal neutralized into one literal segment
	}
	for _, c := range cases {
		if got := PromoteDir(c.shared, c.category); got != c.want {
			t.Errorf("PromoteDir(%q,%q) = %q, want %q", c.shared, c.category, got, c.want)
		}
	}
}

// completionDest must route a finished *arr download into SharedDir/<category>
// (Transmission-style) only when auto-promote is on; everything else keeps the
// per-user downloadDir.
func TestCompletionDest_Routing(t *testing.T) {
	on := func() QueueSettings { return QueueSettings{MaxActive: 3, AutoPromoteArr: true} }
	off := func() QueueSettings { return QueueSettings{MaxActive: 3, AutoPromoteArr: false} }

	w := &Worker{downloadDir: "/dl", sharedDir: "/shared", settings: on}

	// *arr + auto-promote ON → shared/<category>/<torrent>
	if got := w.completionDest(Download{Source: SourceArr, Category: "tv-sonarr"}, "Show.S01"); got != filepath.Join("/shared", "tv-sonarr", "Show.S01") {
		t.Errorf("arr+on = %q", got)
	}

	// *arr but auto-promote OFF → plain downloadDir
	w.settings = off
	if got := w.completionDest(Download{Source: SourceArr, Category: "tv-sonarr"}, "Show.S01"); got != filepath.Join("/dl", "Show.S01") {
		t.Errorf("arr+off = %q", got)
	}

	// UI download (not *arr) + auto-promote ON → plain downloadDir (feature scoped to *arr)
	w.settings = on
	if got := w.completionDest(Download{Source: "", Category: "tv-sonarr"}, "Movie"); got != filepath.Join("/dl", "Movie") {
		t.Errorf("ui+on = %q", got)
	}

	// No SharedDir configured → falls back to downloadDir even for *arr
	w2 := &Worker{downloadDir: "/dl", sharedDir: "", settings: on}
	if got := w2.completionDest(Download{Source: SourceArr, Category: "radarr"}, "Film"); got != filepath.Join("/dl", "Film") {
		t.Errorf("arr+on+noshared = %q", got)
	}
}
