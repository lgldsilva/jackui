package mdns

import (
	"net"
	"os"
	"testing"
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
