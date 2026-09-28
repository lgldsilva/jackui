package transmissionrpc

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── Regressão do incidente de 2026-09-28 (ângulo *arr) ─────────────────────
//
// O torrent-get (poll da stack *arr a cada ~60s) usava streamer.Get, que
// renovava o lastAccess de cada torrent ativo — o activeReadGuard do drop
// ficava eternamente armado e qualquer "Parar" manual era recusado em
// silêncio. Agora o poll usa GetUntouched (não conta como uso) e o caminho
// explícito (DropSeed) bypassa o guard de leitura de qualquer forma.

// O poll da *arr continua ENXERGANDO o torrent — e não bloqueia mais o stop
// manual que vier logo em seguida.
func TestTorrentGet_PollSeesTorrent_AndNoLongerBlocksExplicitStop(t *testing.T) {
	s := streamer.NewForTesting()
	h := NewHandler(nil, s, nil, "/data", "/data", "", nil)

	hash, cleanup := s.SeedActiveForTesting("arr-polled-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

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

	hash, cleanup := s.SeedActiveForTesting("unpolled-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

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
	hash, cleanup := s.SeedActiveForTesting("hash-check", time.Now())
	defer cleanup()

	var zero metainfo.Hash
	if hash == zero {
		t.Fatal("SeedActiveForTesting retornou hash zerado")
	}
	if _, err := s.Get(hash); err != nil {
		t.Fatalf("Get no torrent semeado: %v", err)
	}
}

// torrent-remove com delete-local-data vindo da *arr é remoção EXPLÍCITA:
// mesmo com o guard de leitura armado por um poll recente, o torrent sai do
// streamer e a row some da fila — o mesmo caminho do "Parar" na UI.
func TestTorrentRemove_DeleteLocalData_DropsActiveTorrentAfterPoll(t *testing.T) {
	st := newTestStore(t)
	s := streamer.NewForTesting()
	h := NewHandler(st, s, nil, "/data", "/data", "", nil)
	gin.SetMode(gin.ReleaseMode)

	hash, cleanup := s.SeedActiveForTesting("arr-remove-me", time.Now().Add(-2*time.Hour))
	defer cleanup()

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: hash.HexString(),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + hash.HexString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Poll da *arr imediatamente antes do remove (o cenário do incidente).
	if got := h.activeTorrentInfo([]downloads.Download{*d}); len(got) != 1 {
		t.Fatalf("activeTorrentInfo = %d entradas, want 1", len(got))
	}

	resp := h.methodTorrentRemove(map[string]interface{}{
		"ids":               []interface{}{float64(d.ID)},
		"delete-local-data": true,
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	if n := len(s.ActiveList()); n != 0 {
		t.Fatalf("ActiveList = %d, want 0 — remove explícito da *arr deve dropar o torrent", n)
	}
	all, _ := st.ListAll()
	if len(all) != 0 {
		t.Fatalf("expected 0 downloads after remove, got %d", len(all))
	}
}

// Sem delete-local-data a *arr só quer a row fora da fila: o torrent ativo
// (arquivos no disco) NÃO pode ser dropado do streamer.
func TestTorrentRemove_KeepLocalData_LeavesActiveTorrentAlone(t *testing.T) {
	st := newTestStore(t)
	s := streamer.NewForTesting()
	h := NewHandler(st, s, nil, "/data", "/data", "", nil)
	gin.SetMode(gin.ReleaseMode)

	hash, cleanup := s.SeedActiveForTesting("arr-keep-me", time.Now().Add(-2*time.Hour))
	defer cleanup()

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: hash.HexString(),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + hash.HexString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := h.methodTorrentRemove(map[string]interface{}{
		"ids": []interface{}{float64(d.ID)},
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	if n := len(s.ActiveList()); n != 1 {
		t.Fatalf("ActiveList = %d, want 1 — sem delete-local-data o torrent fica", n)
	}
	all, _ := st.ListAll()
	if len(all) != 0 {
		t.Fatalf("expected 0 downloads after remove, got %d", len(all))
	}
}
