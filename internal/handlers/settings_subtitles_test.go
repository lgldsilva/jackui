package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
)

// subtitlesLoadedConfigForTest boots a REAL config (config.Load) so the
// defaults machinery under test is the production one, and returns it with its
// temp file path for round-trip assertions.
func subtitlesLoadedConfigForTest(t *testing.T) (*config.Config, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	return cfg, path
}

func TestSubtitlesSettingsGet_Defaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, _ := subtitlesLoadedConfigForTest(t)

	w := invokeCoverageHandler(t, SubtitlesGetSettings(cfg), http.MethodGet, "/api/settings/subtitles", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got subtitlesSettingsBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON body: %v (%s)", err, w.Body.String())
	}
	if got.DefaultLanguages != config.DefaultSubtitlesLanguages {
		t.Errorf("default_languages = %q, want %q", got.DefaultLanguages, config.DefaultSubtitlesLanguages)
	}
	if !got.AutoFetch {
		t.Error("auto_fetch = false, want default true")
	}
	if got.BurnDefault {
		t.Error("burn_default = true, want default false")
	}
}

func TestSubtitlesSettingsGet_NeverSerializesCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, _ := subtitlesLoadedConfigForTest(t)
	cfg.Subtitles.OpenSubtitlesAPIKey = "secret-api-key"
	cfg.Subtitles.OpenSubtitlesUsername = "secret-user"
	cfg.Subtitles.OpenSubtitlesPassword = "secret-pass"

	w := invokeCoverageHandler(t, SubtitlesGetSettings(cfg), http.MethodGet, "/api/settings/subtitles", "")
	body := w.Body.String()
	for _, leak := range []string{"secret-api-key", "secret-user", "secret-pass", "opensubtitles"} {
		if strings.Contains(body, leak) {
			t.Errorf("GET response must never serialize SubtitlesConfig credentials; found %q in %s", leak, body)
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if len(fields) != 3 {
		t.Errorf("GET response must contain ONLY the three settings fields, got %d: %v", len(fields), fields)
	}
}

func TestSubtitlesSettingsGet_EchoesPost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, path := subtitlesLoadedConfigForTest(t)

	post := invokeCoverageHandler(t, SubtitlesUpdateSettings(cfg, path), http.MethodPost, "/api/settings/subtitles",
		`{"default_languages":"en-US,es","auto_fetch":false,"burn_default":true}`)
	if post.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200; body: %s", post.Code, post.Body.String())
	}
	var saved subtitlesSettingsBody
	if err := json.Unmarshal(post.Body.Bytes(), &saved); err != nil {
		t.Fatalf("invalid POST response: %v (%s)", err, post.Body.String())
	}
	if saved.DefaultLanguages != "en-US,es" || saved.AutoFetch || !saved.BurnDefault {
		t.Errorf("POST response must echo the saved values, got %+v", saved)
	}

	get := invokeCoverageHandler(t, SubtitlesGetSettings(cfg), http.MethodGet, "/api/settings/subtitles", "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", get.Code)
	}
	var echoed subtitlesSettingsBody
	if err := json.Unmarshal(get.Body.Bytes(), &echoed); err != nil {
		t.Fatalf("invalid GET response: %v", err)
	}
	if echoed != saved {
		t.Errorf("GET after POST mismatch: got %+v, want %+v", echoed, saved)
	}
}

func TestSubtitlesSettingsPost_RoundTripPersists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, path := subtitlesLoadedConfigForTest(t)

	body := `{"default_languages":"pt-BR,pt,en","auto_fetch":false,"burn_default":true}`
	w := invokeCoverageHandler(t, SubtitlesUpdateSettings(cfg, path), http.MethodPost, "/api/settings/subtitles", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response JSON: %v (%s)", err, w.Body.String())
	}
	if resp["message"] != "settings saved" {
		t.Errorf("message = %v, want %q", resp["message"], "settings saved")
	}
	if resp["restartRequired"] != false {
		t.Errorf("restartRequired = %v, want false (nothing needs a restart)", resp["restartRequired"])
	}

	// Persisted to the config file: a fresh Load must see the saved values —
	// in particular the explicit auto_fetch:false must survive (not be
	// re-defaulted to true by the absent-key probe).
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reloading config failed: %v", err)
	}
	if reloaded.Subtitles.DefaultLanguages != "pt-BR,pt,en" || reloaded.Subtitles.AutoFetch || !reloaded.Subtitles.BurnDefault {
		t.Errorf("round-trip lost the saved values: %+v", reloaded.Subtitles)
	}
}

