package main

import (
	"strings"
	"testing"
)

// Single-instance guard (hardening fix): two jackui processes on one DataDir
// would corrupt the anacrolix bolt DB and the piece storage — neither is
// multi-process safe. Startup must take an exclusive non-blocking flock on a
// lockfile inside DataDir and fail fast when it is already held.

func TestAcquireInstanceLock_Exclusive(t *testing.T) {
	dir := t.TempDir()
	f, err := acquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("acquireInstanceLock: %v", err)
	}
	if f == nil {
		t.Fatal("expected a lock file handle for a non-empty DataDir")
	}

	// A second instance on the same DataDir must be refused, naming the cause.
	_, err = acquireInstanceLock(dir)
	if err == nil {
		t.Fatal("second acquire on the same DataDir must fail")
	}
	if !strings.Contains(err.Error(), "another jackui instance is running") {
		t.Fatalf("err = %v, want it to contain %q", err, "another jackui instance is running")
	}

	// Releasing the lock (process exit / close) frees it for the next boot.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f2, err := acquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = f2.Close()
}

func TestAcquireInstanceLock_EmptyDataDirIsNoop(t *testing.T) {
	// No DataDir (tests/CI configs without one) must not block anything.
	f, err := acquireInstanceLock("")
	if err != nil {
		t.Fatalf("empty DataDir must not error, got %v", err)
	}
	if f != nil {
		t.Fatal("empty DataDir must not return a lock handle")
	}
}
