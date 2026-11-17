package auth

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// pinnedDeviceKey returns a store plus the plain key of a profile whose PIN is "1234".
func pinnedDeviceKey(t *testing.T) (*Store, string) {
	t.Helper()
	s := newDeviceStore(t)
	plain, id := mustDeviceKey(t, s, 1, "tv-1")
	if err := s.SetDevicePIN(id, 1, "1234"); err != nil {
		t.Fatal(err)
	}
	return s, plain
}

// Regression: the lockout used to be a read-modify-write on the snapshot loaded
// by LookupDeviceKey, so N parallel wrong guesses all read pin_failures=0, all
// wrote 1 and the lock never tripped.
func TestDeviceKey_PINLockoutHoldsUnderConcurrency(t *testing.T) {
	s, plain := pinnedDeviceKey(t)
	const attempts = 12
	now := time.Now()

	// Every request loads its snapshot before any of them verifies — the worst case.
	snaps := make([]*DeviceKey, attempts)
	for i := range snaps {
		k, err := s.LookupDeviceKey(plain)
		if err != nil {
			t.Fatal(err)
		}
		snaps[i] = k
	}
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range snaps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = s.CheckDevicePIN(snaps[i], "9999", now)
		}()
	}
	close(start)
	wg.Wait()

	invalid := 0
	for i, err := range errs {
		switch {
		case errors.Is(err, ErrDevicePINInvalid):
			invalid++
		case errors.Is(err, ErrDevicePINLocked):
		default:
			t.Fatalf("attempt %d: unexpected err %v", i, err)
		}
	}
	// The guess that trips the lock reports "locked", so at most max-1 report "invalid".
	if invalid > devicePINMaxFailures-1 {
		t.Fatalf("%d of %d concurrent wrong PINs were evaluated without locking (limit %d)",
			invalid, attempts, devicePINMaxFailures)
	}
	k, err := s.LookupDeviceKey(plain)
	if err != nil {
		t.Fatal(err)
	}
	if k.pinLocked == nil || !now.Before(*k.pinLocked) {
		t.Fatalf("profile not locked after %d concurrent wrong PINs (pin_failures=%d)", attempts, k.pinFails)
	}
	if err := s.CheckDevicePIN(k, "1234", now); !errors.Is(err, ErrDevicePINLocked) {
		t.Fatalf("locked profile accepted the right PIN: %v", err)
	}
}

// A stale snapshot (loaded before the lock was set) must not get its PIN compared.
func TestDeviceKey_PINStaleSnapshotCannotBypassLock(t *testing.T) {
	s, plain := pinnedDeviceKey(t)
	now := time.Now()
	stale, err := s.LookupDeviceKey(plain)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < devicePINMaxFailures; i++ {
		k, _ := s.LookupDeviceKey(plain)
		_ = s.CheckDevicePIN(k, "9999", now)
	}
	if err := s.CheckDevicePIN(stale, "1234", now); !errors.Is(err, ErrDevicePINLocked) {
		t.Fatalf("stale snapshot bypassed the lock: %v", err)
	}
}

// The right PIN on the last allowed attempt still unlocks and clears the counter.
func TestDeviceKey_PINRightOnLastAttemptResets(t *testing.T) {
	s, plain := pinnedDeviceKey(t)
	now := time.Now()
	for i := 0; i < devicePINMaxFailures-1; i++ {
		k, _ := s.LookupDeviceKey(plain)
		if err := s.CheckDevicePIN(k, "9999", now); !errors.Is(err, ErrDevicePINInvalid) {
			t.Fatalf("miss %d err = %v", i, err)
		}
	}
	k, _ := s.LookupDeviceKey(plain)
	if err := s.CheckDevicePIN(k, "1234", now); err != nil {
		t.Fatalf("right PIN on the last attempt: %v", err)
	}
	k, _ = s.LookupDeviceKey(plain)
	if k.pinFails != 0 || k.pinLocked != nil {
		t.Fatalf("success must clear the lockout state: fails=%d locked=%v", k.pinFails, k.pinLocked)
	}
}
