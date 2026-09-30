package transmissionrpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/downloads"
)

// ─── helpers ───────────────────────────────────────────────────────────────

// sysIdent is the admin identity pre-existing tests act as: full cross-user
// visibility, matching the behavior those tests were written against.
var sysIdent = rpcIdentity{userID: 0, admin: true}

// rpcPost sends an RPC body to the router, optionally carrying an established
// session-id header.
func rpcPost(t *testing.T, router *gin.Engine, sid, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if sid != "" {
		req.Header.Set(headerTransmissionSessionID, sid)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// rpcHandshake performs the Transmission 409 handshake with Basic Auth and
// returns the session-id the server handed out.
func rpcHandshake(t *testing.T, router *gin.Engine, user, pass string) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/transmission/rpc",
		bytes.NewBufferString(`{"method":"session-get","arguments":{}}`))
	req.SetBasicAuth(user, pass)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("handshake: expected 409, got %d (body %s)", w.Code, w.Body.String())
	}
	sid := w.Header().Get(headerTransmissionSessionID)
	if sid == "" {
		t.Fatal("handshake: no X-Transmission-Session-Id in 409 response")
	}
	return sid
}

// torrentIDs extracts the "id" field of every torrent in a torrent-get reply.
func torrentIDs(t *testing.T, w *httptest.ResponseRecorder) []int {
	t.Helper()
	var rr rpcResponse
	if err := json.NewDecoder(w.Body).Decode(&rr); err != nil {
		t.Fatalf("decode rpc response: %v (body %s)", err, w.Body.String())
	}
	raw, ok := rr.Arguments["torrents"].([]interface{})
	if !ok {
		t.Fatalf("torrents is %T, want []interface{} (body %s)", rr.Arguments["torrents"], w.Body.String())
	}
	ids := make([]int, 0, len(raw))
	for _, e := range raw {
		m, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if f, ok := m["id"].(float64); ok {
			ids = append(ids, int(f))
		}
	}
	return ids
}

// mkUserRow creates a download row attributed to userID with a distinct hash.
func mkUserRow(t *testing.T, st *downloads.Store, userID int, hashByte byte) *downloads.Download {
	t.Helper()
	hash := strings.Repeat(string(hashByte), 40)
	d, err := st.Create(downloads.Download{
		UserID: userID, InfoHash: hash, FileIndex: -1,
		Magnet: "magnet:?xt=urn:btih:" + hash,
	})
	if err != nil {
		t.Fatalf("create download for user %d: %v", userID, err)
	}
	return d
}

// ─── (a) guests must be refused the RPC ────────────────────────────────────

// Guest accounts (auth.RoleGuest) are read-only UI visitors: the Transmission
// RPC can add/remove ANY download, so guests must never get past
// authentication — neither the 409 handshake nor an RPC dispatch.
func TestRPC_GuestCredentialsRefused(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	st := newTestStore(t)
	as, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := as.CreateUser("guesty", "guestpass1", auth.RoleGuest); err != nil {
		t.Fatal(err)
	}
	// Control account: a regular user must still complete the handshake.
	if _, err := as.CreateUser("regular", "regularpass1", auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, nil, as, "/data", "/data", "", nil)
	router := gin.New()
	h.RegisterRoutes(router)

	// GET probe with guest Basic Auth → 401, never the 409 handshake.
	req := httptest.NewRequest("GET", "/transmission/rpc", nil)
	req.SetBasicAuth("guesty", "guestpass1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("guest GET probe: expected 401, got %d (body %s)", w.Code, w.Body.String())
	}
	if sid := w.Header().Get(headerTransmissionSessionID); sid != "" {
		t.Fatalf("guest must not be handed a session-id, got %q", sid)
	}

	// POST RPC with guest Basic Auth → 401 + error result, never dispatched.
	req2 := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(`{"method":"session-get","arguments":{}}`))
	req2.SetBasicAuth("guesty", "guestpass1")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("guest POST: expected 401, got %d (body %s)", w2.Code, w2.Body.String())
	}
	var rr rpcResponse
	if err := json.NewDecoder(w2.Body).Decode(&rr); err != nil {
		t.Fatalf("decode 401 body: %v", err)
	}
	if rr.Result == "success" {
		t.Fatal("guest RPC must not succeed")
	}

	// Control: the regular user still gets the normal 409 handshake.
	req3 := httptest.NewRequest("GET", "/transmission/rpc", nil)
	req3.SetBasicAuth("regular", "regularpass1")
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusConflict {
		t.Fatalf("regular user handshake: expected 409, got %d", w3.Code)
	}
}

// ─── (b) per-user scoping of torrent-get / torrent-remove / torrent-stop ───

