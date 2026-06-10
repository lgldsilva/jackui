package handlers

// Regression tests for the "mount restricted to a user leaks / restriction is
// lost in prod" bug. Three confirmed failure modes are pinned here:
//  1. PUT /api/mounts with a read-only config.yaml returned 500 with cfg
//     ALREADY mutated (GET lied) — now it rolls back.
//  2. JACKUI_EXTERNAL_MOUNTS spawned an unrestricted twin of an admin-saved
//     mount when the path differed only by a trailing slash (or the name only
//     by case) — now dedupe keys are normalized.
//  3. A duplicate entry reaching the browser could WIDEN visibility — now
//     MountsFor prefers the restricted entry.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
)

// disableAIEnv keeps config.Load from probing AI provider /models endpoints
// (network) when the host environment has provider keys exported.
func disableAIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JACKUI_AI_ENABLED", "0")
}

func putMounts(t *testing.T, router *gin.Engine, mounts []config.ExternalMount) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(mounts)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", "/api/mounts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func getMounts(t *testing.T, router *gin.Engine) []config.ExternalMount {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/mounts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/mounts = %d", w.Code)
	}
	var got []config.ExternalMount
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET /api/mounts body: %v", err)
	}
	return got
}

func newMountsRouter(cfg *config.Config, configPath string, browser *local.Browser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/mounts", MountsGet(cfg))
	router.PUT("/api/mounts", MountsUpdate(cfg, configPath, browser))
	return router
}

// assertSaveFailureRollsBack drives a PUT that must fail at Save time and
// asserts the 500 + untouched cfg (via GET) + untouched browser.
func assertSaveFailureRollsBack(t *testing.T, configPath string) {
	t.Helper()
	cfg := &config.Config{}
	oldMounts := []config.ExternalMount{{Name: "Old", Path: "/old", AllowedUsers: []string{"luiz"}}}
	cfg.External.Mounts = oldMounts
	browser := local.NewBrowser(oldMounts)
	router := newMountsRouter(cfg, configPath, browser)

	w := putMounts(t, router, []config.ExternalMount{{Name: "New", Path: "/new"}})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("grav")) {
		t.Errorf("500 body should hint at config.yaml writability, got: %s", w.Body.String())
	}

	// GET must keep reporting the persisted (old) state — no silent mutation.
	got := getMounts(t, router)
	if len(got) != 1 || got[0].Name != "Old" || len(got[0].AllowedUsers) != 1 {
		t.Errorf("cfg mutated despite Save failure: %+v", got)
	}
	// Browser must be untouched: anonymous still sees nothing, luiz sees Old.
	if anon := browser.MountsFor(""); len(anon) != 0 {
		t.Errorf("browser changed despite Save failure (anon sees %+v)", anon)
	}
	if luiz := browser.MountsFor("luiz"); len(luiz) != 1 || luiz[0].Name != "Old" {
		t.Errorf("browser changed despite Save failure (luiz sees %+v)", luiz)
	}
}

func TestMountsUpdate_SaveFailure_PathIsDirectory_RollsBack(t *testing.T) {
	assertSaveFailureRollsBack(t, t.TempDir()) // WriteFile on a directory fails
}

func TestMountsUpdate_SaveFailure_ReadOnlyFile_RollsBack(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores 0444 file modes")
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8989\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	assertSaveFailureRollsBack(t, configPath)
}

func TestLoad_EnvTrailingSlash_DoesNotDuplicateRestrictedMount(t *testing.T) {
	disableAIEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	saved := &config.Config{}
	saved.External.Mounts = []config.ExternalMount{
		{Name: "Downloads", Path: "/downloads", AllowedUsers: []string{"luiz"}},
	}
	if err := saved.Save(configPath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JACKUI_EXTERNAL_MOUNTS", "Downloads:/downloads/")

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.External.Mounts) != 1 {
		t.Fatalf("env trailing slash spawned a twin: %+v", cfg.External.Mounts)
	}
	m := cfg.External.Mounts[0]
	if m.Path != "/downloads" || len(m.AllowedUsers) != 1 || m.AllowedUsers[0] != "luiz" {
		t.Fatalf("restriction lost: %+v", m)
	}
}

