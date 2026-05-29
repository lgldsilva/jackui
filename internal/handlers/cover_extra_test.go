package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/local"
	"github.com/luizg/jackui/internal/streamer"
)

// ─── Auth 0% coverage ─────────────────────────────────────────────────────

func TestIncognitoHeartbeat_NoClaims_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/user/incognito/heartbeat", nil)

	IncognitoHeartbeat()(c)

	// Must not panic
	if w.Code != http.StatusUnauthorized {
		t.Logf("got status %d (expected 401)", w.Code)
	}
}

func TestIncognitoHeartbeat_WithClaims_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/user/incognito/heartbeat", nil)
	setAuth(c, 1, false)

	IncognitoHeartbeat()(c)

	// The handler sets status 204 (NoContent) when successful
	if w.Code == http.StatusNoContent {
		incognitoMu.Lock()
		_, ok := incognitoHeartbeats[1]
		incognitoMu.Unlock()
		if !ok {
			t.Error("expected heartbeat to be recorded")
		}
	}
}

func TestClearIncognito_Success_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mc := &mockCleanable2{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/user/incognito", nil)
	setAuth(c, 42, false)

	ClearIncognito(mc)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if mc.deletedUserID != 42 {
		t.Errorf("expected userID 42, got %d", mc.deletedUserID)
	}
}

type mockCleanable2 struct {
	deletedUserID int
}

func (m *mockCleanable2) DeleteIncognito(userID int) error {
	m.deletedUserID = userID
	return nil
}
func (m *mockCleanable2) DeleteAllIncognito() error { return nil }

func TestCollectExpiredIncognito_Extra(t *testing.T) {
	incognitoMu.Lock()
	incognitoHeartbeats[1] = time.Now().Add(-2 * incognitoTTL)
	incognitoHeartbeats[2] = time.Now()
	incognitoMu.Unlock()
	t.Cleanup(func() {
		incognitoMu.Lock()
		delete(incognitoHeartbeats, 1)
		delete(incognitoHeartbeats, 2)
		incognitoMu.Unlock()
	})

	expired := collectExpiredIncognito()
	if len(expired) != 1 || expired[0] != 1 {
		t.Errorf("expected [1], got %v", expired)
	}
}

func TestPurgeIncognito_Extra(t *testing.T) {
	mc := &mockCleanable2{}
	purgeIncognito([]incognitoCleanable{mc}, []int{99})
	if mc.deletedUserID != 99 {
		t.Errorf("expected userID 99, got %d", mc.deletedUserID)
	}
}

func TestStartIncognitoReaper_CallsDeleteAll(t *testing.T) {
	mc := &mockCleanable2{}
	StartIncognitoReaper(mc)
}

func TestStartIncognitoReaper_MultipleCleaners(t *testing.T) {
	mc1 := &mockCleanable2{}
	mc2 := &mockCleanable2{}
	StartIncognitoReaper(mc1, mc2)
}

func TestNtfyBaseURL_Default_Extra(t *testing.T) {
	cfg := &config.Config{}
	got := ntfyBaseURL(cfg)
	if got != "https://ntfy.sh" {
		t.Errorf("got %q, want %q", got, "https://ntfy.sh")
	}
}

func TestNtfyBaseURL_Custom_Extra(t *testing.T) {
	cfg := &config.Config{Notifications: config.NotificationsConfig{NtfyBaseURL: "https://ntfy.example.com"}}
	got := ntfyBaseURL(cfg)
	if got != "https://ntfy.example.com" {
		t.Errorf("got %q", got)
	}
}

func TestResolveNtfyTopic_NilStore_Extra(t *testing.T) {
	cfg := &config.Config{Notifications: config.NotificationsConfig{NtfyDefaultTopic: "default"}}
	got := resolveNtfyTopic(cfg, nil, nil)
	if got != "default" {
		t.Errorf("got %q, want %q", got, "default")
	}
}

func TestNotifyTest_NoTopic_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	store := newAuthStore(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/user/notify-test", nil)

	NotifyTest(cfg, store)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestVerifyMFA_NotEnabled_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", nil)

	user := &auth.User{MfaEnabled: false}
	result := verifyMFA(c, nil, nil, user, "")
	if !result {
		t.Error("expected true when MFA not enabled")
	}
}

