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

// rpcAuthFixture is an auth-enabled RPC handler behind a real router.
type rpcAuthFixture struct {
	h      *Handler
	as     *auth.Store
	router *gin.Engine
}

func newRPCAuthFixture(t *testing.T) *rpcAuthFixture {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	st := newTestStore(t)
	as, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, nil, as, "/data", "/data", "", nil)
	router := gin.New()
	h.RegisterRoutes(router)
	return &rpcAuthFixture{h: h, as: as, router: router}
}

func (f *rpcAuthFixture) addUser(t *testing.T, name string, role auth.Role) int {
	t.Helper()
	id, err := f.as.CreateUser(name, name+"-pass1", role)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// handshake does the Basic-Auth leg and returns the HTTP status plus the
// session id the server handed out.
func (f *rpcAuthFixture) handshake(t *testing.T, name string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(`{"method":"session-get"}`))
	req.SetBasicAuth(name, name+"-pass1")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w.Code, w.Header().Get(headerTransmissionSessionID)
}

// call sends one RPC authenticated ONLY by the session id (no Basic Auth).
func (f *rpcAuthFixture) call(t *testing.T, sid, body string) (int, rpcResponse) {
	t.Helper()
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(body))
	req.Header.Set(headerTransmissionSessionID, sid)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	var resp rpcResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// session-get must only ever echo the CALLER's session id. It used to return
// an arbitrary key of the shared sessions map, leaking another user's id —
// which alone authenticates as that user (session hijack, incl. admins).
func TestRPC_SessionGetNeverLeaksAnotherUsersSessionID(t *testing.T) {
	f := newRPCAuthFixture(t)
	f.addUser(t, "alice", auth.RoleUser)
	f.addUser(t, "bob", auth.RoleAdmin)

	_, sidA := f.handshake(t, "alice")
	_, sidB := f.handshake(t, "bob")
	if sidA == "" || sidB == "" || sidA == sidB {
		t.Fatalf("handshake did not yield two distinct session ids: %q %q", sidA, sidB)
	}

	// Map iteration order is random: repeat so a leak cannot hide by luck.
	for i := 0; i < 64; i++ {
		code, resp := f.call(t, sidA, `{"method":"session-get"}`)
		if code != http.StatusOK {
			t.Fatalf("alice session-get: HTTP %d", code)
		}
		got, _ := resp.Arguments["session-id"].(string)
		if got == sidB {
			t.Fatalf("iteration %d: alice's session-get leaked bob's (admin) session id", i)
		}
		if got != sidA {
			t.Fatalf("iteration %d: session-id = %q, want the caller's own %q", i, got, sidA)
		}
	}
}

// With auth off there is no session to report: never mint/echo an id.
func TestRPC_SessionGetAuthOffReportsEmptySessionID(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data", "", nil)
	resp := h.dispatch(rpcRequest{Method: "session-get"}, sysIdent)
	if got, _ := resp.Arguments["session-id"].(string); got != "" {
		t.Fatalf("session-id = %q, want empty when auth is off", got)
	}
}

// Only active accounts may authenticate: a disabled or pending account with
// the right password must not obtain a usable session.
func TestRPC_BasicAuthRefusesInactiveAccounts(t *testing.T) {
	for _, status := range []auth.Status{auth.StatusDisabled, auth.StatusPending} {
		t.Run(string(status), func(t *testing.T) {
			f := newRPCAuthFixture(t)
			id := f.addUser(t, "carol", auth.RoleUser)
			if err := f.as.SetStatus(id, status); err != nil {
				t.Fatal(err)
			}
			code, sid := f.handshake(t, "carol")
			if code != http.StatusUnauthorized {
				t.Errorf("handshake for %s account: HTTP %d, want 401", status, code)
			}
			if sid != "" {
				if code, _ := f.call(t, sid, `{"method":"session-get"}`); code == http.StatusOK {
					t.Fatalf("%s account obtained a working RPC session", status)
				}
			}
		})
	}
}

// Global settings (rate limits, queue/alt-speed state) are admin-only, like
// POST /api/stream/limits on the web API.
func TestRPC_GlobalSettingsRequireAdmin(t *testing.T) {
	f := newRPCAuthFixture(t)
	f.addUser(t, "alice", auth.RoleUser)
	f.addUser(t, "bob", auth.RoleAdmin)
	_, sidUser := f.handshake(t, "alice")
	_, sidAdmin := f.handshake(t, "bob")

	const setBody = `{"method":"session-set","arguments":{"download-queue-size":42}}`
	const groupBody = `{"method":"group-set","arguments":{"name":"Default","speed-limit-down":100}}`

	for _, body := range []string{setBody, groupBody} {
		if _, resp := f.call(t, sidUser, body); resp.Result == "success" {
			t.Errorf("non-admin was allowed: %s", body)
		}
	}
	if _, resp := f.call(t, sidUser, `{"method":"session-get"}`); resp.Arguments["download-queue-size"] == float64(42) {
		t.Error("non-admin session-set mutated the global queue size")
	}

	for _, body := range []string{setBody, groupBody} {
		if _, resp := f.call(t, sidAdmin, body); resp.Result != "success" {
			t.Errorf("admin was refused (%q): %s", resp.Result, body)
		}
	}
	if _, resp := f.call(t, sidAdmin, `{"method":"session-get"}`); resp.Arguments["download-queue-size"] != float64(42) {
		t.Errorf("admin session-set not applied: %v", resp.Arguments["download-queue-size"])
	}
}

// Sessions have an absolute lifetime: once expired the id stops authenticating
// (the client gets the 409 and re-runs the handshake, which re-checks the
// account), and the next handshake prunes the dead entry.
func TestRPC_SessionExpiresAndIsPruned(t *testing.T) {
	f := newRPCAuthFixture(t)
	f.addUser(t, "alice", auth.RoleUser)
	_, sid := f.handshake(t, "alice")
	if code, _ := f.call(t, sid, `{"method":"session-get"}`); code != http.StatusOK {
		t.Fatalf("fresh session refused: HTTP %d", code)
	}

	f.h.mu.Lock()
	s := f.h.sessions[sid]
	if ttl := time.Until(s.expires); ttl <= 0 || ttl > rpcSessionTTL {
		t.Errorf("session ttl = %v, want within (0, %v]", ttl, rpcSessionTTL)
	}
	s.expires = time.Now().Add(-time.Second)
	f.h.sessions[sid] = s
	f.h.mu.Unlock()

	if code, _ := f.call(t, sid, `{"method":"session-get"}`); code != http.StatusConflict {
		t.Fatalf("expired session: HTTP %d, want 409 (re-handshake)", code)
	}

	_, fresh := f.handshake(t, "alice")
	f.h.mu.RLock()
	_, stale := f.h.sessions[sid]
	n := len(f.h.sessions)
	f.h.mu.RUnlock()
	if stale || n != 1 {
		t.Errorf("expired session not pruned on handshake (stale=%v, sessions=%d)", stale, n)
	}
	if code, _ := f.call(t, fresh, `{"method":"session-get"}`); code != http.StatusOK {
		t.Fatalf("re-handshake session refused: HTTP %d", code)
	}
}
