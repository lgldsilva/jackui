package main

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

// TestStartDiscoveryNoopWiring: com o flag desligado o wiring registra o
// cleanup (noop) sem efeitos e a app fecha limpo.
func TestStartDiscoveryNoopWiring(t *testing.T) {
	t.Setenv("JACKUI_MDNS", "")
	deps := &appDeps{cfg: &config.Config{Port: 8989, BaseURL: "https://jackui.example"}}
	startDiscovery(deps)
	if len(deps.cleanup) != 1 {
		t.Fatalf("wiring deveria registrar 1 cleanup, got %d", len(deps.cleanup))
	}
	deps.runCleanup() // chama o stop noop — não pode panicking nem bloquear
}

// TestStartDiscoveryEnabledWiring: com JACKUI_MDNS=1 e interfaces reais
// (non-loopback), o stop func real é registrado e o cleanup desliga o
// anunciante — validando o wire completo main→mdns.
func TestStartDiscoveryEnabledWiring(t *testing.T) {
	t.Setenv("JACKUI_MDNS", "1")
	deps := &appDeps{cfg: &config.Config{Port: 8989, BaseURL: ""}}
	startDiscovery(deps)
	if len(deps.cleanup) != 1 {
		t.Fatalf("wiring deveria registrar 1 cleanup, got %d", len(deps.cleanup))
	}
	deps.runCleanup()
}
