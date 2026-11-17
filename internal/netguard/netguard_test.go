package netguard

import (
	"context"
	"net"
	"testing"
)

func TestIsBlockedIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1": true, "::1": true, "10.0.0.1": true, "172.16.0.1": true,
		"192.168.0.1": true, "169.254.169.254": true, "fe80::1": true,
		"fd00::1": true, "0.0.0.0": true,
		"8.8.8.8": false, "1.1.1.1": false, "198.51.100.1": false,
	}
	for s, want := range cases {
		if got := IsBlockedIP(net.ParseIP(s)); got != want {
			t.Errorf("IsBlockedIP(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestDialContextBlocksLoopback(t *testing.T) {
	if _, err := DialContext(context.Background(), "tcp", "127.0.0.1:80", ""); err == nil {
		t.Fatal("expected a loopback dial to be refused")
	}
}

func TestDialContextBadAddr(t *testing.T) {
	if _, err := DialContext(context.Background(), "tcp", "no-port", ""); err == nil {
		t.Fatal("expected an error for an address without a port")
	}
}

func TestDialContextUnresolvable(t *testing.T) {
	if _, err := DialContext(context.Background(), "tcp", "netguard.invalid.:80", ""); err == nil {
		t.Fatal("expected a lookup error for an unresolvable host")
	}
}

// Trust is scoped to the exact host:port — never the whole host.
func TestDialContextTrustedAddr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	conn, err := DialContext(context.Background(), "tcp", addr, addr)
	if err != nil {
		t.Fatalf("trusted dial should succeed: %v", err)
	}
	conn.Close()

	if _, err := DialContext(context.Background(), "tcp", addr, "127.0.0.1:9117"); err == nil {
		t.Fatal("another port on the trusted host must be refused")
	}
}

func TestNewTransportEnforcesGuard(t *testing.T) {
	tr := NewTransport("")
	if _, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("expected the transport dial to refuse loopback")
	}
}
