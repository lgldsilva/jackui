package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/mailer"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
	"github.com/lgldsilva/jackui/internal/transfer"
)

func routerDeps(t *testing.T, node config.NodeConfig) *appDeps {
	t.Helper()
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	return &appDeps{
		cfg:             &config.Config{Node: node},
		streamSrv:       streamer.NewForTesting(),
		hlsMgr:          mgr,
		mlr:             mailer.New(config.SMTPConfig{}),
		transferTracker: transfer.New(1),
		restart:         make(chan struct{}, 1),
	}
}

// The full edge router must build without a gin route-tree conflict (that would
// only surface as a boot panic) and relay download/local/transfer routes to the
// primary while keeping streaming local.
func TestEdgeRouterRelaysToPrimary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var seen []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		_, _ = io.WriteString(w, "from-primary")
	}))
	defer primary.Close()

	r := setupRouter(routerDeps(t, config.NodeConfig{Role: config.NodeRoleEdge, PrimaryURL: primary.URL}))

	relayed := []struct{ method, path string }{
		{http.MethodGet, "/api/downloads"},
		{http.MethodPost, "/api/downloads/batch"},
		{http.MethodGet, "/api/downloads/destinations"},
		{http.MethodPut, "/api/downloads/settings"},
		{http.MethodGet, "/api/transfers"},
		{http.MethodGet, "/api/promote/destinations"},
		{http.MethodGet, "/api/local/list?mount=m&path=p"},
		{http.MethodGet, "/api/local/hls/index.m3u8?mount=m&path=p"},
		{http.MethodGet, "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa?token=t"},
		{http.MethodPost, "/api/stream/art/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/resolve?file=0"},
		{http.MethodPost, "/api/stream/art/resolve/batch"},
	}
	for _, rq := range relayed {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(rq.method, rq.path, nil))
		if w.Body.String() != "from-primary" {
			t.Errorf("%s %s not relayed: %d %q", rq.method, rq.path, w.Code, w.Body.String())
		}
	}
	if len(seen) != len(relayed) {
		t.Fatalf("primary saw %d requests, want %d: %v", len(seen), len(relayed), seen)
	}

	// Streaming stays on the edge.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/stream/active", nil))
	if w.Body.String() == "from-primary" {
		t.Fatal("/api/stream/active must be served by the edge itself")
	}
	if len(seen) != len(relayed) {
		t.Fatalf("stream route reached the primary: %v", seen)
	}
}

func TestPrimaryRouterBuildsAndKeepsLocalRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := setupRouter(routerDeps(t, config.NodeConfig{}))
	want := map[string]bool{"/api/stream/art/:hash": false, "/api/transfers": false, "/api/local/list": false, "/api/local/hls/index.m3u8": false, "/api/lyrics": false}
	for _, ri := range r.Routes() {
		if _, ok := want[ri.Path]; ok {
			want[ri.Path] = true
		}
		if ri.Path == "/api/local/*path" {
			t.Fatal("primary must not register the edge relay")
		}
	}
	for p, ok := range want {
		if !ok {
			t.Errorf("primary router lost %s", p)
		}
	}
}