func TestLoad_EnvSameNameDifferentPath_DoesNotDuplicate(t *testing.T) {
	disableAIEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	saved := &config.Config{}
	saved.External.Mounts = []config.ExternalMount{
		{Name: "Downloads", Path: "/data/downloads", AllowedUsers: []string{"luiz"}},
	}
	if err := saved.Save(configPath); err != nil {
		t.Fatal(err)
	}
	// Same name (different case), different path: the saved restricted mount
	// must win — a name twin would break the next PUT ("nome duplicado") and
	// show up unrestricted.
	t.Setenv("JACKUI_EXTERNAL_MOUNTS", "downloads:/downloads")

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.External.Mounts) != 1 {
		t.Fatalf("env name twin not deduped: %+v", cfg.External.Mounts)
	}
	if got := cfg.External.Mounts[0]; got.Path != "/data/downloads" || len(got.AllowedUsers) != 1 {
		t.Fatalf("saved restricted mount should win: %+v", got)
	}
}

func TestMounts_FullCycle_EnvPutSaveLoad_PreservesRestriction(t *testing.T) {
	disableAIEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := (&config.Config{}).Save(configPath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JACKUI_EXTERNAL_MOUNTS", "Downloads:/downloads/")

	// Boot 1: env seeds the mount, admin restricts it via PUT, Save persists.
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.External.Mounts) != 1 {
		t.Fatalf("env seed: %+v", cfg.External.Mounts)
	}
	browser := local.NewBrowser(cfg.External.Mounts)
	router := newMountsRouter(cfg, configPath, browser)
	w := putMounts(t, router, []config.ExternalMount{
		{Name: "Downloads", Path: "/downloads", AllowedUsers: []string{"luiz"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}

	// Boot 2 (restart with the same env): the restriction must survive.
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.External.Mounts) != 1 {
		t.Fatalf("restart duplicated the mount: %+v", reloaded.External.Mounts)
	}
	m := reloaded.External.Mounts[0]
	if len(m.AllowedUsers) != 1 || m.AllowedUsers[0] != "luiz" {
		t.Fatalf("restriction lost across restart: %+v", m)
	}
	b2 := local.NewBrowser(reloaded.External.Mounts)
	if anon := b2.MountsFor(""); len(anon) != 0 {
		t.Errorf("anonymous can see the restricted mount: %+v", anon)
	}
	if luiz := b2.MountsFor("luiz"); len(luiz) != 1 || !luiz[0].Restricted {
		t.Errorf("luiz should see exactly the restricted mount: %+v", luiz)
	}
}

func TestMountsFor_DuplicateName_PrefersRestricted(t *testing.T) {
	restricted := config.ExternalMount{Name: "Downloads", Path: "/downloads", AllowedUsers: []string{"luiz"}}
	open := config.ExternalMount{Name: "Downloads", Path: "/downloads/"}

	// Both orders: a dup must never WIDEN visibility, whichever comes first.
	for name, mounts := range map[string][]config.ExternalMount{
		"restricted-first": {restricted, open},
		"open-first":       {open, restricted},
	} {
		t.Run(name, func(t *testing.T) {
			b := local.NewBrowser(mounts)
			if anon := b.MountsFor(""); len(anon) != 0 {
				t.Errorf("anonymous sees the duplicated mount: %+v", anon)
			}
			luiz := b.MountsFor("luiz")
			if len(luiz) != 1 || luiz[0].Name != "Downloads" || !luiz[0].Restricted {
				t.Errorf("luiz should see one restricted mount, got %+v", luiz)
			}
		})
	}
}
