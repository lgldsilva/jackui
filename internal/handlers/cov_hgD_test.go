package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
)

// hgDnewCtx builds a gin test context wired to a fresh recorder + request.
func hgDnewCtx(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

// hgDinit flips gin to test mode once per test that touches a handler.
func hgDinit() { gin.SetMode(gin.TestMode) }

// --- ntfy push path (postNtfyNotification was 0% covered) -------------------

// TestHgDNotifyTestSuccess drives NotifyTest through postNtfyNotification's happy
// path: a stub ntfy server returns 200, so the POST succeeds end to end. This is
// the only test exercising the request build + Title/Tags headers + 2xx branch.
func Test_hgDNotifyTestSuccess(t *testing.T) {
	hgDinit()
	var hgDhits int32
	var hgDgotTitle, hgDgotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hgDhits, 1)
		hgDgotTitle = r.Header.Get("Title")
		hgDgotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.Notifications.NtfyBaseURL = srv.URL
	cfg.Notifications.NtfyDefaultTopic = "hgDtopic"

	// store nil → resolveNtfyTopic falls back to the configured default topic.
	c, w := hgDnewCtx("POST", "/api/user/notify-test")
	NotifyTest(cfg, nil)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if atomic.LoadInt32(&hgDhits) != 1 {
		t.Fatalf("ntfy server hits = %d, want 1", hgDhits)
	}
	if hgDgotTitle == "" {
		t.Error("expected Title header to be set on the ntfy POST")
	}
	if hgDgotPath != "/hgDtopic" {
		t.Errorf("ntfy path = %q, want /hgDtopic", hgDgotPath)
	}
}

// TestHgDNotifyTestUpstreamError covers postNtfyNotification's >=300 branch:
// the stub ntfy server returns 500, so the handler must surface 502 BadGateway.
func Test_hgDNotifyTestUpstreamError(t *testing.T) {
	hgDinit()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.Notifications.NtfyBaseURL = srv.URL
	cfg.Notifications.NtfyDefaultTopic = "hgDtopic"

	c, w := hgDnewCtx("POST", "/api/user/notify-test")
	NotifyTest(cfg, nil)(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

// TestHgDNotifyTestDialError covers postNtfyNotification's transport-error branch:
// an unroutable base URL makes http.DefaultClient.Do fail → 502 BadGateway.
func Test_hgDNotifyTestDialError(t *testing.T) {
	hgDinit()
	cfg := &config.Config{}
	// 127.0.0.1:0 is never listening → immediate connection refused.
	cfg.Notifications.NtfyBaseURL = "http://127.0.0.1:0"
	cfg.Notifications.NtfyDefaultTopic = "hgDtopic"

	c, w := hgDnewCtx("POST", "/api/user/notify-test")
	NotifyTest(cfg, nil)(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

// TestHgDNotifyTestNoTopic covers the empty-topic guard: no default topic and no
// per-user store → 400 BadRequest before any HTTP call.
func Test_hgDNotifyTestNoTopic(t *testing.T) {
	hgDinit()
	cfg := &config.Config{} // no NtfyDefaultTopic
	c, w := hgDnewCtx("POST", "/api/user/notify-test")
	NotifyTest(cfg, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

// TestHgDNotifyTestPerUserTopic covers resolveNtfyTopic preferring the logged-in
// user's stored topic over the configured default.
func Test_hgDNotifyTestPerUserTopic(t *testing.T) {
	hgDinit()
	var hgDgotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hgDgotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newAuthStore(t)
	user := createTestUser(t, store, "hgDuser", "hgDpass")
	if err := store.SetNtfyTopic(user.ID, "hgDpersonal"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Notifications.NtfyBaseURL = srv.URL
	cfg.Notifications.NtfyDefaultTopic = "hgDdefault"

	c, w := hgDnewCtx("POST", "/api/user/notify-test")
	setAuth(c, user.ID, false)
	NotifyTest(cfg, store)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if hgDgotPath != "/hgDpersonal" {
		t.Errorf("ntfy path = %q, want /hgDpersonal (per-user topic)", hgDgotPath)
	}
}

// --- ntfyBaseURL default fallback -------------------------------------------

// TestHgDNtfyBaseURLDefault covers ntfyBaseURL returning the public default when
// no base URL is configured.
func Test_hgDNtfyBaseURLDefault(t *testing.T) {
	cfg := &config.Config{}
	if got := ntfyBaseURL(cfg); got != "https://ntfy.sh" {
		t.Errorf("ntfyBaseURL = %q, want https://ntfy.sh", got)
	}
	cfg.Notifications.NtfyBaseURL = "https://ntfy.example.com"
	if got := ntfyBaseURL(cfg); got != "https://ntfy.example.com" {
		t.Errorf("ntfyBaseURL = %q, want configured value", got)
	}
}

// --- config: TestJackett error/success via stub upstream --------------------

// TestHgDTestJackettBadGateway covers TestJackett's failure branch: an unroutable
// Jackett URL makes TestConnection fail → 502 with success:false.
func Test_hgDTestJackettBadGateway(t *testing.T) {
	hgDinit()
	cfg := &config.Config{}
	cfg.Jackett.URL = "http://127.0.0.1:0"
	cfg.Jackett.APIKey = "hgDkey"

	c, w := hgDnewCtx("POST", "/api/config/test")
	TestJackett(cfg)(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
}

// --- config: UpdateConfig save-error branch ---------------------------------

// TestHgDUpdateConfigSaveError covers UpdateConfig's Save-failure branch by
// pointing configPath at a directory (write fails) → 500 InternalServerError.
func Test_hgDUpdateConfigSaveError(t *testing.T) {
	hgDinit()
	cfg := &config.Config{}
	dir := t.TempDir() // a directory is not a writable config file path

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/config",
		strings.NewReader(`{"port":8989,"jackett":{"url":"http://x"},"downloadClients":[]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdateConfig(cfg, dir, nil, nil)(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}
