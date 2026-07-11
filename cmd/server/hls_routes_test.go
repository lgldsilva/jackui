package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// TestRegisterHLSRoutesNoConflict monta as rotas HLS reais numa engine gin e
// garante que NÃO há panic de conflito de árvore de rotas — o cenário do gin
// "conflicts with existing wildcard" que a Phase 2 introduziria se `v/:variant`
// colidisse com o legado `:seg`. Sem este teste, o panic só apareceria no boot
// do servidor (runtime), nunca no CI. Também confirma a precedência: uma
// requisição à playlist de variante casa um handler (não cai no NoRoute).
func TestRegisterHLSRoutesNoConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	deps := &appDeps{
		hlsMgr:    mgr,
		streamSrv: streamer.NewForTesting(),
	}

	r := gin.New()
	// NoRoute sentinela: distingue "nenhuma rota casou" de "handler rodou e
	// respondeu 404".
	r.NoRoute(func(c *gin.Context) { c.String(599, "NOROUTE") })

	api := r.Group("/api")
	adminAPI := r.Group("/api")
	// Panic de conflito de rota estouraria AQUI (falha o teste).
	registerHLSRoutes(api, adminAPI, deps)

	// A árvore precisa conter as rotas de variante novas + o legado.
	wantPaths := map[string]bool{
		"/api/stream/hls/:hash/:file/index.m3u8":            false,
		"/api/stream/hls/:hash/:file/v/:variant/index.m3u8": false,
		"/api/stream/hls/:hash/:file/v/:variant/:seg":       false,
		"/api/stream/hls/:hash/:file/:seg":                  false,
	}
	for _, ri := range r.Routes() {
		if _, ok := wantPaths[ri.Path]; ok {
			wantPaths[ri.Path] = true
		}
	}
	for p, found := range wantPaths {
		if !found {
			t.Errorf("rota HLS não registrada: %s", p)
		}
	}

	// Precedência: /v/0/index.m3u8 (3 segmentos após :file) casa a rota de
	// variante — NÃO o NoRoute (599). `v` estático tem prioridade sobre `:seg`.
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, path := range []string{
		"/api/stream/hls/" + hash + "/0/v/0/index.m3u8",
		"/api/stream/hls/" + hash + "/0/v/0/seg_00000.ts",
		"/api/stream/hls/" + hash + "/0/index.m3u8",
		"/api/stream/hls/" + hash + "/0/seg_00000.ts",
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		r.ServeHTTP(w, req)
		if w.Code == 599 {
			t.Errorf("%s caiu no NoRoute (nenhuma rota casou)", path)
		}
	}
}
