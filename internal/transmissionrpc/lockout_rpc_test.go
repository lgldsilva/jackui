package transmissionrpc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

// The Basic-Auth password-verify path must be guarded by the auth lockout:
// after MaxFailures consecutive wrong passwords the username is locked and
// even the CORRECT password is refused (429 + Retry-After) until the window
// expires. Mirrors handlers.Login's respondIfLocked behavior.
func TestRPC_BasicAuthLockout(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	st := newTestStore(t)
	as, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := as.CreateUser("carol", "carolpass1", auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, nil, as, "/data", "/data", "", nil)
	h.SetLockout(auth.NewLockout(3, time.Minute))
	router := gin.New()
	h.RegisterRoutes(router)

	const maxFailures = 3
	// Burn the failure budget with wrong passwords (bad creds answer with the
	// normal 409 handshake — Transmission-compatible — but count failures).
	for i := 0; i < maxFailures; i++ {
		req := postBasic(t, router, "carol", "wrong-pass")
		if req == http.StatusTooManyRequests {
			t.Fatalf("attempt %d locked earlier than MaxFailures=%d", i+1, maxFailures)
		}
	}

	// Now the CORRECT password must be refused: the username is locked out.
	code, body := postBasicBody(t, router, "carol", "carolpass1")
	if code != http.StatusTooManyRequests {
		t.Fatalf("locked-out username accepted the correct password: HTTP %d (body %s)", code, body)
	}
	var rr rpcResponse
	if err := json.Unmarshal([]byte(body), &rr); err != nil {
		t.Fatalf("429 body is not an rpcResponse: %v (%s)", err, body)
	}
	if rr.Result == "success" {
		t.Fatal("locked-out username must not get a success result")
	}
	// The 409 path must not have handed out a session-id while locked.
	// (A successful handshake would have set the header; verify via a retry
	// that still fails rather than being treated as an established session.)
	code, _ = postBasicBody(t, router, "carol", "carolpass1")
	if code != http.StatusTooManyRequests {
		t.Fatalf("lockout not sticky: HTTP %d", code)
	}

	// Control: a different username is NOT affected by carol's lockout.
	if _, err := as.CreateUser("dave", "davepass1", auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	if code := postBasic(t, router, "dave", "davepass1"); code != http.StatusConflict {
		t.Fatalf("unrelated user should still handshake (409), got %d", code)
	}
}

func postBasic(t *testing.T, router http.Handler, user, pass string) int {
	t.Helper()
	code, _ := postBasicBody(t, router, user, pass)
	return code
}

func postBasicBody(t *testing.T, router http.Handler, user, pass string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/transmission/rpc",
		bytes.NewBufferString(`{"method":"session-get","arguments":{}}`))
	req.SetBasicAuth(user, pass)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}
