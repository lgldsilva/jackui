package edgeproxy

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

var testModTime = time.Unix(1_700_000_000, 0)

func newRouter(t *testing.T, primary string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, err := New(primary)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Any("/api/downloads/*path", h)
	r.GET("/api/local-only", func(c *gin.Context) { c.String(http.StatusOK, "edge") })
	return r
}

func TestNewRejectsBadURL(t *testing.T) {
	for _, u := range []string{"", "10.0.0.1:8989", "ftp://x", "http://"} {
		if _, err := New(u); err == nil {
			t.Errorf("New(%q) accepted an invalid primary URL", u)
		}
	}
}

func TestForwardsMethodPathQueryBodyAndAuth(t *testing.T) {
	var got struct {
		method, path, query, body, auth, cookie, incognito, marker string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query, got.body = r.Method, r.URL.Path, r.URL.RawQuery, string(b)
		got.auth, got.cookie = r.Header.Get("Authorization"), r.Header.Get("Cookie")
		got.incognito, got.marker = r.Header.Get("X-JackUI-Incognito"), r.Header.Get(HeaderForwardedBy)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7}`))
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/downloads/batch?x=1&token=abc", strings.NewReader(`{"files":[1]}`))
	req.Header.Set("Authorization", "Bearer jwt")
	req.Header.Set("Cookie", "a=b")
	req.Header.Set("X-JackUI-Incognito", "1")
	w := httptest.NewRecorder()
	newRouter(t, upstream.URL).ServeHTTP(w, req)

	if w.Code != http.StatusCreated || w.Body.String() != `{"id":7}` {
		t.Fatalf("response = %d %q", w.Code, w.Body.String())
	}
	if got.method != http.MethodPost || got.path != "/api/downloads/batch" || got.query != "x=1&token=abc" {
		t.Fatalf("upstream saw %s %s ?%s", got.method, got.path, got.query)
	}
	if got.body != `{"files":[1]}` || got.auth != "Bearer jwt" || got.cookie != "a=b" || got.incognito != "1" {
		t.Fatalf("upstream lost body/headers: %+v", got)
	}
	if got.marker != "1" {
		t.Fatal("forwarded marker header missing")
	}
}

func TestRangeRequestsPassThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f.bin", testModTime, strings.NewReader("0123456789"))
	}))
	defer upstream.Close()
	req := httptest.NewRequest(http.MethodGet, "/api/downloads/file", nil)
	req.Header.Set("Range", "bytes=2-4")
	w := httptest.NewRecorder()
	newRouter(t, upstream.URL).ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent || w.Body.String() != "234" {
		t.Fatalf("range response = %d %q", w.Code, w.Body.String())
	}
}

func TestPrimaryDownReturns502JSON(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	url := upstream.URL
	upstream.Close() // nothing listening any more
	w := httptest.NewRecorder()
	newRouter(t, url).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/downloads/x", nil))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "primary unavailable") {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}

func TestLoopIsRejected(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer upstream.Close()
	req := httptest.NewRequest(http.MethodGet, "/api/downloads/x", nil)
	req.Header.Set(HeaderForwardedBy, "1")
	w := httptest.NewRecorder()
	newRouter(t, upstream.URL).ServeHTTP(w, req)
	if w.Code != http.StatusLoopDetected || called {
		t.Fatalf("loop not rejected: code=%d upstream called=%v", w.Code, called)
	}
}

func TestUnproxiedRouteStaysLocal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("local route must not reach the primary")
	}))
	defer upstream.Close()
	w := httptest.NewRecorder()
	newRouter(t, upstream.URL).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/local-only", nil))
	if w.Body.String() != "edge" {
		t.Fatalf("got %q", w.Body.String())
	}
}

// The 502 log line must not let a crafted path forge extra log entries.
func TestPrimaryDownLogIsSanitized(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	upstream := httptest.NewServer(http.NotFoundHandler())
	url := upstream.URL
	upstream.Close()
	w := httptest.NewRecorder()
	newRouter(t, url).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/downloads/x%0Aforged-entry", nil))
	if strings.Count(buf.String(), "\n") != 1 || !strings.Contains(buf.String(), "forged-entry") {
		t.Fatalf("log line not sanitized: %q", buf.String())
	}
}
