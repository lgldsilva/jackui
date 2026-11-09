package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestFlow() (*DeviceFlow, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	f := NewDeviceFlow()
	f.now = clk.now
	return f, clk
}

func startTV(t *testing.T, f *DeviceFlow, ip string) (string, *DevicePairing) {
	t.Helper()
	code, p, err := f.Start(DeviceKeyMeta{DeviceID: "tv-1", Name: "Sala", Model: "Shield", IP: ip})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return code, p
}

func TestDeviceFlow_UserCodeShape(t *testing.T) {
	f, _ := newTestFlow()
	_, p := startTV(t, f, "10.0.0.1")
	if len(p.UserCode) != userCodeLen {
		t.Fatalf("len = %d", len(p.UserCode))
	}
	for _, r := range p.UserCode {
		if !strings.ContainsRune(userCodeAlphabet, r) {
			t.Fatalf("char %q outside the unambiguous alphabet", r)
		}
	}
	if got := NormalizeUserCode(" " + strings.ToLower(FormatUserCode(p.UserCode))); got != p.UserCode {
		t.Fatalf("normalize round-trip = %q, want %q", got, p.UserCode)
	}
	if FormatUserCode("ABC") != "ABC" {
		t.Fatal("short code must be returned unchanged")
	}
}

func TestDeviceFlow_PendingSlowDownApprovedOnce(t *testing.T) {
	f, clk := newTestFlow()
	code, p := startTV(t, f, "10.0.0.1")

	if got := f.Poll(code).Err; got != DeviceErrPending {
		t.Fatalf("first poll = %q, want pending", got)
	}
	if got := f.Poll(code).Err; got != DeviceErrSlowDown {
		t.Fatalf("fast poll = %q, want slow_down", got)
	}
	clk.advance(DeviceFlowInterval)
	if got := f.Poll(code).Err; got != DeviceErrPending {
		t.Fatalf("poll after interval = %q, want pending", got)
	}
	if _, err := f.Decide(FormatUserCode(p.UserCode), 7, true); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if _, err := f.Decide(p.UserCode, 8, true); !errors.Is(err, ErrDeviceCodeNotFound) {
		t.Fatalf("second decision must fail, got %v", err)
	}
	res := f.Poll(code)
	if res.Err != "" || res.UserID != 7 || res.Pairing.DeviceID != "tv-1" {
		t.Fatalf("approved poll = %+v", res)
	}
	if got := f.Poll(code).Err; got != DeviceErrExpired {
		t.Fatalf("approval must be delivered once; got %q", got)
	}
}

func TestDeviceFlow_Denied(t *testing.T) {
	f, _ := newTestFlow()
	code, p := startTV(t, f, "10.0.0.1")
	if _, err := f.Decide(p.UserCode, 7, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Lookup(p.UserCode); err == nil {
		t.Fatal("a decided pairing must not be pending anymore")
	}
	if got := f.Poll(code).Err; got != DeviceErrDenied {
		t.Fatalf("got %q, want access_denied", got)
	}
	if got := f.Poll(code).Err; got != DeviceErrExpired {
		t.Fatalf("denial is terminal; got %q", got)
	}
}

func TestDeviceFlow_Expiry(t *testing.T) {
	f, clk := newTestFlow()
	code, p := startTV(t, f, "10.0.0.1")
	clk.advance(DeviceFlowTTL)
	if _, err := f.Lookup(p.UserCode); !errors.Is(err, ErrDeviceCodeNotFound) {
		t.Fatalf("expired lookup err = %v", err)
	}
	if _, err := f.Decide(p.UserCode, 1, true); !errors.Is(err, ErrDeviceCodeNotFound) {
		t.Fatalf("expired decide err = %v", err)
	}
	if got := f.Poll(code).Err; got != DeviceErrExpired {
		t.Fatalf("got %q", got)
	}
	if got := f.Poll("never-issued").Err; got != DeviceErrExpired {
		t.Fatalf("unknown code = %q", got)
	}
}

func TestDeviceFlow_PerIPCapAndGC(t *testing.T) {
	f, clk := newTestFlow()
	for i := 0; i < devicePendingPerIP; i++ {
		startTV(t, f, "10.0.0.9")
	}
	if _, _, err := f.Start(DeviceKeyMeta{DeviceID: "x", IP: "10.0.0.9"}); !errors.Is(err, ErrDeviceFlowFull) {
		t.Fatalf("cap per IP not enforced: %v", err)
	}
	startTV(t, f, "10.0.0.10") // another IP is unaffected
	clk.advance(DeviceFlowTTL)
	startTV(t, f, "10.0.0.9") // expired entries are collected → room again
	if len(f.byCode) != 1 || len(f.byUser) != 1 {
		t.Fatalf("gc left %d/%d entries", len(f.byCode), len(f.byUser))
	}
}

func TestDeviceFlow_LookupReturnsCopy(t *testing.T) {
	f, _ := newTestFlow()
	_, p := startTV(t, f, "10.0.0.1")
	got, err := f.Lookup(strings.ToLower(p.UserCode))
	if err != nil {
		t.Fatal(err)
	}
	got.Name = "mutated"
	again, _ := f.Lookup(p.UserCode)
	if again.Name != "Sala" || again.IP != "10.0.0.1" || again.Model != "Shield" {
		t.Fatalf("lookup leaked internal state: %+v", again)
	}
}
