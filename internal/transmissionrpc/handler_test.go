package transmissionrpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

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
