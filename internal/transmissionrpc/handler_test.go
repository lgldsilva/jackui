package transmissionrpc

import (
	"bytes"
	"encoding/base64"
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
	"github.com/luizg/jackui/internal/streamer"
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

	resp := h.dispatch(rpcRequest{Method: "port-test"}, 0)
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	open, ok := resp.Arguments["port-is-open"].(bool)
	if !ok {
		t.Fatalf("expected port-is-open bool, got %T", resp.Arguments["port-is-open"])
	}
	// Initially false because no external check has been performed yet.
	// A real check runs async when the cache is stale (>60s).
	if open {
		t.Log("port-test returned true (async check completed or cached)")
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
	_ = h.buildTorrent(d, nil, nil, map[string]bool{"id": true, "activityDate": true, "secondsDownloading": true})
}

// allTorrentFields cobre todos os campos suportados pelo buildTorrent (core +
// extras), garantindo que o split do switch não derruba nenhum campo.
var allTorrentFields = []string{
	"id", "hashString", "name", "status", "totalSize", "percentDone",
	"rateDownload", "rateUpload", "downloadDir", "addedDate", "doneDate",
	"error", "errorString", "leftUntilDone", "haveValid", "peersConnected",
	"eta", "isFinished", "isStalled", "labels", "trackers", "uploadRatio",
	"uploadedEver", "downloadedEver", "queuePosition",
	"activityDate", "corruptEver", "desiredAvailable", "haveUnchecked",
	"peersGettingFromUs", "peersSendingToUs", "seedRatioLimit", "seedRatioMode",
	"sizeWhenDone", "startDate", "torrentFile", "maxConnectedPeers",
	"bandwidthPriority", "recheckProgress", "secondsDownloading",
	"secondsSeeding", "comment", "creator", "dateCreated", "pieceCount",
	"pieceSize", "priorities", "wanted", "files", "fileStats",
	"magnetLink", "metadataPercentComplete", "editDate", "fileCount",
	"percentComplete", "peers", "trackerList", "trackerStats",
	"pieces", "availability",
	"etaIdle", "honorsSessionLimits", "isPrivate",
	"peerLimit", "primaryMimeType", "sequentialDownload",
	"downloadLimit", "downloadLimited",
	"uploadLimit", "uploadLimited",
	"seedIdleLimit", "seedIdleMode", "peersFrom",
	"bytesCompleted", "webseeds", "webseedsSendingToUs", "group",
	"manualAnnounceTime",
}

func TestBuildTorrent_AllFieldsPresent(t *testing.T) {
	h := &Handler{dataDir: "/data", downloadDir: "/downloads", sessions: make(map[string]int)}
	started := time.Now().Add(-time.Hour)
	now := time.Now()
	d := downloads.Download{
		ID: 7, InfoHash: "abc", Name: "Movie", Status: downloads.StatusCompleted,
		Progress: 1.0, FileSize: 1000, BytesDownloaded: 1000, Category: "movies",
		Tracker: "https://tr.example", CreatedAt: started, StartedAt: &started,
		CompletedAt: &now,
	}

	fields := make(map[string]bool, len(allTorrentFields))
	for _, f := range allTorrentFields {
		fields[f] = true
	}

	got := h.buildTorrent(d, nil, nil, fields)

	if len(got) != len(allTorrentFields) {
		t.Fatalf("got %d fields, want %d", len(got), len(allTorrentFields))
	}
	for _, f := range allTorrentFields {
		if _, ok := got[f]; !ok {
			t.Errorf("missing field %q", f)
		}
	}
	if got["id"] != 7 {
		t.Errorf("id=%v, want 7", got["id"])
	}
	if got["downloadDir"] != "/downloads" {
		t.Errorf("downloadDir=%v, want /downloads", got["downloadDir"])
	}
}

// ─── session-set ───────────────────────────────────────────────────────────

func TestSessionSet_AltSpeed(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodSessionSet(map[string]interface{}{
		"alt-speed-enabled": true,
		"alt-speed-down":    float64(100),
		"alt-speed-up":      float64(200),
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	if !h.altSpeedEnabled {
		t.Error("altSpeedEnabled should be true")
	}
	if h.altSpeedDown != 100 {
		t.Errorf("altSpeedDown=%d, want 100", h.altSpeedDown)
	}
	if h.altSpeedUp != 200 {
		t.Errorf("altSpeedUp=%d, want 200", h.altSpeedUp)
	}

	// Verify the values propagate to session-get
	sg := h.methodSessionGet()
	if sg.Arguments["alt-speed-enabled"] != true {
		t.Error("session-get alt-speed-enabled should be true")
	}
}

func TestSessionSet_QueueSettings(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodSessionSet(map[string]interface{}{
		"download-queue-enabled": false,
		"download-queue-size":    float64(3),
		"seed-queue-enabled":     true,
		"seed-queue-size":        float64(5),
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	if h.downloadQueueEnabled {
		t.Error("downloadQueueEnabled should be false")
	}
	if h.downloadQueueSize != 3 {
		t.Errorf("downloadQueueSize=%d, want 3", h.downloadQueueSize)
	}
	if !h.seedQueueEnabled {
		t.Error("seedQueueEnabled should be true")
	}
	if h.seedQueueSize != 5 {
		t.Errorf("seedQueueSize=%d, want 5", h.seedQueueSize)
	}
}

func TestSessionSet_StartAdded(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodSessionSet(map[string]interface{}{
		"start-added-torrents": false,
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	if h.startAddedTorrents {
		t.Error("startAddedTorrents should be false")
	}
}

func TestSessionSet_NoArgs(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodSessionSet(map[string]interface{}{})
	if resp.Result != "success" {
		t.Fatalf("expected success with empty args, got %q", resp.Result)
	}
}

// ─── session-close ────────────────────────────────────────────────────────

func TestSessionClose(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodSessionClose()
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── torrent-start / stop / start-now ─────────────────────────────────────

func TestTorrentStart_ChangesStatus(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("c", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("c", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetStatus(1, d.ID, downloads.StatusPaused)

	resp := h.methodTorrentStart(map[string]interface{}{
		"ids": []interface{}{float64(d.ID)},
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	got, err := st.Get(1, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	// torrent-start re-queues; the scheduler promotes to downloading (active limit).
	if got.Status != downloads.StatusQueued {
		t.Errorf("status=%q, want %q", got.Status, downloads.StatusQueued)
	}
}

func TestTorrentStop_ChangesStatus(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("d", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("d", 40),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := h.methodTorrentStop(map[string]interface{}{
		"ids": []interface{}{float64(d.ID)},
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	got, err := st.Get(1, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != downloads.StatusPaused {
		t.Errorf("status=%q, want %q", got.Status, downloads.StatusPaused)
	}
}

func TestTorrentStartNow_SameAsStart(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("e", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("e", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetStatus(1, d.ID, downloads.StatusPaused)

	_ = h.methodTorrentStartNow(map[string]interface{}{
		"ids": []interface{}{float64(d.ID)},
	})

	got, err := st.Get(1, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != downloads.StatusQueued {
		t.Errorf("torrent-start-now: status=%q, want %q", got.Status, downloads.StatusQueued)
	}
}

func TestTorrentStart_OmitsCompleted(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("f", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("f", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetStatus(1, d.ID, downloads.StatusCompleted)

	resp := h.methodTorrentStart(map[string]interface{}{
		"ids": []interface{}{float64(d.ID)},
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	got, err := st.Get(1, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != downloads.StatusCompleted {
		t.Errorf("completed download should not be restarted, status=%q", got.Status)
	}
}

func TestTorrentStart_AllWhenNoIDs(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	_, _ = st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("g", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("g", 40),
	})
	d2, _ := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("h", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("h", 40),
	})
	_ = st.SetStatus(1, d2.ID, downloads.StatusPaused)

	_ = h.methodTorrentStart(nil)

	all, _ := st.ListAll()
	paused := 0
	for _, d := range all {
		if d.Status == downloads.StatusPaused {
			paused++
		}
	}
	if paused > 0 {
		t.Errorf("%d downloads still paused after start-all", paused)
	}
}

// ─── torrent-verify ───────────────────────────────────────────────────────

func TestTorrentVerify_NoStreamer_ReturnsSuccess(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentVerify(map[string]interface{}{
		"ids": []interface{}{float64(1)},
	})
	if resp.Result != "success" {
		t.Errorf("expected success without streamer, got %q", resp.Result)
	}
}

func TestTorrentVerify_NoIDs_ReturnsSuccess(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentVerify(nil)
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── torrent-reannounce ───────────────────────────────────────────────────

func TestTorrentReannounce_NoStreamer_ReturnsSuccess(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentReannounce(map[string]interface{}{
		"ids": []interface{}{float64(1)},
	})
	if resp.Result != "success" {
		t.Errorf("expected success without streamer, got %q", resp.Result)
	}
}

func TestTorrentReannounce_NoIDs_ReturnsSuccess(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentReannounce(nil)
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── queue-move-* ─────────────────────────────────────────────────────────

func TestQueueMove_Top_ReturnsSuccess(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodQueueMove(map[string]interface{}{
		"ids": []interface{}{float64(1)},
	}, "top")
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

func TestQueueMove_Up_Down_Bottom(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	for _, dir := range []string{"up", "down", "bottom"} {
		resp := h.methodQueueMove(map[string]interface{}{
			"ids": []interface{}{float64(1)},
		}, dir)
		if resp.Result != "success" {
			t.Errorf("queue-move-%s: expected success, got %q", dir, resp.Result)
		}
	}
}

// ─── dispatch coverage ────────────────────────────────────────────────────

func TestDispatch_NewMethods_ReturnSuccess(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	methods := []string{
		"session-set", "session-close",
		"torrent-start", "torrent-stop", "torrent-start-now",
		"torrent-verify", "torrent-reannounce",
		"group-get",
		"queue-move-top", "queue-move-up", "queue-move-down", "queue-move-bottom",
	}
	for _, m := range methods {
		resp := h.dispatch(rpcRequest{Method: m, Arguments: map[string]interface{}{}}, 0)
		if resp.Result != "success" {
			t.Errorf("dispatch(%q) = %q, want success", m, resp.Result)
		}
	}
}

// ─── forEachDownload ──────────────────────────────────────────────────────

func TestForEachDownload_NoStore(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	called := false
	resp := h.forEachDownload(nil, func(d downloads.Download) error {
		called = true
		return nil
	})
	if resp.Result != "success" {
		t.Errorf("expected success with nil store, got %q", resp.Result)
	}
	if called {
		t.Error("fn should not be called with nil store")
	}
}

func TestForEachDownload_WithStore(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	_, _ = st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("i", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("i", 40),
	})
	_, _ = st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("j", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("j", 40),
	})

	var visited []int
	resp := h.forEachDownload(nil, func(d downloads.Download) error {
		visited = append(visited, d.ID)
		return nil
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	if len(visited) != 2 {
		t.Errorf("visited %d downloads, want 2", len(visited))
	}
}

// ─── hashFromDownload ─────────────────────────────────────────────────────

func TestHashFromDownload_Valid(t *testing.T) {
	hash := strings.Repeat("a", 40)
	d := downloads.Download{InfoHash: hash}
	hh, err := hashFromDownload(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hh.HexString() != hash {
		t.Errorf("hash=%q, want %q", hh.HexString(), hash)
	}
}

func TestHashFromDownload_Invalid(t *testing.T) {
	d := downloads.Download{InfoHash: "not-a-hex-hash"}
	_, err := hashFromDownload(d)
	if err == nil {
		t.Error("expected error for invalid hash")
	}
}

// ─── NewHandler defaults ──────────────────────────────────────────────────

func TestNewHandler_Defaults(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	if !h.startAddedTorrents {
		t.Error("startAddedTorrents defaults to true")
	}
	if !h.downloadQueueEnabled {
		t.Error("downloadQueueEnabled defaults to true")
	}
	if h.downloadQueueSize != 5 {
		t.Errorf("downloadQueueSize=%d, want 5", h.downloadQueueSize)
	}
	if h.altSpeedDown != 50 {
		t.Errorf("altSpeedDown=%d, want 50", h.altSpeedDown)
	}
}

// ─── group-get / group-set ─────────────────────────────────────────────────

func TestGroupGet_Default(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodGroupGet(nil)
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	groups, ok := resp.Arguments["group"].([]interface{})
	if !ok || len(groups) != 1 {
		t.Fatalf("expected 1 group, got %v", resp.Arguments["group"])
	}
	g := groups[0].(map[string]interface{})
	if g["name"] != "Default" {
		t.Errorf("group name=%q, want Default", g["name"])
	}
}

func TestGroupGet_WithNameFilter(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	// Unknown name should return empty
	resp := h.methodGroupGet(map[string]interface{}{
		"name": "NonExistent",
	})
	groups, ok := resp.Arguments["group"].([]interface{})
	if !ok || len(groups) != 0 {
		t.Errorf("expected 0 groups for unknown name, got %d", len(groups))
	}

	// "Default" should return the group
	resp = h.methodGroupGet(map[string]interface{}{
		"name": "Default",
	})
	groups, _ = resp.Arguments["group"].([]interface{})
	if len(groups) != 1 {
		t.Errorf("expected 1 group for Default, got %d", len(groups))
	}
}

func TestGroupSet_MissingName(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodGroupSet(map[string]interface{}{
		"speed-limit-down": float64(500),
	})
	if resp.Result == "success" {
		t.Error("expected failure for missing name")
	}
}

func TestGroupSet_Default(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodGroupSet(map[string]interface{}{
		"name":             "Default",
		"speed-limit-down": float64(1000),
		"speed-limit-up":   float64(500),
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
}

// ─── trackerHost ───────────────────────────────────────────────────────────

func TestTrackerHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://tracker.example.com:443/announce", "tracker.example.com"},
		{"udp://tracker.opentrackr.org:1337", "tracker.opentrackr.org"},
		{"http://192.168.1.1:6969/announce", "192.168.1.1"},
		{"", ""},
	}
	for _, tc := range tests {
		got := trackerHost(tc.input)
		if got != tc.want {
			t.Errorf("trackerHost(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ─── buildFiles / buildFileStats / buildPriorities / buildWanted ────────────

func TestBuildFiles_Empty(t *testing.T) {
	v := torrentView{
		d: downloads.Download{Name: "test", BytesDownloaded: 500},
		totalSize: 1000,
		files: nil,
	}
	result := buildFiles(v)
	if len(result) != 1 {
		t.Fatalf("expected 1 stub file, got %d", len(result))
	}
	f := result[0].(map[string]interface{})
	if f["name"] != "test" {
		t.Errorf("file name=%q, want test", f["name"])
	}
	if f["bytesCompleted"] != int64(500) {
		t.Errorf("bytesCompleted=%v, want 500", f["bytesCompleted"])
	}
}

func TestBuildFiles_WithStreamerFiles(t *testing.T) {
	v := torrentView{
		files: []streamer.FileInfo{
			{Index: 0, Path: "file1.mkv", Size: 1000, Downloaded: 500},
			{Index: 1, Path: "file2.mp4", Size: 2000, Downloaded: 200},
		},
	}
	result := buildFiles(v)
	if len(result) != 2 {
		t.Fatalf("expected 2 files, got %d", len(result))
	}
	f0 := result[0].(map[string]interface{})
	if f0["name"] != "file1.mkv" {
		t.Errorf("file[0].name=%q, want file1.mkv", f0["name"])
	}
	if f0["bytesCompleted"] != int64(500) {
		t.Errorf("file[0].bytesCompleted=%v, want 500", f0["bytesCompleted"])
	}
}

func TestBuildFileStats_WithPriorities(t *testing.T) {
	v := torrentView{
		files: []streamer.FileInfo{
			{Index: 0, Path: "f1.mkv", Size: 1000, Downloaded: 1000, Priority: "high"},
			{Index: 1, Path: "f2.mp4", Size: 500, Downloaded: 0, Priority: "normal"},
		},
	}
	stats := buildFileStats(v)
	if len(stats) != 2 {
		t.Fatalf("expected 2 fileStats, got %d", len(stats))
	}
	s0 := stats[0].(map[string]interface{})
	if s0["priority"] != 1 {
		t.Errorf("file[0].priority=%v, want 1 (high)", s0["priority"])
	}
	if s0["wanted"] != true {
		t.Errorf("file[0].wanted=%v, want true", s0["wanted"])
	}
}

func TestBuildPriorities_Empty(t *testing.T) {
	v := torrentView{files: nil}
	prios := buildPriorities(v)
	if len(prios) != 1 || prios[0] != 0 {
		t.Errorf("expected [0], got %v", prios)
	}
}

func TestBuildPriorities_WithLabels(t *testing.T) {
	v := torrentView{
		files: []streamer.FileInfo{
			{Index: 0, Priority: "high"},
			{Index: 1, Priority: "low"},
			{Index: 2, Priority: "normal"},
		},
	}
	prios := buildPriorities(v)
	if len(prios) != 3 {
		t.Fatalf("expected 3 priorities, got %d", len(prios))
	}
	if prios[0] != 1 {
		t.Errorf("prio[0]=%v, want 1 (high)", prios[0])
	}
	if prios[1] != -1 {
		t.Errorf("prio[1]=%v, want -1 (low)", prios[1])
	}
	if prios[2] != 0 {
		t.Errorf("prio[2]=%v, want 0 (normal)", prios[2])
	}
}

func TestBuildWanted_Empty(t *testing.T) {
	v := torrentView{files: nil}
	w := buildWanted(v)
	if len(w) != 1 || w[0] != 1 {
		t.Errorf("expected [1], got %v", w)
	}
}

func TestBuildWanted_WithFiles(t *testing.T) {
	v := torrentView{
		files: []streamer.FileInfo{
			{Index: 0, Size: 1000, Progress: 0.5, Downloaded: 500},
			{Index: 1, Size: 2000, Progress: 0, Downloaded: 0},
		},
	}
	w := buildWanted(v)
	if len(w) != 2 {
		t.Fatalf("expected 2 wanted, got %d", len(w))
	}
	if w[0] != 1 {
		t.Errorf("wanted[0]=%v, want 1 (downloading)", w[0])
	}
	if w[1] != 0 {
		t.Errorf("wanted[1]=%v, want 0 (not started)", w[1])
	}
}

// ─── buildPeers ────────────────────────────────────────────────────────────

func TestBuildPeers_NoTorrentObj(t *testing.T) {
	v := torrentView{torrentObj: nil}
	peers := buildPeers(v)
	if len(peers) != 0 {
		t.Errorf("expected 0 peers without torrentObj, got %d", len(peers))
	}
}

// ─── activeTorrentObjects ──────────────────────────────────────────────────

func TestActiveTorrentObjects_NoStreamer(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	result := h.activeTorrentObjects(nil)
	if len(result) != 0 {
		t.Errorf("expected empty map with nil streamer, got %d items", len(result))
	}
}

// ─── newTorrentView (magnetLink, trackerStats) ─────────────────────────────

func TestNewTorrentView_MagnetLink(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	v := h.newTorrentView(downloads.Download{
		InfoHash: "abcdef0123456789abcdef0123456789abcdef01",
		Magnet:   "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01&dn=MyFile",
	}, nil, nil)
	if v.magnetLink != "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01&dn=MyFile" {
		t.Errorf("magnetLink=%q, want original magnet", v.magnetLink)
	}
}

func TestNewTorrentView_MagnetLink_Fallback(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	v := h.newTorrentView(downloads.Download{
		InfoHash: "abcdef0123456789abcdef0123456789abcdef01",
	}, nil, nil)
	want := "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01"
	if v.magnetLink != want {
		t.Errorf("magnetLink=%q, want %q", v.magnetLink, want)
	}
}

func TestNewTorrentView_Trackers(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	v := h.newTorrentView(downloads.Download{
		InfoHash: "abcdef0123456789abcdef0123456789abcdef01",
		Tracker:  "https://tracker.example.com/announce",
	}, &streamer.TorrentInfo{
		Trackers: []string{
			"https://tracker.example.com/announce",
			"udp://tracker2.example:1337",
		},
	}, nil)
	if len(v.trackers) != 2 {
		t.Errorf("expected 2 trackers, got %d", len(v.trackers))
	}
	if len(v.trackerStats) != 2 {
		t.Errorf("expected 2 trackerStats, got %d", len(v.trackerStats))
	}
	if v.trackerList == "" {
		t.Error("trackerList should not be empty")
	}
}

// ─── torrent-set enhanced args ─────────────────────────────────────────────

func TestTorrentSet_BandwidthPriority(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	// bandwidthPriority only applies when streamer is available; without it
	// the call is a no-op (should still return success).
	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":               []interface{}{float64(1)},
		"bandwidthPriority": float64(1),
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

func TestTorrentSet_TrackerListOld(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("k", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("k", 40),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":         []interface{}{float64(d.ID)},
		"trackerList": "https://new-tracker.example/announce\nhttps://backup.example/announce",
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
}

func TestTorrentSet_PeerLimit(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	// Without streamer, peer limit is a no-op (but still returns success).
	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":       []interface{}{float64(1)},
		"peerLimit": float64(100),
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── newTorrentView with si.Files ──────────────────────────────────────────

func TestNewTorrentView_FileInfo(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	si := &streamer.TorrentInfo{
		Name:      "TestTorrent",
		TotalSize: 3000,
		Files: []streamer.FileInfo{
			{Index: 0, Path: "video.mkv", Size: 2000, Downloaded: 1000, Progress: 0.5, Priority: "normal"},
			{Index: 1, Path: "sub.srt", Size: 1000, Downloaded: 0, Progress: 0.0, Priority: "low"},
		},
	}
	v := h.newTorrentView(downloads.Download{}, si, nil)
	if len(v.files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(v.files))
	}
	if v.files[0].Path != "video.mkv" {
		t.Errorf("file[0].Path=%q, want video.mkv", v.files[0].Path)
	}
	if v.metadataComplete != 1.0 {
		t.Errorf("metadataComplete=%v, want 1.0", v.metadataComplete)
	}
}

// ─── JSON-RPC 2.0 protocol ─────────────────────────────────────────────────

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"download-dir", "download_dir"},
		{"rpc-version-semver", "rpc_version_semver"},
		{"seedRatioLimit", "seed_ratio_limit"},
		{"cumulative-stats", "cumulative_stats"},
		{"downloadedBytes", "downloaded_bytes"},
		{"rpc_version", "rpc_version"},
		{"preferred_transports", "preferred_transports"},
		{"", ""},
		{"single", "single"},
		{"ABC", "a_b_c"},
	}
	for _, tc := range tests {
		got := toSnakeCase(tc.input)
		if got != tc.want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSnakeToKebab(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"session_get", "session-get"},
		{"torrent_add", "torrent-add"},
		{"download_dir", "download-dir"},
		{"simple", "simple"},
	}
	for _, tc := range tests {
		got := snakeToKebab(tc.input)
		if got != tc.want {
			t.Errorf("snakeToKebab(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestConvertMapKeys_Simple(t *testing.T) {
	input := map[string]interface{}{
		"download-dir": "/data",
		"speed-limit":  float64(100),
	}
	result := convertMapKeys(input, toSnakeCase).(map[string]interface{})
	if result["download_dir"] != "/data" {
		t.Errorf("missing download_dir, got %v", result)
	}
	if _, ok := result["download-dir"]; ok {
		t.Error("download-dir should have been converted")
	}
}

func TestConvertMapKeys_Nested(t *testing.T) {
	input := map[string]interface{}{
		"cumulative-stats": map[string]interface{}{
			"downloadedBytes": float64(1000),
			"uploadedBytes":   float64(500),
		},
	}
	result := convertMapKeys(input, toSnakeCase).(map[string]interface{})
	stats, ok := result["cumulative_stats"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected cumulative_stats map, got %v", result)
	}
	if stats["downloaded_bytes"] != float64(1000) {
		t.Errorf("downloaded_bytes=%v, want 1000", stats["downloaded_bytes"])
	}
}

func TestConvertMapKeys_Array(t *testing.T) {
	input := map[string]interface{}{
		"trackers": []interface{}{
			map[string]interface{}{
				"announce": "http://tracker.example",
				"sitename": "",
			},
		},
	}
	result := convertMapKeys(input, toSnakeCase).(map[string]interface{})
	trackers := result["trackers"].([]interface{})
	if len(trackers) != 1 {
		t.Fatalf("expected 1 tracker, got %d", len(trackers))
	}
}

func TestHandleJSONRPC_SessionGet(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	body := `{"jsonrpc":"2.0","method":"session_get","params":{},"id":1}`
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router := gin.New()
	h.RegisterRoutes(router)
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var jr jsonRPCResp
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		t.Fatalf("failed to decode JSON-RPC response: %v", err)
	}
	if jr.JSONRPC != "2.0" {
		t.Errorf("jsonrpc=%q, want 2.0", jr.JSONRPC)
	}
	if jr.ID != float64(1) {
		t.Errorf("id=%v, want 1", jr.ID)
	}
	result, ok := jr.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %v", jr.Result)
	}
	if result["version"] != "4.1.1" {
		t.Errorf("version=%v, want 4.1.1", result["version"])
	}
	// Verify keys are in snake_case
	if _, ok := result["download_dir"]; !ok {
		t.Errorf("expected snake_case key 'download_dir' in result, got keys: %v", keysOf(result))
	}
	if jr.Error != nil {
		t.Errorf("unexpected error: %+v", jr.Error)
	}
}

func TestHandleJSONRPC_Notification(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	// Notification = no "id" field
	body := `{"jsonrpc":"2.0","method":"session_get","params":{}}`
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router := gin.New()
	h.RegisterRoutes(router)
	router.ServeHTTP(resp, req)

	// Notifications should return 204 No Content
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for notification, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestHandleJSONRPC_UnknownMethod(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	body := `{"jsonrpc":"2.0","method":"nonexistent","params":{},"id":1}`
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router := gin.New()
	h.RegisterRoutes(router)
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var jr jsonRPCResp
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if jr.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if jr.Error.Code != 1 {
		t.Errorf("error code=%d, want 1", jr.Error.Code)
	}
}

func TestHandleJSONRPC_ParseError(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	body := `not valid json`
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router := gin.New()
	h.RegisterRoutes(router)
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
}

func TestHandleJSONRPC_SnakeCaseMethod(t *testing.T) {
	// Ensure snake_case method names are converted to kebab-case internally.
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	body := `{"jsonrpc":"2.0","method":"torrent_start","params":{"ids":[1]},"id":2}`
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/transmission/rpc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router := gin.New()
	h.RegisterRoutes(router)
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var jr jsonRPCResp
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if jr.Error != nil {
		t.Errorf("unexpected error for torrent_start: %+v", jr.Error)
	}
}

// keysOf is a helper to list map keys for error messages.
func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ─── buildPieces ───────────────────────────────────────────────────────────

func TestBuildPieces_NoTorrentObj(t *testing.T) {
	v := torrentView{torrentObj: nil}
	result := buildPieces(v)
	if result != "" {
		t.Errorf("expected '' without torrentObj, got %q", result)
	}
}

// ─── torrentView with download stats ───────────────────────────────────────

func TestBuildTorrent_UploadRatio_NoTorrent(t *testing.T) {
	h := &Handler{dataDir: "/data", downloadDir: "/downloads", sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	d := downloads.Download{
		ID: 5, InfoHash: "abc123", Name: "Test",
		Status: downloads.StatusCompleted,
		CreatedAt: time.Now(),
	}

	fields := map[string]bool{"uploadRatio": true, "uploadedEver": true, "downloadedEver": true}
	result := h.buildTorrent(d, nil, nil, fields)

	if result["uploadRatio"] != 0.0 {
		t.Errorf("uploadRatio=%v, want 0.0", result["uploadRatio"])
	}
	if result["uploadedEver"] != int64(0) {
		t.Errorf("uploadedEver=%v, want 0", result["uploadedEver"])
	}
	if result["downloadedEver"] != int64(0) {
		t.Errorf("downloadedEver=%v, want 0", result["downloadedEver"])
	}
}

// ─── torrent-add metainfo ───────────────────────────────────────────────────

func TestTorrentAdd_Metainfo_InvalidBase64(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentAdd(map[string]interface{}{
		"metainfo": "not-valid-base64!!!",
	}, 1)
	if resp.Result == "success" {
		t.Error("expected failure for invalid base64")
	}
}

func TestTorrentAdd_Metainfo_InvalidTorrent(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	// Valid base64 but not a .torrent → parse error
	b64 := base64.StdEncoding.EncodeToString([]byte("not a torrent"))
	resp := h.methodTorrentAdd(map[string]interface{}{
		"metainfo": b64,
	}, 1)
	if resp.Result == "success" {
		t.Error("expected failure for invalid .torrent")
	}
}

func TestTorrentAdd_MissingBothFilenameAndMetainfo(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentAdd(map[string]interface{}{}, 1)
	if resp.Result == "success" {
		t.Error("expected failure when both filename and metainfo are missing")
	}
}

func TestTorrentAdd_Metainfo_Labels(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	// Test that labels override category from download-dir. We can't easily
	// create a valid .torrent in a unit test without the full bencode/marshal
	// pipeline, so we verify the behavior indirectly: labels are passed but
	// since our metainfo parse fails (no streamer to call ImportTorrentBytes),
	// we test that the error path works with labels.
	// The actual metainfo happy path requires a streamer to call ImportTorrentBytes.
	b64 := base64.StdEncoding.EncodeToString([]byte("not-a-valid-torrent"))
	resp := h.methodTorrentAdd(map[string]interface{}{
		"metainfo": b64,
		"labels":   []interface{}{"tv-sonarr"},
	}, 1)
	if resp.Result == "success" {
		t.Error("expected failure with invalid metainfo and nil streamer")
	}
}

// ─── torrent-remove delete-local-data ───────────────────────────────────────

func TestTorrentRemove_DeleteLocalData_NoStreamer(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("z", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("z", 40),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := h.methodTorrentRemove(map[string]interface{}{
		"ids":               []interface{}{float64(d.ID)},
		"delete-local-data": true,
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	all, _ := st.ListAll()
	if len(all) != 0 {
		t.Errorf("expected 0 downloads after remove, got %d", len(all))
	}
}

func TestTorrentRemove_DeleteLocalData_False(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("y", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("y", 40),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := h.methodTorrentRemove(map[string]interface{}{
		"ids": []interface{}{float64(d.ID)},
	})
	if resp.Result != "success" {
		t.Fatalf("expected success, got %q", resp.Result)
	}
	all, _ := st.ListAll()
	if len(all) != 0 {
		t.Errorf("expected 0 downloads, got %d", len(all))
	}
}

// ─── torrent-set-location with move ─────────────────────────────────────────

func TestTorrentSetLocation_WithPath(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("w", 40),
		FileIndex: -1, Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("w", 40),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := h.methodTorrentSetLocation(map[string]interface{}{
		"ids":      []interface{}{float64(d.ID)},
		"location": "/data/new/location",
		"move":     true,
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
	got, err := st.Get(1, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FilePath != "/data/new/location" {
		t.Errorf("filePath=%q, want /data/new/location", got.FilePath)
	}
}

// Segurança: set-location com path FORA dos diretórios permitidos (traversal)
// deve ser rejeitado e NÃO alterar o file_path persistido.
func TestTorrentSetLocation_RejectsTraversal(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	d, err := st.Create(downloads.Download{
		UserID: 1, InfoHash: strings.Repeat("x", 40),
		FileIndex: -1, FilePath: "/data/orig",
		Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("x", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{"/etc", "/data/../etc/passwd", "../../root"} {
		resp := h.methodTorrentSetLocation(map[string]interface{}{
			"ids":      []interface{}{float64(d.ID)},
			"location": loc,
		})
		if resp.Result == "success" {
			t.Errorf("location %q deveria ser rejeitada (fora do downloadDir)", loc)
		}
	}
	got, _ := st.Get(1, d.ID)
	if got.FilePath != "/data/orig" {
		t.Errorf("file_path foi alterado por path traversal: %q", got.FilePath)
	}
}

func TestTorrentSetLocation_NoLocation(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentSetLocation(map[string]interface{}{
		"ids": []interface{}{float64(1)},
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── torrent-set speed limits ───────────────────────────────────────────────

func TestTorrentSet_SpeedLimits(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":             []interface{}{float64(1)},
		"downloadLimit":   float64(500),
		"downloadLimited": true,
		"uploadLimit":     float64(100),
		"uploadLimited":   true,
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── torrent-set tracker args ──────────────────────────────────────────────

func TestTorrentSet_TrackerAdd(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":        []interface{}{float64(1)},
		"trackerAdd": []interface{}{"udp://new-tracker.example:1337"},
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

func TestTorrentSet_TrackerReplace(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":            []interface{}{float64(1)},
		"trackerReplace": []interface{}{[]interface{}{float64(0), "http://new-tracker.example/announce"}},
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

func TestTorrentSet_TrackerList(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":         []interface{}{float64(1)},
		"trackerList": "http://t1.example/announce\nhttp://t2.example/announce\n\nhttp://backup.example/announce",
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

func TestTorrentSet_TrackerList_Empty(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":         []interface{}{float64(1)},
		"trackerList": "",
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── torrent-set seed ratio / idle ─────────────────────────────────────────

func TestTorrentSet_SeedRatio(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":            []interface{}{float64(1)},
		"seedRatioLimit": float64(2.0),
		"seedRatioMode":  float64(1),
		"seedIdleLimit":  float64(60),
		"seedIdleMode":   float64(1),
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── torrent-set honors-session-limits / queue-position ─────────────────────

func TestTorrentSet_ExtraArgs(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":                 []interface{}{float64(1)},
		"honorsSessionLimits": true,
		"queuePosition":       float64(0),
	})
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
}

// ─── torrent-add peer-limit / bandwidth-priority ───────────────────────────

func TestTorrentAdd_PeerLimitAndPriority(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	gin.SetMode(gin.ReleaseMode)

	resp := h.methodTorrentAdd(map[string]interface{}{
		"filename":           strings.Repeat("a", 40),
		"peer-limit":         float64(100),
		"bandwidth-priority": float64(1),
	}, 1)
	if resp.Result != "success" {
		t.Errorf("expected success, got %q", resp.Result)
	}
	all, err := st.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 download, got %d", len(all))
	}
}

// ─── dispatch coverage for port-test ──────────────────────────────────────

func TestDispatch_BespokePortTest(t *testing.T) {
	h := &Handler{sessions: make(map[string]int)}
	gin.SetMode(gin.ReleaseMode)

	resp := h.dispatch(rpcRequest{Method: "port-test"}, 0)
	if resp.Result != "success" {
		t.Errorf("port-test: expected success, got %q", resp.Result)
	}
}
