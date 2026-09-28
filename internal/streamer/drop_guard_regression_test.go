package streamer

import (
	"errors"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// ─── Regressão do incidente de 2026-09-28 ("Parar" não parava) ──────────────
//
// Produção: o torrent-get da stack *arr (poll a cada ~60s) renovava o
// lastAccess de TODOS os torrents ativos via Get(); o guard activeReadGuard
// (60s) do drop ficava eternamente armado e todo "Parar" era recusado em
// silêncio (6× DELETE → 6× 200, torrent vivo). O fix: leitura de monitoramento
// (GetUntouched) não conta como uso, e o caminho EXPLÍCITO (DropSeed) bypassa
// o guard de leitura — recusando apenas com viewer lease / download ativo.

// Get() continua contando como uso (leitura real: player, resolve de info).
func TestGet_BumpsLastAccess(t *testing.T) {
	s := NewForTesting()
	h, close := s.SeedActiveForTesting("guard-bump", time.Now().Add(-2*time.Hour))
	defer close()

	s.mu.Lock()
	before := s.active[h].lastAccess
	s.mu.Unlock()
	if time.Since(before) < time.Hour {
		t.Fatalf("setup: entrada deveria estar stale (lastAccess=%s)", before)
	}

	if _, err := s.Get(h); err != nil {
		t.Fatalf("Get: %v", err)
	}

	s.mu.Lock()
	after := s.active[h].lastAccess
	s.mu.Unlock()
	if time.Since(after) > time.Minute {
		t.Fatalf("Get não fez bump no lastAccess: antes=%s depois=%s", before, after)
	}
}

// GetUntouched() é o caminho de MONITORAMENTO: mesma snapshot, sem bump.
func TestGetUntouched_DoesNotBumpLastAccess(t *testing.T) {
	s := NewForTesting()
	stale := time.Now().Add(-2 * time.Hour)
	h, close := s.SeedActiveForTesting("monitor-poll", stale)
	defer close()

	info, err := s.GetUntouched(h)
	if err != nil {
		t.Fatalf("GetUntouched: %v", err)
	}
	if info == nil || info.Name != "monitor-poll" {
		t.Fatalf("GetUntouched retornou snapshot inválida: %+v", info)
	}

	s.mu.Lock()
	after := s.active[h].lastAccess
	s.mu.Unlock()
	if !after.Equal(stale) {
		t.Fatalf("GetUntouched renovou o lastAccess: %s (want %s)", after, stale)
	}
}

// O drop GENÉRICO (idle reaper, health probe, teardown de ciclo de vida)
// permanece protegido pelo activeReadGuard: recusa silenciosa com leitura
// recente. O poll da *arr hoje usa GetUntouched, mas uma leitura REAL
// (player/HLS) ainda deve travar esse caminho.
func TestDrop_GenericPath_StillGuarded_AfterRecentRead(t *testing.T) {
	s := NewForTesting()
	h, close := s.SeedActiveForTesting("lifecycle-torrent", time.Now().Add(-2*time.Hour))
	defer close()

	if _, err := s.Get(h); err != nil { // leitura real
		t.Fatalf("Get: %v", err)
	}

	s.Drop(h) // recusa silenciosa intencional neste caminho

	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if !stillActive {
		t.Fatal("Drop genérico deveria ter sido recusado (leitura real < 60s)")
	}
}

// FIX do incidente: o "Parar" (caminho explícito, DropSeed) funciona MESMO
// segundos depois de um Get de monitoramento — o guard de leitura não se
// aplica a ação explícita do usuário.
func TestDropSeed_BypassesReadGuard_AfterMonitoringGet(t *testing.T) {
	s := NewForTesting()
	h, close := s.SeedActiveForTesting("incident-torrent", time.Now().Add(-2*time.Hour))
	defer close()

	if _, err := s.Get(h); err != nil { // era isso que armava o guard no incidente
		t.Fatalf("Get (poll *arr): %v", err)
	}

	if err := s.DropSeed(h); err != nil {
		t.Fatalf("DropSeed após poll de monitoramento: %v (ação explícita deve vencer o guard de leitura)", err)
	}

	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if stillActive {
		t.Fatal("DropSeed deveria ter removido o torrent mesmo com Get < 60s atrás")
	}
}

// O que NÃO o explicit vence: viewer lease ativo (alguém assistindo).
func TestDropSeed_StillRefused_WhileViewerLeaseActive(t *testing.T) {
	s := NewForTesting()
	h, close := s.SeedActiveForTesting("being-watched", time.Now().Add(-2*time.Hour))
	defer close()

	s.AcquireViewer(h)
	err := s.DropSeed(h)
	if !errors.Is(err, ErrTorrentViewerActive) {
		t.Fatalf("DropSeed = %v, want ErrTorrentViewerActive", err)
	}
	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if !stillActive {
		t.Fatal("torrent com viewer lease NÃO pode ser dropado")
	}

	// Último viewer sai → agora o "Parar" funciona.
	s.ReleaseViewer(h)
	if err := s.DropSeed(h); err != nil {
		t.Fatalf("DropSeed após ReleaseViewer: %v", err)
	}
}

// ...e download em background registrado (proteção do worker de downloads).
func TestDropSeed_StillRefused_WhileBackgroundDownloadActive(t *testing.T) {
	s := NewForTesting()
	h, close := s.SeedActiveForTesting("background-dl", time.Now().Add(-2*time.Hour))
	defer close()

	name := "background-dl"
	s.RegisterDownload(name)
	if err := s.DropSeed(h); !errors.Is(err, ErrTorrentDownloadProtected) {
		t.Fatalf("DropSeed = %v, want ErrTorrentDownloadProtected", err)
	}

	s.UnregisterDownload(name)
	if err := s.DropSeed(h); err != nil {
		t.Fatalf("DropSeed após UnregisterDownload: %v", err)
	}
}

// DropSeed em hash inexistente é idempotente e honesto: ErrTorrentNotActive.
func TestDropSeed_IdempotentOnUnknownHash(t *testing.T) {
	s := NewForTesting()
	if err := s.DropSeed(metainfo.Hash{0xAA}); !errors.Is(err, ErrTorrentNotActive) {
		t.Fatalf("DropSeed em hash desconhecido = %v, want ErrTorrentNotActive", err)
	}
}

// Drop genérico em hash inexistente permanece no-op silencioso.
func TestDrop_NoopOnUnknownHash(t *testing.T) {
	s := NewForTesting()
	s.Drop(metainfo.Hash{0xBB}) // não pode panicking
}

// Controle: drop genérico em torrent ocioso (> 60s) remove — o guard de
// leitura só protege a janela recente.
func TestDrop_Succeeds_WhenIdleBeyondGuard(t *testing.T) {
	s := NewForTesting()
	h, close := s.SeedActiveForTesting("idle-torrent", time.Now().Add(-2*time.Hour))
	defer close()

	s.Drop(h)

	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if stillActive {
		t.Fatal("Drop em torrent ocioso (> 60s) deveria remover o torrent")
	}
}