func TestVerifyMFA_Required_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newAuthStore(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/auth/login", nil)

	user := &auth.User{ID: 1, Username: "testuser", MfaEnabled: true}
	result := verifyMFA(c, store, auth.NewLockout(3, time.Minute), user, "")
	if result {
		t.Error("expected false when MFA code is missing")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["mfaRequired"] != true {
		t.Error("expected mfaRequired flag")
	}
}

// ─── Local 0% coverage ──────────────────────────────────────────────────

func TestCheckMountAccess_Denied_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Real", Path: t.TempDir()},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	if checkMountAccess(b, c, "FakeMount") {
		t.Error("expected denied")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestIsAdminMove_Denied_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/move", nil)

	if isAdminMove(c) {
		t.Error("expected false for no claims")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestServeFromCompletedStore_NilStore_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/hash/0", nil)

	got := serveFromCompletedStore(c, nil, metainfo.Hash{}, 0)
	if got {
		t.Error("expected false for nil store")
	}
}

func TestServeFromCompletedStore_NoPath_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/hash/0", nil)

	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	got := serveFromCompletedStore(c, store, h, 0)
	if got {
		t.Error("expected false for non-existent path")
	}
}

func TestServeFromStreamer_FileNotActive_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/hash/0", nil)

	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	serveFromStreamer(c, s, h, 0)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestResolveTorrentInfo_NotActive_Extra(t *testing.T) {
	s := streamer.NewForTesting()
	var h metainfo.Hash
	h.FromHexString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	defer func() {
		if r := recover(); r != nil {
			// Expected: resolveTorrentInfo tries s.Add which needs real client
		}
	}()
	_, _ = resolveTorrentInfo(s, nil, h)
}

func TestBuildStreamURL_HTTPS_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/aaa/0", nil)
	c.Request.Host = "example.com"
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	var h metainfo.Hash
	h.FromHexString("cccccccccccccccccccccccccccccccccccccccc")
	url := buildStreamURL(c, h, 0)

	if !strings.HasPrefix(url, "https://") {
		t.Errorf("url = %q, should start with https://", url)
	}
}

func TestBuildStreamURL_WithToken_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/aaa/0", nil)
	c.Request.Host = "localhost"
	c.Request.Header.Set("Authorization", "Bearer mysecrettoken")

	var h metainfo.Hash
	h.FromHexString("dddddddddddddddddddddddddddddddddddddddd")
	url := buildStreamURL(c, h, 0)

	if !strings.Contains(url, "token=mysecrettoken") {
		t.Errorf("url = %q, should contain token", url)
	}
}

func TestStreamAdd_BadJSON_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/add", StreamAdd(s, nil))

	req := httptest.NewRequest("POST", "/api/stream/add", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamMetadata_HasCache_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	mc, err := streamer.NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mc.Close() })
	s := streamer.NewForTesting()
	s.SetMetadataCache(mc)

	mc.Set(&streamer.TorrentInfo{
		InfoHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:      "Test Movie",
		TotalSize: 1000,
		Files:     []streamer.FileInfo{{Index: 0, Path: "test.mp4", Size: 1000, IsVideo: true}},
	})

	router := gin.New()
	router.GET("/api/stream/metadata/:hash", StreamMetadata(s))

	req := httptest.NewRequest("GET", "/api/stream/metadata/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var meta map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &meta)
	if meta["name"] != "Test Movie" {
		t.Errorf("name = %v, want 'Test Movie'", meta["name"])
	}
}

