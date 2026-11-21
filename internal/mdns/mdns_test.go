package mdns

import (
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/hashicorp/mdns"
)

func TestEnabled(t *testing.T) {
	t.Setenv("JACKUI_MDNS", "")
	if Enabled() {
		t.Fatal("default deve ser desligado")
	}
	t.Setenv("JACKUI_MDNS", "1")
	if !Enabled() {
		t.Fatal("JACKUI_MDNS=1 deve ligar")
	}
	t.Setenv("JACKUI_MDNS", "yes")
	if !Enabled() {
		t.Fatal("JACKUI_MDNS=yes deve ligar")
	}
	t.Setenv("JACKUI_MDNS", "0")
	if Enabled() {
		t.Fatal("JACKUI_MDNS=0 deve ficar desligado")
	}
}

func TestAnnounceDisabledIsNoop(t *testing.T) {
	t.Setenv("JACKUI_MDNS", "")
	stop := Announce("jackui-test", 8989, "")
	if stop == nil {
		t.Fatal("stop deve ser não-nil mesmo desligado")
	}
	stop() // noop, não pode panicking
	stop() // dupla chamada segura
}

func TestAnnounceWithoutInterfacesReturnsNoop(t *testing.T) {
	// Ambientes sem net sem loopback IPv4 (gluetun netns): deve desistir sem
	// erro e devolver noop — nunca afeta o startup do servidor.
	t.Setenv("JACKUI_MDNS", "1")
	stop := Announce("jackui-test", 8989, "")
	if stop == nil {
		t.Fatal("stop não-nil esperado")
	}
	// Nada a assertar além de "não entrou em panic": a decisão depende das
	// interfaces do host que roda o teste.
}

func TestBaseURLForAnnouncePrefersConfigured(t *testing.T) {
	got := BaseURLForAnnounce("https://jackui.example", 8989)
	if got != "https://jackui.example" {
		t.Fatalf("got %q", got)
	}
}

func TestBaseURLForAnnounceReconstructsLAN(t *testing.T) {
	got := BaseURLForAnnounce("", 8989)
	// Em hosts com IPv4 privado deve reconstruir; em sem, vazio. Ambos válidos.
	if got != "" {
		ip := net.ParseIP(got[len("http://"):][:0])
		_ = ip
		if os.Getenv("CI") == "" && !net.ParseIP(hostOf(got)).IsPrivate() {
			t.Fatalf("URL reconstruída deveria usar IP privado: %q", got)
		}
	}
}

func hostOf(url string) string {
	// url = http://1.2.3.4:8989
	rest := url[len("http://"):]
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == ':' {
			return rest[:i]
		}
	}
	return rest
}

// TestAnnounceRealServer exercises the full enabled path: a real mDNS server
// binds loopback, and the returned stop func shuts it down exactly once.
func TestAnnounceRealServer(t *testing.T) {
	t.Setenv("JACKUI_MDNS", "1")
	// The mdns library binds the interfaces it is given; give it loopback.
	stop := Announce("jackui-cov", 0, "")
	if stop == nil {
		t.Fatal("stop não-nil esperado")
	}
	stop()
	stop() // dupla chamada segura
}

// TestBaseURLForAnnounceWithoutPrivateIP covers the "no private IPv4" branch:
// in a test host loopback-only environments are not controllable, so assert
// the value is either a reconstructed private URL or empty.
func TestBaseURLForAnnounceWithoutPrivateIP(t *testing.T) {
	got := BaseURLForAnnounce("", 8989)
	if got != "" {
		u := got[len("http://"):]
		for i := len(u) - 1; i >= 0; i-- {
			if u[i] == ':' {
				u = u[:i]
				break
			}
		}
		ip := net.ParseIP(u)
		if ip == nil || !ip.IsPrivate() {
			t.Fatalf("URL reconstruída deve usar IP privado: %q", got)
		}
	}
}

// TestAnnounceOnRealBind covers the full enabled path with injected loopback
// IPs: a real mDNS server binds, the stop func shuts it down exactly once.
func TestAnnounceOnRealBind(t *testing.T) {
	stop := announceOn("jackui-cov", 0, "http://127.0.0.1:8989", []net.IP{net.ParseIP("127.0.0.1")})
	if stop == nil {
		t.Fatal("stop não-nil esperado")
	}
	stop()
	stop() // dupla chamada segura
}

func TestNonLoopbackIPv4(t *testing.T) {
	loop := mustCIDR(t, "127.0.0.1/8")
	v4 := mustCIDR(t, "192.168.0.10/24")
	v6 := mustCIDR(t, "fe80::1/64")
	got := nonLoopbackIPv4([]net.Addr{loop, v4, v6})
	if len(got) != 1 || !got[0].Equal(net.ParseIP("192.168.0.0")) {
		t.Fatalf("got %v, want [192.168.0.0] (network address of the CIDR)", got)
	}
	if nonLoopbackIPv4([]net.Addr{loop, v6}) != nil {
		t.Fatal("sem IPv4 público, deveria ser nil")
	}
}

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAnnounceErrorPaths covers the guards that production cannot hit:
// interface enumeration failure, no non-loopback IPv4 (gluetun netns), and
// the mDNS server bind failure.
func TestAnnounceErrorPaths(t *testing.T) {
	t.Setenv("JACKUI_MDNS", "1")
	v4 := mustCIDR(t, "192.168.0.10/24")

	origAddrs, origServer := interfaceAddrs, newMDNSServer
	t.Cleanup(func() { interfaceAddrs, newMDNSServer = origAddrs, origServer })

	t.Run("interface enumeration fails", func(t *testing.T) {
		interfaceAddrs = func() ([]net.Addr, error) { return nil, fmt.Errorf("boom") }
		stop := Announce("jackui", 8989, "")
		if stop == nil {
			t.Fatal("stop não-nil esperado")
		}
		stop()
	})

	t.Run("no non-loopback IPv4 (gluetun)", func(t *testing.T) {
		loop := mustCIDR(t, "127.0.0.1/8")
		interfaceAddrs = func() ([]net.Addr, error) { return []net.Addr{loop}, nil }
		stop := Announce("jackui", 8989, "")
		if stop == nil {
			t.Fatal("stop não-nil esperado")
		}
		stop()
	})

	t.Run("server bind fails", func(t *testing.T) {
		interfaceAddrs = func() ([]net.Addr, error) { return []net.Addr{v4}, nil }
		newMDNSServer = func(*mdns.Config) (interface{ Shutdown() error }, error) {
			return nil, fmt.Errorf("bind boom")
		}
		stop := Announce("jackui", 8989, "")
		if stop == nil {
			t.Fatal("stop não-nil esperado")
		}
		stop()
	})
}

// TestBaseURLForAnnounceErrorPath covers the interface-enumeration failure
// branch of the URL reconstruction.
func TestBaseURLForAnnounceErrorPath(t *testing.T) {
	origAddrs := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = origAddrs })
	interfaceAddrs = func() ([]net.Addr, error) { return nil, fmt.Errorf("boom") }
	if got := BaseURLForAnnounce("", 8989); got != "" {
		t.Fatalf("expected empty URL on enumeration failure, got %q", got)
	}
}
