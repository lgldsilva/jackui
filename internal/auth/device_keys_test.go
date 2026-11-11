package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func newDeviceStore(t *testing.T) *Store {
	t.Helper()
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2)
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustDeviceKey(t *testing.T, s *Store, uid int, deviceID string) (string, int64) {
	t.Helper()
	k, id, err := s.CreateDeviceKey(uid, DeviceKeyMeta{DeviceID: deviceID, Name: "Sala", Model: "Shield", IP: "10.0.0.5"})
	if err != nil {
		t.Fatalf("CreateDeviceKey: %v", err)
	}
	return k, id
}

func TestDeviceKey_HashedPrefixedAndLookup(t *testing.T) {
	s := newDeviceStore(t)
	plain, id := mustDeviceKey(t, s, 1, "tv-1")
	if !strings.HasPrefix(plain, DeviceKeyPrefix) {
		t.Fatalf("key %q lacks prefix", plain)
	}
	var stored string
	if err := s.db.QueryRow("SELECT key_hash FROM device_keys WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == plain || stored != sha256Hex(plain) {
		t.Fatal("key must be stored as its SHA-256, never in clear")
	}
	k, err := s.LookupDeviceKey(plain)
	if err != nil || k.UserID != 1 || k.DeviceID != "tv-1" || k.Username != "testuser1" {
		t.Fatalf("lookup = %+v, %v", k, err)
	}
	for _, bad := range []string{"", "nope", DeviceKeyPrefix + "unknown"} {
		if _, err := s.LookupDeviceKey(bad); !errors.Is(err, ErrDeviceKeyInvalid) {
			t.Fatalf("LookupDeviceKey(%q) err = %v", bad, err)
		}
	}
	if err := s.TouchDeviceKey(id, "10.0.0.6"); err != nil {
		t.Fatal(err)
	}
	k, _ = s.LookupDeviceKey(plain)
	if k.LastUsedAt == nil || k.LastIP != "10.0.0.6" {
		t.Fatalf("touch not recorded: %+v", k)
	}
}

func TestDeviceKey_RepairReplacesSameUserOnly(t *testing.T) {
	s := newDeviceStore(t)
	old, _ := mustDeviceKey(t, s, 1, "tv-1")
	other, _ := mustDeviceKey(t, s, 2, "tv-1")
	fresh, _ := mustDeviceKey(t, s, 1, "tv-1")
	if _, err := s.LookupDeviceKey(old); err == nil {
		t.Fatal("re-pairing the same user must revoke the previous key")
	}
	for _, k := range []string{other, fresh} {
		if _, err := s.LookupDeviceKey(k); err != nil {
			t.Fatalf("key should stay valid: %v", err)
		}
	}
	profiles, err := s.DeviceProfiles("tv-1")
	if err != nil || len(profiles) != 2 {
		t.Fatalf("profiles = %+v, %v", profiles, err)
	}
	own, _ := s.ListDeviceKeys(1)
	all, _ := s.ListDeviceKeys(-1)
	if len(own) != 1 || len(all) != 2 {
		t.Fatalf("list own=%d all=%d", len(own), len(all))
	}
}

func TestDeviceKey_RevokeOwnership(t *testing.T) {
	s := newDeviceStore(t)
	plain, id := mustDeviceKey(t, s, 1, "tv-1")
	if err := s.RevokeDeviceKey(id, 2); !errors.Is(err, ErrDeviceKeyNotFound) {
		t.Fatalf("foreign revoke err = %v", err)
	}
	if err := s.RevokeDeviceKey(id, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupDeviceKey(plain); err == nil {
		t.Fatal("revoked key still resolves")
	}
	if err := s.RevokeDeviceKey(id, -1); !errors.Is(err, ErrDeviceKeyNotFound) {
		t.Fatalf("double revoke err = %v", err)
	}
	_, id2 := mustDeviceKey(t, s, 2, "tv-2")
	if err := s.RevokeDeviceKey(id2, -1); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
}

func TestDeviceKey_PINLifecycleAndLockout(t *testing.T) {
	s := newDeviceStore(t)
	plain, id := mustDeviceKey(t, s, 1, "tv-1")
	now := time.Now()
	k, _ := s.LookupDeviceKey(plain)
	if err := s.CheckDevicePIN(k, "", now); err != nil {
		t.Fatalf("no PIN set must pass: %v", err)
	}
	for _, bad := range []string{"12", "1234567", "12a4"} {
		if err := s.SetDevicePIN(id, 1, bad); !errors.Is(err, ErrDevicePINFormat) {
			t.Fatalf("SetDevicePIN(%q) err = %v", bad, err)
		}
	}
	if err := s.SetDevicePIN(id, 2, "1234"); !errors.Is(err, ErrDeviceKeyNotFound) {
		t.Fatalf("foreign PIN set err = %v", err)
	}
	if err := s.SetDevicePIN(id, 1, "1234"); err != nil {
		t.Fatal(err)
	}
	k, _ = s.LookupDeviceKey(plain)
	if !k.HasPIN {
		t.Fatal("HasPIN not reported")
	}
	if err := s.CheckDevicePIN(k, "", now); !errors.Is(err, ErrDevicePINRequired) {
		t.Fatalf("missing PIN err = %v", err)
	}
	if err := s.CheckDevicePIN(k, "1234", now); err != nil {
		t.Fatalf("right PIN: %v", err)
	}
	var err error
	for i := 0; i < devicePINMaxFailures; i++ {
		k, _ = s.LookupDeviceKey(plain)
		err = s.CheckDevicePIN(k, "9999", now)
	}
	if !errors.Is(err, ErrDevicePINLocked) {
		t.Fatalf("after %d misses err = %v, want locked", devicePINMaxFailures, err)
	}
	k, _ = s.LookupDeviceKey(plain)
	if err := s.CheckDevicePIN(k, "1234", now); !errors.Is(err, ErrDevicePINLocked) {
		t.Fatalf("locked profile accepted the right PIN: %v", err)
	}
	if err := s.CheckDevicePIN(k, "1234", now.Add(devicePINLockWindow+time.Second)); err != nil {
		t.Fatalf("lock must expire: %v", err)
	}
	if err := s.SetDevicePIN(id, 1, ""); err != nil {
		t.Fatal(err)
	}
	k, _ = s.LookupDeviceKey(plain)
	if k.HasPIN {
		t.Fatal("clearing the PIN failed")
	}
}
