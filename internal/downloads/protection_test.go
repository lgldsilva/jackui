package downloads

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

var protHash = metainfo.Hash{0xab}

// TestReleaseProtectionKeptForOtherUsersRow: user A's row finishing must not
// drop the protection while user B's row of the same torrent is tracked.
func TestReleaseProtectionKeptForOtherUsersRow(t *testing.T) {
	w := genTestWorker(t)
	w.streamer.RegisterDownload("Pack")
	w.mu.Lock()
	w.tracked[2] = &trackedDL{id: 2, name: "Pack", hash: protHash} // user B
	w.mu.Unlock()

	w.releaseProtection("Pack", protHash, 1) // user A's row #1 completed
	if !w.streamer.IsDownloadProtected("Pack") {
		t.Fatal("protection dropped while another row still downloads the torrent")
	}

	w.mu.Lock()
	delete(w.tracked, 2)
	w.mu.Unlock()
	w.releaseProtection("Pack", protHash, 1)
	if w.streamer.IsDownloadProtected("Pack") {
		t.Fatal("protection must be released once no row needs it")
	}
}

func TestReleaseProtectionKeptForPendingInit(t *testing.T) {
	w := genTestWorker(t)
	w.streamer.RegisterDownload("Pack")
	w.mu.Lock()
	w.setPendingLocked(5, protHash.HexString(), func() {})
	w.mu.Unlock()
	w.releaseProtection("Pack", protHash, 1)
	if !w.streamer.IsDownloadProtected("Pack") {
		t.Fatal("an init in flight for the same hash still needs the protection")
	}
}

// TestReseedAfterCompletionSkipsSharedTorrent: the post-completion drop must
// not tear down a torrent another row is still downloading.
func TestReseedAfterCompletionSkipsSharedTorrent(t *testing.T) {
	w := genTestWorker(t)
	drops := 0
	w.drop = func(metainfo.Hash) { drops++ }
	w.mu.Lock()
	w.tracked[2] = &trackedDL{id: 2, name: "Pack", hash: protHash}
	w.mu.Unlock()

	w.reseedAfterCompletion(Download{ID: 1, InfoHash: protHash.HexString(), Name: "Pack"}, false)
	if drops != 0 {
		t.Fatalf("drops = %d, want 0 while row #2 still downloads", drops)
	}

	w.mu.Lock()
	delete(w.tracked, 2)
	w.mu.Unlock()
	w.reseedAfterCompletion(Download{ID: 1, InfoHash: protHash.HexString(), Name: "Pack"}, false)
	if drops != 1 {
		t.Fatalf("drops = %d, want 1 once nothing else uses the torrent", drops)
	}
}
