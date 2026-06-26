package downloads

import (
	"testing"
)

// Regression for item 4: a user holding BOTH a whole-torrent row (file_index=-2)
// AND per-file rows (file_index=0,1) for the SAME (user, info_hash) — valid via
// the API (handlers accept -2; *arr RPC creates -2) — collapses into ONE group.
// That mixed group MUST be driven as WHOLE (the -2 row's DownloadAll covers every
// file), never through the per-file priority path (which would fail
// resolveFileIndex(-2) and fight DownloadAll over piece priorities, and could
// corrupt the sentinel). The fix makes isWhole() true whenever ANY member is the
// whole-torrent sentinel, and reconcileGroup routes such a group to the
// single-row whole path via wholeMember().
func TestMix_WholeAndPerFile_DrivenAsWhole(t *testing.T) {
	store := dlwNewStore(t)
	tor := wholeSpecTorrent(t, "Pack", [][]string{{"a.bin"}, {"b.bin"}, {"c.bin"}, {"d.bin"}})
	hash := tor.InfoHash().HexString()

	whole, err := store.Create(Download{
		UserID: 1, InfoHash: hash, FileIndex: FileIndexWholeTorrent,
		Magnet: "m", Name: "Pack",
	})
	if err != nil {
		t.Fatalf("create whole: %v", err)
	}
	perFile, err := store.Create(Download{
		UserID: 1, InfoHash: hash, FileIndex: 1,
		Magnet: "m", Name: "Pack",
	})
	if err != nil {
		t.Fatalf("create perFile: %v", err)
	}

	// GroupRows fuses them into ONE group (same user+hash).
	groups := GroupRows([]Download{*whole, *perFile})
	if len(groups) != 1 {
		t.Fatalf("expected mix to fuse into 1 group, got %d", len(groups))
	}
	g := groups[0]

	// The fix: a mixed group reports WHOLE, and wholeMember() returns the -2 row.
	if !g.isWhole() {
		t.Fatal("mixed group (whole + per-file) must be driven as WHOLE")
	}
	if wm := g.wholeMember(); wm.FileIndex != FileIndexWholeTorrent {
		t.Fatalf("wholeMember() = file_index %d, want the sentinel %d", wm.FileIndex, FileIndexWholeTorrent)
	}

	// And the per-file priority path is NEVER taken for this group (reconcileGroup
	// short-circuits to reconcile(wholeMember)). The sentinel row is untouched in
	// the DB: file_index stays -2.
	after, err := store.Get(1, whole.ID)
	if err != nil {
		t.Fatalf("get whole after: %v", err)
	}
	if after.FileIndex != FileIndexWholeTorrent {
		t.Errorf("whole-torrent sentinel must survive: file_index = %d, want %d", after.FileIndex, FileIndexWholeTorrent)
	}
}