func TestSubtitlesSettingsGet_FallbacksForHandBuiltConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Hand-built config (no Load): the projection must still answer with the
	// effective default language, mirroring currentDownloadsQueue normalization.
	cfg := &config.Config{}
	w := invokeCoverageHandler(t, SubtitlesGetSettings(cfg), http.MethodGet, "/api/settings/subtitles", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got subtitlesSettingsBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if got.DefaultLanguages != config.DefaultSubtitlesLanguages {
		t.Errorf("default_languages = %q, want fallback %q", got.DefaultLanguages, config.DefaultSubtitlesLanguages)
	}
}

func TestSubtitlesSettingsPost_SaveError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, _ := subtitlesLoadedConfigForTest(t)
	// Unwritable destination: parent directory does not exist.
	badPath := filepath.Join(t.TempDir(), "missing", "config.yaml")

	w := invokeCoverageHandler(t, SubtitlesUpdateSettings(cfg, badPath), http.MethodPost, "/api/settings/subtitles",
		`{"default_languages":"en","auto_fetch":true,"burn_default":false}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "failed to save config") {
		t.Errorf("error message %q must mention the save failure", w.Body.String())
	}
}

func TestSubtitlesSettingsPost_ValidationErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"missing languages", `{}`, "default_languages is required"},
		{"empty languages", `{"default_languages":""}`, "default_languages is required"},
		{"blank languages", `{"default_languages":"   "}`, "default_languages is required"},
		{"space inside code", `{"default_languages":"pt BR"}`, "invalid language code"},
		{"space inside subtag", `{"default_languages":"pt- BR"}`, "invalid language code"},
		{"not a code", `{"default_languages":"portuguese"}`, "invalid language code"},
		{"too many entries", `{"default_languages":"pt,es,en,fr,de,it"}`, "at most 5"},
		{"trailing comma", `{"default_languages":"pt-BR,"}`, "empty language code"},
		{"leading comma", `{"default_languages":",pt"}`, "empty language code"},
		{"invalid chars", `{"default_languages":"pt-BR!"}`, "invalid language code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, path := subtitlesLoadedConfigForTest(t)
			before, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("reading config file failed: %v", readErr)
			}
			w := invokeCoverageHandler(t, SubtitlesUpdateSettings(cfg, path), http.MethodPost, "/api/settings/subtitles", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantMsg) {
				t.Errorf("error message %q must contain %q", w.Body.String(), tc.wantMsg)
			}
			// A rejected request must not touch the config or the file. Load
			// already created the default file; its bytes must be identical.
			if cfg.Subtitles.DefaultLanguages != config.DefaultSubtitlesLanguages || !cfg.Subtitles.AutoFetch {
				t.Errorf("config mutated on validation error: %+v", cfg.Subtitles)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("reading config file failed: %v", readErr)
			}
			if string(after) != string(before) {
				t.Error("config file must not be rewritten on validation error")
			}
		})
	}

	// Malformed JSON body — same contract as the downloads settings route.
	cfg, path := subtitlesLoadedConfigForTest(t)
	w := invokeCoverageHandler(t, SubtitlesUpdateSettings(cfg, path), http.MethodPost, "/api/settings/subtitles", `{"default_languages":`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON: status = %d, want 400", w.Code)
	}
}

func TestSubtitlesSettingsPost_MaxFiveLanguages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, path := subtitlesLoadedConfigForTest(t)

	w := invokeCoverageHandler(t, SubtitlesUpdateSettings(cfg, path), http.MethodPost, "/api/settings/subtitles",
		`{"default_languages":"pt-BR,pt,en,es,fr","auto_fetch":true,"burn_default":false}`)
	if w.Code != http.StatusOK {
		t.Errorf("exactly 5 codes must be accepted, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestSubtitlesSettingsPost_CasingAndTrimKept(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, path := subtitlesLoadedConfigForTest(t)

	w := invokeCoverageHandler(t, SubtitlesUpdateSettings(cfg, path), http.MethodPost, "/api/settings/subtitles",
		`{"default_languages":" pt-BR , zh-Hans ,ES","auto_fetch":true,"burn_default":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var saved subtitlesSettingsBody
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if saved.DefaultLanguages != "pt-BR,zh-Hans,ES" {
		t.Errorf("normalized = %q, want %q (trimmed around commas, original casing kept)", saved.DefaultLanguages, "pt-BR,zh-Hans,ES")
	}
}

// subtitlesSettingsRouter wires the settings routes exactly like setupRouter
// does (claims middleware + AdminOnly on the write group), so the 403 fence is
// part of what's under test.
func subtitlesSettingsRouter(cfg *config.Config, configPath string, claims *auth.Claims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if claims != nil {
			c.Set("auth.claims", claims)
		}
		c.Next()
	})
	api := r.Group("/api")
	api.GET("/settings/subtitles", SubtitlesGetSettings(cfg))
	adminAPI := api.Group("")
	adminAPI.Use(auth.AdminOnly())
	adminAPI.POST("/settings/subtitles", SubtitlesUpdateSettings(cfg, configPath))
	return r
}

