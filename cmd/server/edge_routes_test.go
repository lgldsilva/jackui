package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
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
		// Previews of a local-mount file resolve on the primary's disks.
		{http.MethodGet, "/api/preview/comic?mount=m&path=p"},
		{http.MethodGet, "/api/preview/epub/res?mount=m&path=p&href=x"},
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

	// Streaming, and previews of a torrent file, stay on the edge.
	for _, p := range []string{"/api/stream/active", "/api/preview/comic?hash=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&idx=0"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Body.String() == "from-primary" {
			t.Fatalf("%s must be served by the edge itself", p)
		}
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

// Device pairing keeps its pending codes in memory: the TV (start/token) and the
// approving phone (pending/approve) must meet on ONE process, so an edge relays
// those four to the primary. Session/profiles only read the DB and stay local.
func TestEdgeRelaysDevicePairing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var seen []string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		_, _ = io.WriteString(w, "from-primary")
	}))
	defer primary.Close()

	deps := routerDeps(t, config.NodeConfig{Role: config.NodeRoleEdge, PrimaryURL: primary.URL})
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	deps.authStore = store
	deps.tokenMgr = auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), time.Minute)
	deps.deviceFlow = auth.NewDeviceFlow()
	r := setupRouter(deps)

	access, _, err := deps.tokenMgr.SignAccess(&auth.User{ID: 1, Username: "u", Role: auth.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	for _, rq := range []struct{ method, path string }{
		{http.MethodPost, "/api/auth/device/start"},
		{http.MethodPost, "/api/auth/device/token"},
		{http.MethodGet, "/api/auth/device/pending?code=ABCD"},
		{http.MethodPost, "/api/auth/device/approve"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(rq.method, rq.path, nil)
		req.Header.Set("Authorization", "Bearer "+access)
		r.ServeHTTP(w, req)
		if w.Body.String() != "from-primary" {
			t.Errorf("%s %s not relayed: %d %q", rq.method, rq.path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/device/profiles", nil))
	if w.Body.String() == "from-primary" {
		t.Fatal("/api/auth/device/profiles reads the shared DB and must stay local")
	}
	if len(seen) != 4 {
		t.Fatalf("primary saw %v, want the 4 pairing calls", seen)
	}
}
