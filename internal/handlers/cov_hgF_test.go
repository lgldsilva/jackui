package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
)

// hgF prefix on every identifier to avoid collisions with the other test files
// in this package. These tests target the 0%/low-coverage handlers that the
// existing *_test.go / cov_*_test.go files don't yet exercise — chiefly the
// full HTTP flow of HistoryRefresh/historyRefreshHandler and the upstream GET
// branches of ProxyTorrentDownload.

// hgFHistory spins up a throwaway SQLite-backed history store in a temp dir.
func hgFHistory(t *testing.T) *history.Store {
	t.Helper()
	s, err := history.New(seededPool(t))
	if err != nil {
		t.Fatalf("history.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// hgFSeedRow saves one result for the given query and returns its row ID by
// reading it back via Search (admin scope, anonymous user).
func hgFSeedRow(t *testing.T, store *history.Store, query string, r jackett.Result) int64 {
	t.Helper()
	if err := store.Save(query, []jackett.Result{r}, 0, false); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	rows, err := store.Search(query, 0, true)
	if err != nil {
		t.Fatalf("store.Search: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("seeded row not found for query %q", query)
	}
	return rows[0].ID
}

func hgFCtx(t *testing.T, method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

// ----- historyRefreshHandler -----

func Test_hgF_HistoryRefresh_InvalidID(t *testing.T) {
	store := hgFHistory(t)
	cache := newRefreshCache()

	c, w := hgFCtx(t, "POST", "/api/history/notanint/refresh")
	c.Params = gin.Params{{Key: "id", Value: "notanint"}}

	historyRefreshHandler(c, store, nil, cache)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgF_HistoryRefresh_RowNotFound(t *testing.T) {
	store := hgFHistory(t)
	cache := newRefreshCache()

	c, w := hgFCtx(t, "POST", "/api/history/999/refresh")
	c.Params = gin.Params{{Key: "id", Value: "999"}}

	historyRefreshHandler(c, store, nil, cache)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgF_HistoryRefresh_NilJackett(t *testing.T) {
	store := hgFHistory(t)
	cache := newRefreshCache()
	id := hgFSeedRow(t, store, "matrix", jackett.Result{
		Title: "The Matrix 1999", Tracker: "T1", InfoHash: "hgfhash1",
	})

	c, w := hgFCtx(t, "POST", "/api/history/x/refresh")
	c.Params = gin.Params{{Key: "id", Value: itoaHGF(id)}}

	// jck == nil → 503 (after row lookup succeeds and cache misses).
	historyRefreshHandler(c, store, nil, cache)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgF_HistoryRefresh_JackettError(t *testing.T) {
	store := hgFHistory(t)
	cache := newRefreshCache()
	id := hgFSeedRow(t, store, "dune", jackett.Result{
		Title: "Dune 2021", Tracker: "T1", InfoHash: "hgfhash2",
	})

	// Upstream always 500s → jck.Search returns an error → 502.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	jck := jackett.New(srv.URL, "key")

	c, w := hgFCtx(t, "POST", "/api/history/x/refresh")
	c.Params = gin.Params{{Key: "id", Value: itoaHGF(id)}}

	historyRefreshHandler(c, store, jck, cache)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgF_HistoryRefresh_Success_FreshPoll(t *testing.T) {
	store := hgFHistory(t)
	cache := newRefreshCache()
	id := hgFSeedRow(t, store, "blade", jackett.Result{
		Title: "Blade Runner 2049", Tracker: "T1", InfoHash: "hgfhash3",
	})

	// Upstream returns a fresh result matching the seeded title (by infoHash).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Results":[{"Title":"Blade Runner 2049","Tracker":"T1","Seeders":42,"Peers":50,"InfoHash":"hgfhash3"}]}`))
	}))
	defer srv.Close()
	jck := jackett.New(srv.URL, "key")

	c, w := hgFCtx(t, "POST", "/api/history/x/refresh")
	c.Params = gin.Params{{Key: "id", Value: itoaHGF(id)}}

	historyRefreshHandler(c, store, jck, cache)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"seeders":42`) {
		t.Errorf("expected seeders=42 in body; got: %s", body)
	}
	if !strings.Contains(body, `"cached":false`) {
		t.Errorf("expected cached=false for fresh poll; got: %s", body)
	}

	// Second call must now hit the TTL cache (cached=true) without a fresh poll.
	c2, w2 := hgFCtx(t, "POST", "/api/history/x/refresh")
	c2.Params = gin.Params{{Key: "id", Value: itoaHGF(id)}}
	historyRefreshHandler(c2, store, jck, cache)
	if w2.Code != http.StatusOK {
		t.Fatalf("cached call status = %d, want 200; body: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), `"cached":true`) {
		t.Errorf("expected cached=true on second call; got: %s", w2.Body.String())
	}
}

// Test_hgF_HistoryRefresh_Wrapper drives the public HistoryRefresh constructor
// (which builds its own cache) through gin routing for the 404 path.
func Test_hgF_HistoryRefresh_Wrapper(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := hgFHistory(t)
	router := gin.New()
	router.POST("/api/history/:id/refresh", HistoryRefresh(store, nil))

	req := httptest.NewRequest("POST", "/api/history/12345/refresh", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// ----- ProxyTorrentDownload upstream GET branches -----

func Test_hgF_Proxy_UpstreamConnError(t *testing.T) {
	// Point the client at an unroutable address so proxyHTTP.Get fails → 502.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close() // close immediately so the GET fails to connect
	client := jackett.New(base, "key")

	c, w := hgFCtx(t, "GET", "/api/proxy?url="+base+"/dl/x.torrent")

	ProxyTorrentDownload(client)(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

func Test_hgF_Proxy_UpstreamNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	client := jackett.New(srv.URL, "key")

	c, w := hgFCtx(t, "GET", "/api/proxy?url="+srv.URL+"/dl/missing.torrent")

	ProxyTorrentDownload(client)(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (mirrored upstream); body: %s", w.Code, w.Body.String())
	}
}

func Test_hgF_Proxy_UpstreamSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		w.Header().Set("Content-Disposition", `attachment; filename="hgf.torrent"`)
		_, _ = w.Write([]byte("d8:announce..."))
	}))
	defer srv.Close()
	client := jackett.New(srv.URL, "key")

	c, w := hgFCtx(t, "GET", "/api/proxy?url="+srv.URL+"/dl/hgf.torrent")

	ProxyTorrentDownload(client)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get(httpshared.ContentType); ct != "application/x-bittorrent" {
		t.Errorf("Content-Type = %q, want application/x-bittorrent", ct)
	}
	if !strings.Contains(w.Body.String(), "d8:announce") {
		t.Errorf("proxied body not forwarded: %q", w.Body.String())
	}
}

// itoaHGF converts an int64 row ID to its decimal string form for use as a gin
// path param, without pulling in strconv at call sites.
func itoaHGF(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
