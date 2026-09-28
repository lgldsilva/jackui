package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

	h, close := s.SeedActiveForTesting("incident-torrent", time.Now().Add(-2*time.Hour))
	defer close()

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

	h, close := s.SeedActiveForTesting("being-watched", time.Now().Add(-2*time.Hour))
	defer close()
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

	h, close := s.SeedActiveForTesting("droppable-torrent", time.Now().Add(-2*time.Hour))
	defer close()

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

// O IsDropRefusal precisa distinguir recusa (409) de idempotência (200).
func TestStreamDrop_RefusalClassification(t *testing.T) {
	if streamer.IsDropRefusal(streamer.ErrTorrentViewerActive) {
		// ok
	} else {
		t.Fatal("ErrTorrentViewerActive deveria ser recusa")
	}
	if !streamer.IsDropRefusal(streamer.ErrTorrentDownloadProtected) {
		t.Fatal("ErrTorrentDownloadProtected deveria ser recusa")
	}
	if streamer.IsDropRefusal(streamer.ErrTorrentNotActive) {
		t.Fatal("ErrTorrentNotActive é sucesso idempotente, não recusa")
	}
	if streamer.IsDropRefusal(errors.New("qualquer erro")) {
		t.Fatal("erro genérico não é recusa")
	}
}
