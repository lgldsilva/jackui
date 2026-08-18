package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
)

func pauseRouter(store *downloads.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PATCH("/api/downloads/:id/pause", DownloadsPause(store))
	router.PATCH("/api/downloads/batch/pause", DownloadsBatchPause(store))
	return router
}

// PATCH /downloads/:id/pause num item concluído precisa ser recusado: deixar
// passar transformava o `completed` em `paused`, e aí o card perdia as ações de
// concluído (Promover / Parar e remover / Abrir no local) — o item ficava preso
// na lista sem como tirá-lo mantendo os arquivos no disco.
func TestDownloadsPause_RejectsCompleted(t *testing.T) {
	store := hgAStore(t)
	d := mustCreateDownload(t, store, hgAValidHash, downloads.StatusCompleted)
	router := pauseRouter(store)

	w := hgADo(router, "PATCH", "/api/downloads/"+strconv.Itoa(d.ID)+"/pause", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", w.Code)
	}

	got, err := store.Get(d.UserID, d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != downloads.StatusCompleted {
		t.Errorf("status=%q, want %q — a row não pode sair de concluído", got.Status, downloads.StatusCompleted)
	}
}

// Um download em andamento continua pausável — a guarda é só para os terminais.
func TestDownloadsPause_AllowsDownloading(t *testing.T) {
	store := hgAStore(t)
	d := mustCreateDownload(t, store, hgAValidHash, downloads.StatusDownloading)
	router := pauseRouter(store)

	w := hgADo(router, "PATCH", "/api/downloads/"+strconv.Itoa(d.ID)+"/pause", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}

	got, _ := store.Get(d.UserID, d.ID)
	if got.Status != downloads.StatusPaused {
		t.Errorf("status=%q, want %q", got.Status, downloads.StatusPaused)
	}
}

// O batch não pode falhar inteiro por causa de um terminal na seleção: pausa o
// que dá e reporta `affected` com a contagem real (o frontend usa esse número).
func TestDownloadsBatchPause_SkipsCompletedRows(t *testing.T) {
	store := hgAStore(t)
	secondHash := "c1c2c3c4c5c6c7c8c9c0c1c2c3c4c5c6c7c8c9c0c1c2c3c4"
	active := mustCreateDownload(t, store, hgAValidHash, downloads.StatusDownloading)
	completed := mustCreateDownload(t, store, secondHash, downloads.StatusCompleted)
	router := pauseRouter(store)

	body, _ := json.Marshal(map[string]any{"ids": []int{active.ID, completed.ID}})
	w := hgADo(router, "PATCH", "/api/downloads/batch/pause", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	var resp struct {
		Affected int `json:"affected"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Affected != 1 {
		t.Errorf("affected=%d, want 1 — só a row ativa pode ser pausada", resp.Affected)
	}

	gotCompleted, _ := store.Get(completed.UserID, completed.ID)
	if gotCompleted.Status != downloads.StatusCompleted {
		t.Errorf("completed virou %q no batch pause", gotCompleted.Status)
	}
}
