package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── Regressão do incidente de 2026-09-28 ───────────────────────────────────
//
// Antes: DELETE /api/stream/:hash respondia 200 {"message":"dropped"} mesmo
// quando o Streamer.Drop recusava o drop (activeReadGuard alimentado pelo
// torrent-get da *arr) — a UI não tinha como saber que nada aconteceu.
// Agora: a ação explícita dropa mesmo após o poll, e uma recusa real
// (viewer lease / download em background) vira 409 com o motivo.

// O cenário exato do incidente — poll da *arr (Get) e o clique logo em
// seguida — agora termina com o torrent REMOVIDO e 200 verdadeiro.
func TestStreamDrop_DropsEvenRightAfterArrPoll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	h, cleanup := s.SeedActiveForTesting("incident-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	// torrent-get da *arr → GetUntouched; um Get aqui simularia leitura real —
	// o fix vale para ambos, pois o caminho explícito bypassa o guard de leitura.
	if _, err := s.Get(h); err != nil {
		t.Fatalf("Get (poll *arr): %v", err)
	}

	router := gin.New()
	router.DELETE("/api/stream/:hash", StreamDrop(s, nil, nil))

	req := httptest.NewRequest("DELETE", "/api/stream/"+h.HexString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["message"] != "dropped" {
		t.Fatalf("message = %q, want 'dropped'", resp["message"])
	}
	if got := len(s.ActiveList()); got != 0 {
		t.Fatalf("ActiveList = %d entradas após DELETE 200, want 0 (agora o 200 é verdadeiro)", got)
	}
}

// Recusa real com feedback honesto: player aberto (viewer lease) → 409 com o
// motivo, torrent permanece ativo e a row NÃO é marcada seed-stopped.
func TestStreamDrop_Conflict_WhenViewerLeaseActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	h, cleanup := s.SeedActiveForTesting("being-watched", time.Now().Add(-2*time.Hour))
	defer cleanup()
	s.AcquireViewer(h)

	router := gin.New()
	router.DELETE("/api/stream/:hash", StreamDrop(s, nil, nil))

	req := httptest.NewRequest("DELETE", "/api/stream/"+h.HexString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
	if got := len(s.ActiveList()); got != 1 {
		t.Fatalf("ActiveList = %d, want 1 — viewer lease deve manter o torrent", got)
	}
}

// Controle: sem leitura recente e sem viewer, o DELETE dropa de verdade.
func TestStreamDrop_ActuallyDrops_WhenNoRecentMonitoringRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	h, cleanup := s.SeedActiveForTesting("droppable-torrent", time.Now().Add(-2*time.Hour))
	defer cleanup()

	router := gin.New()
	router.DELETE("/api/stream/:hash", StreamDrop(s, nil, nil))

	req := httptest.NewRequest("DELETE", "/api/stream/"+h.HexString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := len(s.ActiveList()); got != 0 {
		t.Fatalf("ActiveList = %d entradas após DELETE, want 0", got)
	}
}

// Batch: a recusa de UM hash (viewer lease) não aborta o lote nem vira
// "dropped" fantasma — o hash recusado vai para failed (com a string RAW que o
// cliente mandou), os demais são dropados de verdade, e o torrent segurado pelo
// viewer permanece ativo.
func TestStreamDropBatch_RefusedHashLandsInFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	watched, cleanupWatched := s.SeedActiveForTesting("batch-being-watched", time.Now().Add(-2*time.Hour))
	defer cleanupWatched()
	idle, cleanupIdle := s.SeedActiveForTesting("batch-idle", time.Now().Add(-2*time.Hour))
	defer cleanupIdle()
	s.AcquireViewer(watched)

	// Poll da *arr entre o seed e o clique: não pode influenciar o resultado.
	if _, err := s.Get(idle); err != nil {
		t.Fatalf("Get (poll *arr): %v", err)
	}

	router := gin.New()
	router.POST("/api/stream/drop/batch", StreamDropBatch(s, nil, nil))

	// O hash assistido vai em MAIÚSCULAS para provar que failed devolve a
	// string raw (é assim que o cliente casa de volta com a seleção).
	rawWatched := strings.ToUpper(watched.HexString())
	body := `{"hashes":["` + rawWatched + `","` + idle.HexString() + `","` + idle.HexString() + `"]}`
	w := postDropBatch(t, router, body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Dropped int      `json:"dropped"`
		Total   int      `json:"total"`
		Failed  []string `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Dropped != 1 || resp.Total != 3 {
		t.Fatalf("dropped=%d total=%d, want 1/3 (duplicata deduplicada, recusado não conta)", resp.Dropped, resp.Total)
	}
	if len(resp.Failed) != 1 || resp.Failed[0] != rawWatched {
		t.Fatalf("failed = %v, want [%s] (string raw do hash recusado)", resp.Failed, rawWatched)
	}

	active := s.ActiveList()
	if len(active) != 1 {
		t.Fatalf("ActiveList = %d, want 1 — só o torrent com viewer lease sobrevive", len(active))
	}
	if _, err := s.GetUntouched(watched); err != nil {
		t.Fatalf("torrent assistido deveria continuar ativo: %v", err)
	}
	if _, err := s.GetUntouched(idle); err == nil {
		t.Fatal("torrent ocioso deveria ter sido dropado pelo batch")
	}
}

// Com store: a row do hash dropado é marcada seed-stopped (para o boot não
// ressuscitar o auto-seed), mas a do hash RECUSADO fica intacta — o torrent
// continua vivo, então marcar seed_stopped seria mentir para o próximo boot.
func TestStreamDropBatch_SeedStoppedOnlyForDroppedRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	s := streamer.NewForTesting()

	watched, cleanupWatched := s.SeedActiveForTesting("store-being-watched", time.Now().Add(-2*time.Hour))
	defer cleanupWatched()
	idle, cleanupIdle := s.SeedActiveForTesting("store-idle", time.Now().Add(-2*time.Hour))
	defer cleanupIdle()
	s.AcquireViewer(watched)

	rowWatched := mustCreateDownload(t, store, watched.HexString(), downloads.StatusCompleted)
	rowIdle := mustCreateDownload(t, store, idle.HexString(), downloads.StatusCompleted)

	router := gin.New()
	router.POST("/api/stream/drop/batch", StreamDropBatch(s, nil, store))

	body := `{"hashes":["` + watched.HexString() + `","` + idle.HexString() + `"]}`
	w := postDropBatch(t, router, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	gotIdle, err := store.Get(rowIdle.UserID, rowIdle.ID)
	if err != nil {
		t.Fatalf("Get(idle): %v", err)
	}
	if gotIdle.SeedStoppedAt == nil {
		t.Fatal("row do hash dropado deveria estar seed-stopped")
	}
	gotWatched, err := store.Get(rowWatched.UserID, rowWatched.ID)
	if err != nil {
		t.Fatalf("Get(watched): %v", err)
	}
	if gotWatched.SeedStoppedAt != nil {
		t.Fatalf("row do hash RECUSADO não pode ser seed-stopped (torrent segue vivo): %v", gotWatched.SeedStoppedAt)
	}
}
