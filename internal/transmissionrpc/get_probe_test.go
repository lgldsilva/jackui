package transmissionrpc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

// *arr clients (Sonarr/Radarr/Prowlarr) fetch the CSRF session-id with a GET to
// /transmission/rpc before POSTing. The handler must answer that GET with the
// Transmission handshake (409 + X-Transmission-Session-Id), NOT fall through to
// the SPA (which returned 200 HTML and made the client report "auth failure").
func TestRPC_GetSessionProbe(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	st := newTestStore(t)
	as, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := as.CreateUser("bob", "secret123!", auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, nil, as, "/data", "/data", "", nil)
	router := gin.New()
	h.RegisterRoutes(router)

	// GET probe with Basic Auth → 409 + session-id (the handshake the *arr needs).
	req := httptest.NewRequest("GET", "/transmission/rpc", nil)
	req.SetBasicAuth("bob", "secret123!")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("GET probe: esperava 409, got %d (body %s)", w.Code, w.Body.String())
	}
	sid := w.Header().Get("X-Transmission-Session-Id")
	if sid == "" {
		t.Fatal("GET probe 409 sem X-Transmission-Session-Id")
	}

	// GET carrying the established session-id → 200 (acknowledge; no RPC body).
	req2 := httptest.NewRequest("GET", "/transmission/rpc", nil)
	req2.Header.Set("X-Transmission-Session-Id", sid)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("GET com session-id: esperava 200, got %d", w2.Code)
	}
}
