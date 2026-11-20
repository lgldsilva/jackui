package main

// openapi_validate_test.go — issue #201 item 1: response validation with
// kin-openapi in httptest. Boots the full router, hits representative
// lean-back endpoints and validates every response (status + schema) against
// docs/api/openapi.yaml, so the spec cannot silently drift from what the
// server actually answers.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/mailer"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
	"github.com/lgldsilva/jackui/internal/transcode"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

func TestOpenAPIResponsesValidate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	raw, err := os.ReadFile("../../docs/api/openapi.yaml")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("spec invalid: %v", err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("spec router: %v", err)
	}

	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	full := &appDeps{
		cfg:            &config.Config{},
		mlr:            mailer.New(config.SMTPConfig{}),
		streamSrv:      streamer.NewForTesting(),
		hlsMgr:         mgr,
		subtitleClient: subtitles.New("", "", "", t.TempDir()),
		historyStore:   &history.Store{},
		libraryStore:   &library.Store{},
		downloadsStore: &downloads.Store{},
		playlistsStore: &playlists.Store{},
		watchlistStore: &watchlist.Store{},
		authStore:      &auth.Store{},
		// Real token manager so the /api/auth public group (login/refresh/
		// logout) registers like production; the routes only capture it.
		tokenMgr: auth.NewTokenManager([]byte("test-secret-0123456789abcdef0123"), 15*time.Minute),
	}
	engine := setupRouter(full)
	server := httptest.NewServer(engine)
	defer server.Close()

	// Stable error shape (#201 item 2): a 401 must carry {"error","code"} —
	// asserted directly and via the spec. Uses /api/library because /auth/me
	// double-writes the 401 body (pre-existing middleware-composition bug,
	// tracked separately) which breaks the JSON decoder.
	t.Run("stable error shape", func(t *testing.T) {
		resp, body := doRequest(t, server, http.MethodGet, "/api/library", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
		if body["error"] == nil || body["code"] == nil {
			t.Fatalf("401 must carry {error,code}, got %v", body)
		}
		validateAgainstSpec(t, router, resp)
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"login body shape", http.MethodPost, "/api/auth/login"},
		{"refresh missing token 400", http.MethodPost, "/api/auth/refresh"},
		{"library unauthorized 401", http.MethodGet, "/api/library"},
		{"favorites unauthorized 401", http.MethodGet, "/api/stream/favorites"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := doRequest(t, server, tc.method, tc.path, "")
			validateAgainstSpec(t, router, resp)
		})
	}
}

// rawBodyKey stores the response bytes on the request: doRequest decodes the
// JSON once and validateAgainstSpec re-validates the same bytes against the
// spec (http bodies are single-read).
type rawBodyKey struct{}

func doRequest(t *testing.T, server *httptest.Server, method, path, body string) (*http.Response, map[string]interface{}) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = io.NopCloser(newBytesReader(body))
	}
	req, err := http.NewRequest(method, server.URL+path, rd)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	*req = *req.WithContext(context.WithValue(req.Context(), rawBodyKey{}, raw))
	resp.Request = req
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	if os.Getenv("BODY_DEBUG") == "1" {
		t.Logf("raw %d bytes: %q", len(raw), string(raw[:min(len(raw), 120)]))
	}
	return resp, m
}

func validateAgainstSpec(t *testing.T, r routers.Router, resp *http.Response) {
	t.Helper()
	req := resp.Request
	route, pathParams, err := r.FindRoute(req)
	if err != nil {
		t.Fatalf("request %s %s not matched by the spec: %v", req.Method, req.URL.Path, err)
	}
	requestInput := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: pathParams,
		Route:      route,
	}
	raw, ok := req.Context().Value(rawBodyKey{}).([]byte)
	if !ok {
		raw, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	}
	if err := openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: requestInput,
		Status:                 resp.StatusCode,
		Header:                 resp.Header,
		Body:                   io.NopCloser(bytes.NewReader(raw)),
	}); err != nil {
		t.Fatalf("response %s %s (%d) does not match the spec: %v", req.Method, req.URL.Path, resp.StatusCode, err)
	}
}

func bodyBytes(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw
}

func newBytesReader(s string) io.Reader {
	return bytes.NewReader([]byte(s))
}
