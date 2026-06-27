package transmissionrpc

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/downloads"
)

// mkDownload cria um download num status específico (cobre mapJackUIStatusToTR).
func mkDownload(t *testing.T, st *downloads.Store, hash, status string) int {
	t.Helper()
	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: hash, FileIndex: -1,
		Magnet: "magnet:?xt=urn:btih:" + hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != downloads.StatusDownloading {
		_ = st.SetStatus(1, d.ID, status)
	}
	return d.ID
}

// torrent-get sobre downloads em todos os status exercita buildTorrent,
// newTorrentView, core/extraTorrentField, mapJackUIStatusToTR e activeTorrentInfo.
func TestRPC_TorrentGet_AllStatuses(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data", "", nil)
	for i, status := range []string{
		downloads.StatusDownloading, downloads.StatusCompleted,
		downloads.StatusPaused, downloads.StatusFailed, downloads.StatusQueued,
	} {
		mkDownload(t, st, strings.Repeat(string(rune('a'+i)), 40), status)
	}
	resp := h.dispatch(rpcRequest{
		Method: "torrent-get",
		Arguments: map[string]interface{}{
			"fields": []interface{}{"id", "name", "status", "percentDone", "rateDownload",
				"eta", "downloadDir", "labels", "trackers", "files", "peers", "error"},
		},
	}, 1)
	if resp.Result != "success" {
		t.Fatalf("torrent-get: %q", resp.Result)
	}
	torrents, ok := resp.Arguments["torrents"].([]interface{})
	if !ok || len(torrents) != 5 {
		t.Fatalf("esperava 5 torrents, got %T len=%d", resp.Arguments["torrents"], len(torrents))
	}
}

// torrent-get com lista de ids filtra (cobre o parseIDs + filtro no get).
func TestRPC_TorrentGet_ByID(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data", "", nil)
	id := mkDownload(t, st, strings.Repeat("a", 40), downloads.StatusDownloading)
	mkDownload(t, st, strings.Repeat("b", 40), downloads.StatusCompleted)
	resp := h.dispatch(rpcRequest{
		Method:    "torrent-get",
		Arguments: map[string]interface{}{"ids": []interface{}{float64(id)}, "fields": []interface{}{"id", "name"}},
	}, 1)
	if resp.Result != "success" {
		t.Fatalf("torrent-get by id: %q", resp.Result)
	}
}

func TestRPC_TorrentRemove(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data", "", nil)
	id := mkDownload(t, st, strings.Repeat("a", 40), downloads.StatusDownloading)
	resp := h.dispatch(rpcRequest{Method: "torrent-remove", Arguments: map[string]interface{}{"ids": []interface{}{float64(id)}}}, 1)
	if resp.Result != "success" {
		t.Fatalf("torrent-remove: %q", resp.Result)
	}
	all, _ := st.ListAll()
	if len(all) != 0 {
		t.Errorf("download não foi removido: %d restantes", len(all))
	}
	// sem ids → erro
	if r := h.dispatch(rpcRequest{Method: "torrent-remove", Arguments: map[string]interface{}{}}, 1); r.Result == "success" {
		t.Error("torrent-remove sem ids deveria falhar")
	}
}

func TestRPC_TorrentSet_Paused(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data", "", nil)
	id := mkDownload(t, st, strings.Repeat("a", 40), downloads.StatusDownloading)
	// pausa
	h.dispatch(rpcRequest{Method: "torrent-set", Arguments: map[string]interface{}{"ids": []interface{}{float64(id)}, "paused": true}}, 1)
	if d, _ := st.Get(1, id); d.Status != downloads.StatusPaused {
		t.Errorf("esperava paused, got %s", d.Status)
	}
	// retoma
	h.dispatch(rpcRequest{Method: "torrent-set", Arguments: map[string]interface{}{"ids": []interface{}{float64(id)}, "paused": false}}, 1)
	if d, _ := st.Get(1, id); d.Status != downloads.StatusDownloading {
		t.Errorf("esperava downloading, got %s", d.Status)
	}
}

func TestRPC_FreeSpace_And_SetLocation(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data", "", nil)
	if r := h.dispatch(rpcRequest{Method: "free-space", Arguments: map[string]interface{}{"path": "/data"}}, 1); r.Result != "success" {
		t.Errorf("free-space: %q", r.Result)
	}
	if r := h.dispatch(rpcRequest{Method: "torrent-set-location", Arguments: map[string]interface{}{"ids": []interface{}{float64(1)}, "location": "/data/x"}}, 1); r.Result != "success" {
		t.Errorf("torrent-set-location: %q", r.Result)
	}
}

func TestRPC_SessionStats_WithData(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data", "", nil)
	mkDownload(t, st, strings.Repeat("a", 40), downloads.StatusDownloading)
	mkDownload(t, st, strings.Repeat("b", 40), downloads.StatusCompleted)
	if r := h.methodSessionStats(); r.Result != "success" {
		t.Errorf("session-stats: %q", r.Result)
	}
}

// torrent-add: variações de filename (cobre os ramos de methodTorrentAdd).
func TestRPC_TorrentAdd_Variants(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data", "", nil)
	// bare infohash (sem prefixo magnet:) → vira magnet
	if r := h.methodTorrentAdd(map[string]interface{}{"filename": strings.Repeat("a", 40)}, 1); r.Result != "success" {
		t.Errorf("bare infohash: %q", r.Result)
	}
	// paused=true
	if r := h.methodTorrentAdd(map[string]interface{}{"filename": "magnet:?xt=urn:btih:" + strings.Repeat("b", 40), "paused": true, "download-dir": "/downloads/tv-sonarr"}, 1); r.Result != "success" {
		t.Errorf("paused add: %q", r.Result)
	}
	// filename vazio → erro
	if r := h.methodTorrentAdd(map[string]interface{}{}, 1); r.Result == "success" {
		t.Error("filename vazio deveria falhar")
	}
	// não-magnet, não-URL, não-hash → unsupported
	if r := h.methodTorrentAdd(map[string]interface{}{"filename": "qualquer-coisa-invalida"}, 1); r.Result == "success" {
		t.Error("filename não suportado deveria falhar")
	}
}

// Handshake com auth habilitado: 1º request (BasicAuth, sem session-id) → 409
// com X-Transmission-Session-Id; 2º com o session-id → processa. Cobre o ramo
// authStore != nil do rpcHandler + emit409.
func TestRPC_AuthHandshake(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	st := newTestStore(t)
	as, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer as.Close()
	if _, err := as.CreateUser("bob", "secret123!", auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, nil, as, "/data", "/data", "", nil)
	router := gin.New()
	h.RegisterRoutes(router)

	// 1) BasicAuth, sem session-id → 409 + header
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(`{"method":"session-get"}`))
	req.SetBasicAuth("bob", "secret123!")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("esperava 409 no handshake, got %d", w.Code)
	}
	sid := w.Header().Get("X-Transmission-Session-Id")
	if sid == "" {
		t.Fatal("409 sem X-Transmission-Session-Id")
	}
	// 2) com o session-id → 200
	req2 := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(`{"method":"session-get"}`))
	req2.Header.Set("X-Transmission-Session-Id", sid)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("esperava 200 com session-id, got %d", w2.Code)
	}
}