// A non-admin RPC identity must only see and mutate its OWN download rows.
// alice must not see bob's rows via torrent-get, and removing/stopping by
// bob's torrent id must leave bob's row untouched.
func TestRPC_UserScoping_TorrentGetAndMutations(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	st := newTestStore(t)
	as, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	aliceID, err := as.CreateUser("alice", "alicepass1", auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := as.CreateUser("bob", "bobpass1!", auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	dAlice := mkUserRow(t, st, aliceID, '1')
	dBob := mkUserRow(t, st, bobID, '2')

	h := NewHandler(st, nil, as, "/data", "/data", "", nil)
	router := gin.New()
	h.RegisterRoutes(router)

	sid := rpcHandshake(t, router, "alice", "alicepass1")

	// torrent-get as alice → only alice's row.
	w := rpcPost(t, router, sid, `{"method":"torrent-get","arguments":{"fields":["id"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("torrent-get: HTTP %d (body %s)", w.Code, w.Body.String())
	}
	ids := torrentIDs(t, w)
	if len(ids) != 1 || ids[0] != dAlice.ID {
		t.Fatalf("alice torrent-get: expected only her row %d, got %v (bob's row %d leaked)", dAlice.ID, ids, dBob.ID)
	}

	// torrent-remove as alice targeting bob's row → bob's row must survive.
	w = rpcPost(t, router, sid, fmt.Sprintf(`{"method":"torrent-remove","arguments":{"ids":[%d]}}`, dBob.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("torrent-remove: HTTP %d", w.Code)
	}
	if _, err := st.Get(bobID, dBob.ID); err != nil {
		t.Fatalf("alice removed bob's download %d: %v", dBob.ID, err)
	}

	// torrent-stop as alice targeting bob's row → bob's status unchanged.
	before, err := st.Get(bobID, dBob.ID)
	if err != nil {
		t.Fatal(err)
	}
	w = rpcPost(t, router, sid, fmt.Sprintf(`{"method":"torrent-stop","arguments":{"ids":[%d]}}`, dBob.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("torrent-stop: HTTP %d", w.Code)
	}
	after, err := st.Get(bobID, dBob.ID)
	if err != nil {
		t.Fatalf("bob's row vanished after alice's torrent-stop: %v", err)
	}
	if after.Status != before.Status {
		t.Fatalf("alice's torrent-stop mutated bob's row: status %q → %q", before.Status, after.Status)
	}

	// Control: bob sees exactly his own row.
	sidBob := rpcHandshake(t, router, "bob", "bobpass1!")
	w = rpcPost(t, router, sidBob, `{"method":"torrent-get","arguments":{"fields":["id"]}}`)
	ids = torrentIDs(t, w)
	if len(ids) != 1 || ids[0] != dBob.ID {
		t.Fatalf("bob torrent-get: expected only his row %d, got %v", dBob.ID, ids)
	}

	// Control: an admin keeps full cross-user visibility.
	if _, err := as.CreateUser("root", "rootpass1!", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	sidAdmin := rpcHandshake(t, router, "root", "rootpass1!")
	w = rpcPost(t, router, sidAdmin, `{"method":"torrent-get","arguments":{"fields":["id"]}}`)
	ids = torrentIDs(t, w)
	if len(ids) != 2 {
		t.Fatalf("admin torrent-get: expected both rows, got %v", ids)
	}
}

// ─── (d) session-set / session-get race ────────────────────────────────────

// session-set (writes) and session-get (reads) mutate/read the alt-speed and
// queue fields with no lock — the *arr session-get poll races the UI
// session-set. Run under `go test -race`: two goroutines hammer both sides.
func TestSessionSetGet_ConcurrentNoRace(t *testing.T) {
	h := NewHandler(nil, nil, nil, t.TempDir(), t.TempDir(), "", nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			h.methodSessionSet(map[string]interface{}{
				keyAltSpeedEn:    i%2 == 0,
				keyAltSpeedDown:  float64(i),
				keyAltSpeedUp:    float64(i + 1),
				keyStartAdded:    i%2 == 1,
				keyDLQueueEn:     i%3 == 0,
				keyDLQueueSize:   float64(i),
				keySeedQueueEn:   i%4 == 0,
				keySeedQueueSize: float64(i),
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if resp := h.methodSessionGet(); resp.Result != "success" {
				t.Errorf("session-get: %q", resp.Result)
			}
		}
	}()
	wg.Wait()
}

// ─── (e) torrent-remove delete-local-data really deletes ───────────────────

// delete-local-data must actually remove the download's files when the row's
// file_path resolves inside the download roots — and must leave files alone
// when the path is outside those roots (row deletion still succeeds).
func TestTorrentRemove_DeleteLocalData_RemovesConfinedFiles(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	st := newTestStore(t)
	root := t.TempDir()

	inside := filepath.Join(root, "release.mkv")
	if err := os.WriteFile(inside, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir() // deliberately NOT a download root
	outside := filepath.Join(outsideDir, "keepme.mkv")
	if err := os.WriteFile(outside, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	dIn, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("1", 40), FileIndex: -1,
		FilePath: inside, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("1", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	dOut, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("2", 40), FileIndex: -1,
		FilePath: outside, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("2", 40),
	})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(st, nil, nil, root, root, "", nil)
	router := gin.New()
	h.RegisterRoutes(router)

	body := fmt.Sprintf(`{"method":"torrent-remove","arguments":{"ids":[%d,%d],"delete-local-data":true}}`, dIn.ID, dOut.ID)
	w := rpcPost(t, router, "", body)
	if w.Code != http.StatusOK {
		t.Fatalf("torrent-remove: HTTP %d (body %s)", w.Code, w.Body.String())
	}

	// Both rows are gone.
	all, _ := st.ListAll()
	if len(all) != 0 {
		t.Fatalf("expected 0 rows after remove, got %d", len(all))
	}
	// The confined file was actually deleted from disk.
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("delete-local-data: confined file %q still exists (err=%v)", inside, err)
	}
	// The outside-roots file survives.
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("delete-local-data deleted %q which is outside the download roots", outside)
	}
}
