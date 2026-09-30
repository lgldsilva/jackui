package downloads

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Regression tests for the atomic cross-filesystem move (hardening fix):
// a copy interrupted by kill -9 used to leave a TRUNCATED file at the final
// destination name, and the "dst exists ⇒ already done" shortcut then deleted
// the complete source and reported success — a `completed` row pointing at
// truncated media. The copy must go to dst+".partial", be fsynced, renamed, and
// only then may the source be removed; a size-mismatched dst must be recopied.

// partialCopySuffixPath is the scratch name the atomic copy writes before the
// final rename (kept in sync with moveFileProgress).
func partialCopySuffixPath(dst string) string { return dst + ".partial" }

// The boot-rescue scenario: the source is still in the cache and the
// destination holds a TRUNCATED file (a kill -9 mid-copy). moveDownloadedFile
// is what rescueInterruptedMove re-invokes; it must recopy the complete content
// and only then drop the source — never "already done" over a size mismatch.
func TestMoveDownloadedFile_TruncatedDstIsRecopied(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("A"), 100)
	src := filepath.Join(dataDir, "Pack", "file.mkv")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	// Simulate the truncated destination a crash mid-copy leaves behind.
	dst := filepath.Join(destDir, "file.mkv")
	if err := os.WriteFile(dst, bytes.Repeat([]byte("B"), 10), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := moveDownloadedFile(context.Background(), dataDir, destDir, "Pack/file.mkv", nil)
	if err != nil {
		t.Fatalf("moveDownloadedFile: %v", err)
	}
	if got != dst {
		t.Fatalf("returned path = %q, want %q", got, dst)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("destination holds a TRUNCATED copy (%d bytes, want %d) — the complete source was discarded over a partial copy", len(body), len(payload))
	}
	if fileExists(src) {
		t.Error("complete source must be removed only after the destination is complete")
	}
	if fileExists(partialCopySuffixPath(dst)) {
		t.Errorf("stale scratch copy %s must not survive the move", partialCopySuffixPath(dst))
	}
}

// The whole-torrent tree path (moveTreeEntry) must behave the same: a
// truncated destination is recopied, not treated as already moved.
func TestMoveTreeEntry_TruncatedDstIsRecopied(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("C"), 64)
	src := filepath.Join(dataDir, "Pack", "ep01.mkv")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(destDir, "ep01.mkv")
	if err := os.WriteFile(dst, bytes.Repeat([]byte("D"), 8), 0o644); err != nil {
		t.Fatal(err)
	}

	moved, err := moveTreeEntry(context.Background(), dataDir, destDir, "Pack", "Pack/ep01.mkv", nil)
	if err != nil {
		t.Fatalf("moveTreeEntry: %v", err)
	}
	if !moved {
		t.Fatal("moved = false, want true")
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("destination holds a TRUNCATED copy (%d bytes, want %d)", len(body), len(payload))
	}
	if fileExists(src) {
		t.Error("complete source must be removed only after the destination is complete")
	}
}

// Idempotency preserved: a destination whose size matches the source IS the
// "already moved" case — the source is dropped and the destination kept.
func TestMoveDownloadedFile_CompleteDstIsAlreadyMoved(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("E"), 50)
	src := filepath.Join(dataDir, "Pack", "file.mkv")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(destDir, "file.mkv")
	// Same size, older content: a previous attempt copied it fully and died
	// before removing the source.
	if err := os.WriteFile(dst, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := moveDownloadedFile(context.Background(), dataDir, destDir, "Pack/file.mkv", nil); err != nil {
		t.Fatalf("moveDownloadedFile: %v", err)
	}
	if fileExists(src) {
		t.Error("src should be removed on the idempotent already-moved path")
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("dst content changed on the already-moved path: %d bytes", len(body))
	}
}

// The cross-filesystem copy itself must be atomic: content lands via the
// scratch ".partial" file (fsynced + renamed), leaving no scratch file behind.
func TestMoveFileProgress_CopyIsAtomicViaPartial(t *testing.T) {
	orig := renameFn
	renameFn = func(string, string) error { return syscall.EXDEV }
	t.Cleanup(func() { renameFn = orig })

	dir := t.TempDir()
	payload := bytes.Repeat([]byte("F"), 100)
	src := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "b.bin")

	if err := moveFileProgress(context.Background(), src, dst, nil); err != nil {
		t.Fatalf("moveFileProgress: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("dst content mismatch: %d bytes, want %d", len(body), len(payload))
	}
	if fileExists(src) {
		t.Error("src must be removed after the copy completes")
	}
	if fileExists(partialCopySuffixPath(dst)) {
		t.Errorf("scratch copy %s must be consumed by the final rename", partialCopySuffixPath(dst))
	}
}

// A stale scratch file from a copy killed mid-flight must not wedge the next
// attempt: the move still completes and cleans it up.
func TestMoveFileProgress_StalePartialFromCrashIsRecovered(t *testing.T) {
	orig := renameFn
	renameFn = func(string, string) error { return syscall.EXDEV }
	t.Cleanup(func() { renameFn = orig })

	dir := t.TempDir()
	payload := bytes.Repeat([]byte("G"), 40)
	src := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "b.bin")
	// What a kill -9 mid-copy leaves with the atomic scheme: scratch file, no dst.
	if err := os.WriteFile(partialCopySuffixPath(dst), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := moveFileProgress(context.Background(), src, dst, nil); err != nil {
		t.Fatalf("moveFileProgress: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("dst content mismatch: %d bytes, want %d", len(body), len(payload))
	}
	if fileExists(partialCopySuffixPath(dst)) {
		t.Errorf("stale scratch copy %s must be overwritten/consumed", partialCopySuffixPath(dst))
	}
}

// A failed/aborted copy must leave the destination ABSENT (never truncated):
// with the scratch file the failed attempt can't touch the final name at all.
func TestMoveFileProgress_CopyFailureKeepsSrcAndDstAbsent(t *testing.T) {
	orig := renameFn
	renameFn = func(string, string) error { return syscall.EXDEV }
	t.Cleanup(func() { renameFn = orig })

	dir := t.TempDir()
	payload := bytes.Repeat([]byte("H"), 200)
	src := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "b.bin")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // abort the copy at the first read
	if err := moveFileProgress(ctx, src, dst, nil); err == nil {
		t.Fatal("expected the canceled copy to fail")
	}
	if fileExists(dst) {
		t.Error("a failed copy must not leave anything at the final destination name")
	}
	if fileExists(partialCopySuffixPath(dst)) {
		t.Errorf("failed copy must clean up its scratch file %s", partialCopySuffixPath(dst))
	}
	if !fileExists(src) {
		t.Error("source must survive a failed copy")
	}
}