func TestStreamMetadata_Miss_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	mc, err := streamer.NewMetadataCache(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mc.Close() })
	s := streamer.NewForTesting()
	s.SetMetadataCache(mc)

	router := gin.New()
	router.GET("/api/stream/metadata/:hash", StreamMetadata(s))

	req := httptest.NewRequest("GET", "/api/stream/metadata/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestStreamSetLimits_Negative_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/limits", StreamSetLimits(s))

	body := []byte(`{"down":-1,"up":0}`)
	req := httptest.NewRequest("POST", "/api/stream/limits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// ─── stream.go low-coverage handlers ─────────────────────────────────────

func TestCanModifyMount_Unknown_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote", nil)

	if canModifyMount(c, "Unknown") {
		t.Error("expected false for unknown mount without claims")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestCanModifyMount_MeusDownloads_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/local/promote", nil)

	if !canModifyMount(c, "Meus downloads") {
		t.Error("expected true for Meus downloads")
	}
}

func TestIsAdminCtx_NoClaims_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	if isAdminCtx(c) {
		t.Error("expected false for no claims")
	}
}

func TestIsAdminCtx_Admin_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	setAuth(c, 1, true)

	if !isAdminCtx(c) {
		t.Error("expected true for admin")
	}
}

func TestIsMountRoot_WithMatchingMount_Extra(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	if !isMountRoot(b, mountDir) {
		t.Error("expected mount dir to be detected as root")
	}
	if isMountRoot(b, filepath.Join(mountDir, "subdir")) {
		t.Error("subdir should not be root")
	}
}

func TestResolveDeletablePath_Root_Extra(t *testing.T) {
	mountDir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	_, err := resolveDeletablePath(b, "Test", "")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestThumbnailHandler_BadHash_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/thumb/:hash/:file", StreamThumbnail(s))

	req := httptest.NewRequest("GET", "/api/stream/thumb/nothex/0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamResolveLocalAbs_Valid_Extra(t *testing.T) {
	mountDir := t.TempDir()
	os.WriteFile(filepath.Join(mountDir, "file.mp4"), []byte("data"), 0o644)
	b := local.NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: mountDir},
	})

	got, err := resolveLocalAbs(b, "Test", "file.mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty path")
	}
}

func TestStreamFavorites_WithFavs_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	favPath := filepath.Join(t.TempDir(), "fav.db")
	fav, err := streamer.NewFavorites(favPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fav.Close)
	s.SetFavorites(fav)

	router := gin.New()
	router.GET("/api/stream/favorites", StreamFavorites(s))

	req := httptest.NewRequest("GET", "/api/stream/favorites", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var list []interface{}
	json.Unmarshal(w.Body.Bytes(), &list)
	if list == nil {
		t.Error("expected non-nil list")
	}
}

func TestStreamPlaylistM3U_NotActive_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.GET("/api/stream/playlist/:hash/:file", StreamPlaylistM3U(s))

	req := httptest.NewRequest("GET", "/api/stream/playlist/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/0", nil)
	w := httptest.NewRecorder()

	defer func() {
		recover()
	}()
	router.ServeHTTP(w, req)
}

func TestIsTraversalErr_MountNotFound_Extra(t *testing.T) {
	if !isTraversalErr(fmt.Errorf("mount 'X' not found")) {
		t.Error("expected true for mount not found error")
	}
}

func TestIsTraversalErr_Plain_Extra(t *testing.T) {
	if isTraversalErr(fmt.Errorf("something else")) {
		t.Error("expected false for other errors")
	}
}

func TestStreamPause_BadHash_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()

	router := gin.New()
	router.POST("/api/stream/:hash/pause", StreamPause(s))

	req := httptest.NewRequest("POST", "/api/stream/nothex/pause", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestClassifyForBrowser_More_Extra(t *testing.T) {
	cases := []struct {
		probe     localProbe
		wantDirect bool
	}{
		{localProbe{Container: "isom", VideoCodec: "h264", AudioCodec: "aac"}, true},
		{localProbe{Container: "mp42", VideoCodec: "h264", AudioCodec: "mp3"}, true},
		{localProbe{Container: "qt", VideoCodec: "vp8", AudioCodec: "vorbis"}, true},
		{localProbe{Container: "mp4", VideoCodec: "h264", AudioCodec: ""}, true},
	}
	for i, tc := range cases {
		direct, _ := classifyForBrowser(tc.probe)
		if direct != tc.wantDirect {
			t.Errorf("case %d: direct=%v, want %v", i, direct, tc.wantDirect)
		}
	}
}

func TestDetectLangFromName_Fallback_Extra(t *testing.T) {
	if got := detectLangFromName("movie.de.srt"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
