package transmissionrpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/downloads"
)

func newTestStore(t *testing.T) *downloads.Store {
	t.Helper()
	st, err := downloads.New(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestSessionGet(t *testing.T) {
	h := &Handler{
		dataDir:     "/data/streams",
		downloadDir: "/data/downloads",
		sessions: make(map[string]int),
	}
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodSessionGet()
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	args := resp.Arguments
	if args["version"] != "4.1.1" {
		t.Errorf("expected version 4.1.1, got %v", args["version"])
	}
	if v, ok := args["rpc-version"]; !ok || fmt.Sprintf("%v", v) != "19" {
		t.Errorf("expected rpc-version 19, got %v", args["rpc-version"])
	}
	if v, ok := args["rpc-version-semver"]; !ok || v != "6.0.1" {
		t.Errorf("expected rpc-version-semver 6.0.1, got %v", args["rpc-version-semver"])
	}
	dd, ok := args["download-dir"].(string)
	if !ok || dd != "/data/downloads" {
		t.Errorf("expected download-dir /data/downloads, got %v", args["download-dir"])
	}
}

func TestSessionStats(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodSessionStats()
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
}

func TestPortTest(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodSessionStats()
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}

	// Also test port-test via dispatch
	resp = h.dispatch(rpcRequest{Method: "port-test"}, 0)
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	open, ok := resp.Arguments["port-is-open"].(bool)
	if !ok || !open {
		t.Errorf("expected port-is-open true")
	}
}

func TestExtractInfoHash(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01&dn=test",
			want:  "abcdef0123456789abcdef0123456789abcdef01",
		},
		{
			input: "abcdef0123456789abcdef0123456789abcdef01",
			want:  "abcdef0123456789abcdef0123456789abcdef01",
		},
		{
			input: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
			want:  "abcdef0123456789abcdef0123456789abcdef01",
		},
		{
			input: "",
			want:  "",
		},
		{
			input: "not-a-hash",
			want:  "",
		},
		{
			input: "magnet:?xt=urn:btih:deadbeefdeadbeefdeadbeefdeadbeefdeadbeef&tr=udp://tracker.example.com:1337",
			want:  "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		},
	}

	for _, tc := range tests {
		got := extractInfoHash(tc.input)
		if got != tc.want {
			t.Errorf("extractInfoHash(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestExtractCategory(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/downloads/tv-sonarr", "downloads/tv-sonarr"},
		{"/data/downloads/tv/Monsters", "tv/Monsters"},
		{"/downloads/movies", "downloads/movies"},
		{"", ""},
	}

	for _, tc := range tests {
		got := extractCategory(tc.input)
		if got != tc.want {
			t.Errorf("extractCategory(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseIDs(t *testing.T) {
	tests := []struct {
		input interface{}
		want  map[int]bool
	}{
		{nil, nil},
		{float64(5), map[int]bool{5: true}},
		{[]interface{}{float64(1), float64(2), float64(3)}, map[int]bool{1: true, 2: true, 3: true}},
	}

	for _, tc := range tests {
		got := parseIDs(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseIDs(%v) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for k := range tc.want {
			if !got[k] {
				t.Errorf("parseIDs(%v) missing key %d", tc.input, k)
			}
		}
	}
}

func TestRPCErrorOnMissingMethod(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	resp := h.dispatch(rpcRequest{Method: "non-existent-method"}, 0)
	if resp.Result == "success" {
		t.Errorf("expected error for unknown method")
	}
}

func TestRPCHandlerNoAuth(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	h.RegisterRoutes(router)

	body := `{"method":"session-get","arguments":{}}`
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if rpcResp.Result != "success" {
		t.Errorf("expected success, got %q", rpcResp.Result)
	}
}

// SSRF: a URL do torrent vem do cliente RPC; IPs internos devem ser barrados.
func TestFetchTorrentHash_BlocksInternalIP(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1/x.torrent",
		"http://169.254.169.254/latest/meta-data", // metadata cloud
		"http://127.0.0.1/x.torrent",
		"http://192.168.0.10/x.torrent",
	} {
		if _, err := fetchTorrentHash(u); err == nil {
			t.Errorf("fetchTorrentHash(%q) deveria falhar (IP interno)", u)
		}
	}
	if _, err := fetchTorrentHash("ftp://example.com/x"); err == nil {
		t.Error("esquema não-http deveria ser rejeitado")
	}
}

// torrent-add deve atribuir o download ao usuário autenticado (não ao 0).
func TestTorrentAdd_SetsUserID(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	hash := strings.Repeat("a", 40)
	resp := h.methodTorrentAdd(map[string]interface{}{
		"filename": "magnet:?xt=urn:btih:" + hash,
	}, 7)
	if resp.Result != "success" {
		t.Fatalf("torrent-add falhou: %q", resp.Result)
	}
	all, err := st.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].UserID != 7 {
		t.Fatalf("esperava 1 download com UserID=7, got %+v", all)
	}
}

// torrent-set "labels" deve atualizar a categoria do download existente
// (antes chamava Create sem Magnet e nunca funcionava).
func TestTorrentSet_Labels_UpdatesCategory(t *testing.T) {
	st := newTestStore(t)
	hash := strings.Repeat("b", 40)
	d, err := st.Create(downloads.Download{
		UserID: 3, InfoHash: hash, FileIndex: -1,
		Magnet: "magnet:?xt=urn:btih:" + hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(st, nil, nil, "/data", "/data")
	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":    []interface{}{float64(d.ID)},
		"labels": []interface{}{"tv-sonarr"},
	})
	if resp.Result != "success" {
		t.Fatalf("torrent-set: %q", resp.Result)
	}
	got, err := st.Get(3, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Category != "tv-sonarr" {
		t.Errorf("category=%q, want tv-sonarr", got.Category)
	}
}

// buildTorrent não deve dar panic quando StartedAt é nil num download completo.
func TestBuildTorrent_NilStartedAt_NoPanic(t *testing.T) {
	h := &Handler{dataDir: "/data", downloadDir: "/data", sessions: make(map[string]int)}
	now := time.Now()
	d := downloads.Download{
		ID: 1, Status: downloads.StatusCompleted,
		CreatedAt: now, CompletedAt: &now, // StartedAt nil de propósito
	}
	_ = h.buildTorrent(d, nil, map[string]bool{"id": true, "activityDate": true, "secondsDownloading": true})
}
