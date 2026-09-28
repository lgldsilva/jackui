package streamer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// ─── Regressão do incidente de 2026-09-28 ("Parar" não parava) ──────────────
//
// Produção: o torrent-get da stack *arr (poll a cada ~60s) renovava o
// lastAccess de cada torrent ativo via Get(); o guard activeReadGuard (60s)
// do drop ficava eternamente armado e qualquer "Parar" era recusado em
// silêncio (6× DELETE → 6× 200, torrent vivo). O fix: leitura de monitoramento
// (GetUntouched) não conta como uso, e o caminho EXPLÍCITO (DropSeed) bypassa
// o guard de leitura — recusando apenas com viewer lease / download ativo.

// Get() continua contando como uso (leitura real: player, resolve de info).
func TestGet_BumpsLastAccess(t *testing.T) {
	s := NewForTesting()
	h, cleanup := s.SeedActiveForTesting("guard-bump", time.Now().Add(-2*time.Hour))
	defer cleanup()

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
	h, cleanup := s.SeedActiveForTesting("monitor-poll", stale)
	defer cleanup()

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
	h, cleanup := s.SeedActiveForTesting("lifecycle-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

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
	h, cleanup := s.SeedActiveForTesting("incident-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

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
	h, cleanup := s.SeedActiveForTesting("being-watched", time.Now().Add(-2*time.Hour))
	defer cleanup()

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
	h, cleanup := s.SeedActiveForTesting("background-dl", time.Now().Add(-2*time.Hour))
	defer cleanup()

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
	h, cleanup := s.SeedActiveForTesting("idle-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	s.Drop(h)

	s.mu.Lock()
	_, stillActive := s.active[h]
	s.mu.Unlock()
	if stillActive {
		t.Fatal("Drop em torrent ocioso (> 60s) deveria remover o torrent")
	}
}

// GetUntouched em hash que já saiu do conjunto ativo responde o mesmo erro do
// Get — o poll da *arr precisa distinguir "sumiu" de snapshot válida.
func TestGetUntouched_UnknownHash(t *testing.T) {
	s := NewForTesting()
	info, err := s.GetUntouched(metainfo.Hash{0xCC})
	if !errors.Is(err, errTorrentGone) {
		t.Fatalf("GetUntouched em hash desconhecido = (%v, %v), want errTorrentGone", info, err)
	}
	if info != nil {
		t.Fatalf("GetUntouched em hash desconhecido devolveu snapshot: %+v", info)
	}
	if _, err := s.Get(metainfo.Hash{0xCC}); !errors.Is(err, errTorrentGone) {
		t.Fatalf("Get em hash desconhecido = %v, want errTorrentGone (mesmo contrato do GetUntouched)", err)
	}
}

// IsDropRefusal é o contrato que o handler usa para escolher 409 vs 200:
// só viewer lease e download em background são recusa; ErrTorrentNotActive é
// sucesso idempotente e erros genéricos (mesmo embrulhados) não são recusa.
func TestIsDropRefusal_Classification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"viewer lease", ErrTorrentViewerActive, true},
		{"download protegido", ErrTorrentDownloadProtected, true},
		{"viewer lease embrulhado", fmt.Errorf("drop %s: %w", "abc", ErrTorrentViewerActive), true},
		{"not active = idempotente", ErrTorrentNotActive, false},
		{"leitura recente (guard interno)", errRecentlyRead, false},
		{"genérico", errors.New("qualquer erro"), false},
	}
	for _, tc := range cases {
		if got := IsDropRefusal(tc.err); got != tc.want {
			t.Errorf("IsDropRefusal(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// mustFixture é o único ponto de falha do fixture SeedActiveForTesting: nil
// passa em silêncio; erro vira panic com o passo identificado, para o teste
// que usa o fixture morrer no lugar certo e não numa asserção downstream.
func TestMustFixture(t *testing.T) {
	mustFixture("noop", nil)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("mustFixture com erro deveria dar panic")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "SeedActiveForTesting: passo-x: boom") {
			t.Fatalf("panic = %v, want passo + causa na mensagem", r)
		}
	}()
	mustFixture("passo-x", errors.New("boom"))
}
