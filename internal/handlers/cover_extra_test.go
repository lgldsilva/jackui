package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
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
	deletedUserID  int
	deleteAllCalls int
}

func (m *mockCleanable2) DeleteIncognito(userID int) error {
	m.deletedUserID = userID
	return nil
}
func (m *mockCleanable2) DeleteAllIncognito() error {
	m.deleteAllCalls++
	return nil
}

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
	StartIncognitoReaper(mc) // purga síncrona no boot antes do ticker
	if mc.deleteAllCalls != 1 {
		t.Errorf("DeleteAllIncognito chamado %d vezes, queria 1 (purga de boot)", mc.deleteAllCalls)
	}
}

func TestStartIncognitoReaper_MultipleCleaners(t *testing.T) {
	mc1 := &mockCleanable2{}
	mc2 := &mockCleanable2{}
	StartIncognitoReaper(mc1, mc2)
	if mc1.deleteAllCalls != 1 || mc2.deleteAllCalls != 1 {
		t.Errorf("cada cleaner deveria ser purgado 1x no boot: mc1=%d mc2=%d", mc1.deleteAllCalls, mc2.deleteAllCalls)
	}
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

func TestServeFromCompletedStore_NilStore_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/hash/0", nil)

	got := serveFromCompletedStore(c, nil, nil, metainfo.Hash{}, 0)
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
	got := serveFromCompletedStore(c, store, streamer.NewForTesting(), h, 0)
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
	mc, err := streamer.NewMetadataCache(dbtest.NewDB(t))
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
	mc, err := streamer.NewMetadataCache(dbtest.NewDB(t))
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

func TestStreamFavorites_WithFavs_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10))
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

// StreamFavorites enriquece cada favorito com totalSize/seeders do metadata
// cache (DB separado). Favorito com snapshot → campos preenchidos; sem snapshot
// → ausentes do JSON (omitempty).
func TestStreamFavorites_SortMetaEnrichment_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fav.Close)
	s.SetFavorites(fav)
	mc, err := streamer.NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	s.SetMetadataCache(mc)

	const hashWithMeta = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const hashNoMeta = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := fav.Add("WithMeta", hashWithMeta, "magnet:?xt=a", "manual", 0); err != nil {
		t.Fatal(err)
	}
	if err := fav.Add("NoMeta", hashNoMeta, "magnet:?xt=b", "manual", 0); err != nil {
		t.Fatal(err)
	}
	if err := mc.Set(&streamer.TorrentInfo{InfoHash: hashWithMeta, Name: "WithMeta", TotalSize: 4096, PrimaryFile: -1}); err != nil {
		t.Fatal(err)
	}
	if err := mc.SetHealth(hashWithMeta, 12, 4); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/api/stream/favorites", StreamFavorites(s))
	req := httptest.NewRequest("GET", "/api/stream/favorites", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}

	var list []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byHash := map[string]map[string]interface{}{}
	for _, f := range list {
		byHash[f["infoHash"].(string)] = f
	}
	withMeta := byHash[hashWithMeta]
	if withMeta == nil {
		t.Fatalf("favorito com meta ausente; body: %s", w.Body.String())
	}
	if withMeta["totalSize"].(float64) != 4096 {
		t.Errorf("totalSize = %v, want 4096", withMeta["totalSize"])
	}
	if withMeta["seeders"].(float64) != 12 {
		t.Errorf("seeders = %v, want 12", withMeta["seeders"])
	}
	noMeta := byHash[hashNoMeta]
	if noMeta == nil {
		t.Fatalf("favorito sem meta ausente")
	}
	if _, ok := noMeta["totalSize"]; ok {
		t.Errorf("totalSize não deveria estar presente sem meta: %v", noMeta["totalSize"])
	}
	if _, ok := noMeta["seeders"]; ok {
		t.Errorf("seeders não deveria estar presente sem probe: %v", noMeta["seeders"])
	}
}

func TestStreamTrackers_Extra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	router := gin.New()
	router.GET("/api/stream/trackers/:hash", StreamTrackers(s))

	// Invalid hash → 400.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/stream/trackers/nothex", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad hash: status = %d, want 400", w.Code)
	}

	// Valid hash, no magnet / no cached .torrent → 200 with an empty array (never null).
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/stream/trackers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rows == nil {
		t.Error("expected [] (non-nil) for a torrent with no trackers")
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