func doSubtitlesSettingsRequest(t *testing.T, r *gin.Engine, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/settings/subtitles", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSubtitlesSettings_AdminOnlyGating(t *testing.T) {
	cfg, path := subtitlesLoadedConfigForTest(t)
	admin := &auth.Claims{UserID: 1, Username: "root", Role: auth.RoleAdmin}
	viewer := &auth.Claims{UserID: 2, Username: "viewer", Role: auth.RoleUser}

	// Admin passes the fence.
	if w := doSubtitlesSettingsRequest(t, subtitlesSettingsRouter(cfg, path, admin), http.MethodPost, `{"default_languages":"en","auto_fetch":true,"burn_default":false}`); w.Code != http.StatusOK {
		t.Errorf("admin POST status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	// Non-admin and anonymous are refused with 403 BEFORE the handler runs —
	// use a sentinel value the admin POST above never set.
	for name, claims := range map[string]*auth.Claims{"viewer": viewer, "anonymous": nil} {
		w := doSubtitlesSettingsRequest(t, subtitlesSettingsRouter(cfg, path, claims), http.MethodPost, `{"default_languages":"zz","auto_fetch":false,"burn_default":true}`)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s POST status = %d, want 403", name, w.Code)
		}
		if cfg.Subtitles.DefaultLanguages == "zz" {
			t.Errorf("%s POST must not reach the handler", name)
		}
	}
	// The read side stays in the plain api group: any authenticated shape can GET.
	if w := doSubtitlesSettingsRequest(t, subtitlesSettingsRouter(cfg, path, nil), http.MethodGet, ""); w.Code != http.StatusOK {
		t.Errorf("GET status = %d, want 200 (read is not AdminOnly)", w.Code)
	}
}

func TestSubtitlesSettings_SearchLanguagesSource(t *testing.T) {
	if got := subtitlesSearchLanguages(nil); got != config.DefaultSubtitlesLanguages {
		t.Errorf("nil config = %q, want fallback %q", got, config.DefaultSubtitlesLanguages)
	}
	cfg := &config.Config{} // hand-built: nothing set
	if got := subtitlesSearchLanguages(cfg); got != config.DefaultSubtitlesLanguages {
		t.Errorf("unset config = %q, want fallback %q", got, config.DefaultSubtitlesLanguages)
	}
	cfg.Subtitles.DefaultLanguages = "en,fr"
	if got := subtitlesSearchLanguages(cfg); got != "en,fr" {
		t.Errorf("configured = %q, want en,fr", got)
	}
}

func TestSubtitlesSettings_NormalizeLanguages(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		err  string
	}{
		{raw: "pt-BR,pt", want: "pt-BR,pt"},
		{raw: " pt-BR , pt ", want: "pt-BR,pt"},
		{raw: "zh-Hans", want: "zh-Hans"},
		{raw: "EN", want: "EN"}, // casing kept as sent
		{raw: "", err: "required"},
		{raw: "   ", err: "required"},
		{raw: "pt,", err: "empty language code"},
		{raw: "pt BR", err: "invalid language code"},
		{raw: "p", err: "invalid language code"},          // primary tag too short
		{raw: "portuguese", err: "invalid language code"}, // primary tag too long
		{raw: "pt,es,en,fr,de,it", err: "at most 5"},
	}
	for _, tc := range cases {
		got, errMsg := normalizeSubtitlesLanguages(tc.raw)
		if tc.err != "" {
			if errMsg == "" || !strings.Contains(errMsg, tc.err) {
				t.Errorf("normalize(%q) error = %q, want containing %q", tc.raw, errMsg, tc.err)
			}
			continue
		}
		if errMsg != "" {
			t.Errorf("normalize(%q) unexpected error: %s", tc.raw, errMsg)
			continue
		}
		if got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
