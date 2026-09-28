package transmissionrpc

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── Regressão do incidente de 2026-09-28 (ângulo *arr) ─────────────────────
//
// O torrent-get (poll da stack *arr a cada ~60s) usava streamer.Get, que
// renovava o lastAccess de todos os torrents ativos — o activeReadGuard do
// drop ficava eternamente armado e todo "Parar" manual era recusado em
// silêncio. Agora o poll usa GetUntouched (não conta como uso) e o caminho
// explícito (DropSeed) bypassa o guard de leitura de qualquer forma.

// O poll da *arr continua ENXERGANDO o torrent — e não bloqueia mais o stop
// manual que vier logo em seguida.
func TestTorrentGet_PollSeesTorrent_AndNoLongerBlocksExplicitStop(t *testing.T) {
	s := streamer.NewForTesting()
	h := NewHandler(nil, s, nil, "/data", "/data", "", nil)

	hash, close := s.SeedActiveForTesting("arr-polled-torrent", time.Now().Add(-2*time.Hour))
	defer close()

	// O caminho exato que o torrent-get da *arr executa a cada minuto.
	active := h.activeTorrentInfo([]downloads.Download{
		{ID: 372, InfoHash: hash.HexString()},
	})
	if _, ok := active[hash.HexString()]; !ok {
		t.Fatal("activeTorrentInfo deveria resolver o torrent ativo (é assim que a *arr o vê)")
	}

	// Imediatamente depois do poll, o usuário clica em "Parar".
	if err := s.DropSeed(hash); err != nil {
		t.Fatalf("DropSeed após torrent-get da *arr: %v (poll não deve mais armar o guard)", err)
	}
	if n := len(s.ActiveList()); n != 0 {
		t.Fatalf("ActiveList = %d, want 0 — o stop explícito deve vencer", n)
	}
}

// Controle: sem poll nenhum, o stop sempre funcionou.
func TestTorrentGet_WithoutPoll_UserDropSucceeds(t *testing.T) {
	s := streamer.NewForTesting()
	_ = NewHandler(nil, s, nil, "/data", "/data", "", nil)

	hash, close := s.SeedActiveForTesting("unpolled-torrent", time.Now().Add(-2*time.Hour))
	defer close()

	if err := s.DropSeed(hash); err != nil {
		t.Fatalf("DropSeed: %v", err)
	}
	if n := len(s.ActiveList()); n != 0 {
		t.Fatalf("ActiveList = %d, want 0", n)
	}
}

// auto-seed persistido tem que ir junto (o Drop genérico deixava
// o registro vivo → resumeSeeding ressuscitava o torrent no boot). Aqui
// apenas a sanidade do fixture: o hash derivado resolve no streamer.
func TestSeedActiveForTesting_HashMatchesTorrent(t *testing.T) {
	s := streamer.NewForTesting()
	hash, close := s.SeedActiveForTesting("hash-check", time.Now())
	defer close()

	var zero metainfo.Hash
	if hash == zero {
		t.Fatal("SeedActiveForTesting retornou hash zerado")
	}
	if _, err := s.Get(hash); err != nil {
		t.Fatalf("Get no torrent semeado: %v", err)
	}
}
