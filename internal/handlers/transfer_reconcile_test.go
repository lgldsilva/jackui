package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// Boot reconcile: a persisted promote (interrupted by a restart) is re-submitted
// and the copy completes — the destination comes to exist and the pending is removed.
func Test_ReconcilePromote_ResumesCopy(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	pending, err := transfer.OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	tr := transfer.New()

	src := filepath.Join(t.TempDir(), "movie.mkv")
	dst := filepath.Join(t.TempDir(), "dest", "movie.mkv")
	if err := os.WriteFile(src, []byte("movie-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "movie.mkv", FilePath: src})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SetStatus(0, d.ID, downloads.StatusCompleted)

	payload, _ := json.Marshal(promotePayload{DownloadID: d.ID, UserID: 0, KeepSeeding: false})
	if _, err := pending.Add(transfer.Pending{Kind: "promote", Src: src, Dst: dst, Payload: string(payload)}); err != nil {
		t.Fatal(err)
	}

	ReconcilePendingTransfers(pending, tr, store, s)

	// The copy runs in the background (tr.Submit → goroutine). Waits for it to finish.
	moved := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dst); err == nil {
			moved = true
			break
		}
		<-time.After(2 * time.Millisecond) // yields the CPU to the copy goroutine
	}
	if !moved {
		t.Fatal("destination was not created by the reconcile")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should have been removed after the move")
	}
	// pending cleared + file_path updated.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if l, _ := pending.List(); len(l) == 0 {
			break
		}
		<-time.After(2 * time.Millisecond) // yields the CPU to the copy goroutine
	}
	if l, _ := pending.List(); len(l) != 0 {
		t.Errorf("pending should be empty, got %d", len(l))
	}
	if up, _ := store.Get(0, d.ID); up == nil || up.FilePath != dst {
		t.Errorf("file_path not updated: %+v", up)
	}
}

// Missing source + present destination = the copy had already completed before the
// crash: reconcile by re-pointing file_path and clear the pending (no re-copy).
func Test_ReconcilePromote_SrcGoneDstPresent(t *testing.T) {
	store := hgAStore(t)
	s := streamer.NewForTesting()
	pending, err := transfer.OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "done.mkv")
	if err := os.WriteFile(dst, []byte("ja-copiado"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{InfoHash: hgAValidHash, Magnet: MagnetPrefix + hgAValidHash, Name: "done.mkv", FilePath: "/gone/done.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SetStatus(0, d.ID, downloads.StatusCompleted)
	payload, _ := json.Marshal(promotePayload{DownloadID: d.ID, UserID: 0})
	_, _ = pending.Add(transfer.Pending{Kind: "promote", Src: "/gone/done.mkv", Dst: dst, Payload: string(payload)})

	ReconcilePendingTransfers(pending, transfer.New(), store, s)

	if l, _ := pending.List(); len(l) != 0 {
		t.Errorf("pending should be cleared, got %d", len(l))
	}
	if up, _ := store.Get(0, d.ID); up == nil || up.FilePath != dst {
		t.Errorf("file_path should point at the existing destination: %+v", up)
	}
}

// Unknown kind is dropped (does not stall the reconcile queue).
func Test_Reconcile_UnknownKindDropped(t *testing.T) {
	pending, err := transfer.OpenStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}

	_, _ = pending.Add(transfer.Pending{Kind: "bogus", Src: "a", Dst: "b"})
	ReconcilePendingTransfers(pending, transfer.New(), hgAStore(t), streamer.NewForTesting())
	if l, _ := pending.List(); len(l) != 0 {
		t.Errorf("unknown kind should be removed, got %d", len(l))
	}
}

func Test_shouldSerialize_Modes(t *testing.T) {
	// serial: forces sequential on any disk.
	if !shouldSerialize(transferModeSerial, "/anything/at/all") {
		t.Error("serial mode should serialize")
	}
	// parallel: never serializes (ignores HDD detection).
	if shouldSerialize(transferModeParallel, "/anything/at/all") {
		t.Error("parallel mode should not serialize")
	}
	// auto / "" : delegates to disk detection — nonexistent path → false.
	if shouldSerialize(transferModeAuto, "/no/such/path-xyz") {
		t.Error("auto on a nonexistent path should be false (non-rotational)")
	}
	if shouldSerialize("", "/no/such/path-xyz") {
		t.Error("empty = auto")
	}
}

func Test_transferMode_NilSafe(t *testing.T) {
	if transferMode(nil) != "" {
		t.Error("transferMode(nil) should be empty")
	}
}
